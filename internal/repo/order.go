package repo

import (
	"database/sql"
	"fmt"
	"time"

	"medistock/internal/models"
)

type OrderRepo struct {
	db *sql.DB
}

func NewOrderRepo(db *sql.DB) *OrderRepo {
	return &OrderRepo{db: db}
}

// CartLine is a requested line item before stock/price are resolved.
type CartLine struct {
	MedicineID int64
	Quantity   int
}

// nullableID converts a 0 customer ID (meaning "anonymous / walk-in, no
// record created") into a SQL NULL so the orders.customer_id foreign key
// constraint isn't violated.
func nullableID(id int64) interface{} {
	if id == 0 {
		return nil
	}
	return id
}

// Create places an order inside a single transaction: it verifies stock,
// deducts it, records the order and its line items, and computes the total.
// Either everything succeeds together or nothing is written at all.
func (r *OrderRepo) Create(customerID int64, lines []CartLine) (models.Order, error) {
	if len(lines) == 0 {
		return models.Order{}, fmt.Errorf("cannot create an order with no items")
	}

	tx, err := r.db.Begin()
	if err != nil {
		return models.Order{}, err
	}
	defer tx.Rollback()

	now := time.Now()
	res, err := tx.Exec(`INSERT INTO orders (customer_id, created_at, total) VALUES (?, ?, 0)`,
		nullableID(customerID), now.Format(time.RFC3339))
	if err != nil {
		return models.Order{}, fmt.Errorf("create order: %w", err)
	}
	orderID, err := res.LastInsertId()
	if err != nil {
		return models.Order{}, err
	}

	var total float64
	var items []models.OrderItem
	for _, line := range lines {
		if line.Quantity <= 0 {
			return models.Order{}, fmt.Errorf("invalid quantity %d for medicine %d", line.Quantity, line.MedicineID)
		}

		var name string
		var price float64
		var qty int
		err := tx.QueryRow(`SELECT name, price, quantity FROM medicines WHERE id=?`, line.MedicineID).
			Scan(&name, &price, &qty)
		if err != nil {
			return models.Order{}, fmt.Errorf("medicine %d not found: %w", line.MedicineID, err)
		}
		if qty < line.Quantity {
			return models.Order{}, fmt.Errorf("insufficient stock for %q: have %d, need %d", name, qty, line.Quantity)
		}

		subtotal := price * float64(line.Quantity)
		total += subtotal

		if _, err := tx.Exec(`UPDATE medicines SET quantity = quantity - ? WHERE id = ?`, line.Quantity, line.MedicineID); err != nil {
			return models.Order{}, fmt.Errorf("deduct stock: %w", err)
		}

		itemRes, err := tx.Exec(
			`INSERT INTO order_items (order_id, medicine_id, quantity, unit_price, subtotal)
			 VALUES (?, ?, ?, ?, ?)`,
			orderID, line.MedicineID, line.Quantity, price, subtotal,
		)
		if err != nil {
			return models.Order{}, fmt.Errorf("insert order item: %w", err)
		}
		itemID, _ := itemRes.LastInsertId()

		items = append(items, models.OrderItem{
			ID: itemID, OrderID: orderID, MedicineID: line.MedicineID, MedicineName: name,
			Quantity: line.Quantity, UnitPrice: price, Subtotal: subtotal,
		})
	}

	if _, err := tx.Exec(`UPDATE orders SET total = ? WHERE id = ?`, total, orderID); err != nil {
		return models.Order{}, fmt.Errorf("update order total: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return models.Order{}, fmt.Errorf("commit order: %w", err)
	}

	return models.Order{ID: orderID, CustomerID: customerID, CreatedAt: now, Total: total, Items: items}, nil
}

// Get retrieves a full order with its line items.
func (r *OrderRepo) Get(id int64) (models.Order, error) {
	var o models.Order
	var createdAt string
	var customerID sql.NullInt64
	var customerName sql.NullString
	row := r.db.QueryRow(
		`SELECT o.id, o.customer_id, COALESCE(c.name, 'Walk-in'), o.created_at, o.total
		 FROM orders o LEFT JOIN customers c ON c.id = o.customer_id
		 WHERE o.id = ?`, id)
	if err := row.Scan(&o.ID, &customerID, &customerName, &createdAt, &o.Total); err != nil {
		return o, err
	}
	o.CustomerID = customerID.Int64
	o.CustomerName = customerName.String
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		o.CreatedAt = t
	}

	rows, err := r.db.Query(
		`SELECT oi.id, oi.medicine_id, m.name, oi.quantity, oi.unit_price, oi.subtotal
		 FROM order_items oi JOIN medicines m ON m.id = oi.medicine_id
		 WHERE oi.order_id = ?`, id)
	if err != nil {
		return o, err
	}
	defer rows.Close()

	for rows.Next() {
		var it models.OrderItem
		if err := rows.Scan(&it.ID, &it.MedicineID, &it.MedicineName, &it.Quantity, &it.UnitPrice, &it.Subtotal); err != nil {
			return o, err
		}
		it.OrderID = o.ID
		o.Items = append(o.Items, it)
	}
	return o, rows.Err()
}

// List returns a summary of all orders, most recent first.
func (r *OrderRepo) List() ([]models.Order, error) {
	rows, err := r.db.Query(
		`SELECT o.id, o.customer_id, COALESCE(c.name, 'Walk-in'), o.created_at, o.total
		 FROM orders o LEFT JOIN customers c ON c.id = o.customer_id
		 ORDER BY o.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Order
	for rows.Next() {
		var o models.Order
		var createdAt string
		var customerID sql.NullInt64
		if err := rows.Scan(&o.ID, &customerID, &o.CustomerName, &createdAt, &o.Total); err != nil {
			return nil, err
		}
		o.CustomerID = customerID.Int64
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			o.CreatedAt = t
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
