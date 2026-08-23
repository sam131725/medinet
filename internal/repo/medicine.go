package repo

import (
	"fmt"
	"regexp"
	"strings"

	"medistock/internal/db"
	"medistock/internal/models"
)

// MedicineRepo provides CRUD and stock operations for medicines.
type MedicineRepo struct {
	db *db.DB
}

func NewMedicineRepo(d *db.DB) *MedicineRepo {
	return &MedicineRepo{db: d}
}

const medicineColumns = `id, name, code, manufacturer, batch, expiry_date, price, quantity, reorder_level, max_per_order, low_stock_alert_sent`

func scanMedicine(row interface{ Scan(...interface{}) error }) (models.Medicine, error) {
	var m models.Medicine
	var alertSent int
	err := row.Scan(&m.ID, &m.Name, &m.Code, &m.Manufacturer, &m.Batch, &m.ExpiryDate, &m.Price, &m.Quantity, &m.ReorderLevel, &m.MaxPerOrder, &alertSent)
	m.LowStockAlertSent = alertSent != 0
	return m, err
}

var nonAlnum = regexp.MustCompile(`[^A-Z0-9]+`)

// codeEqualsClause returns a WHERE-fragment for a case-insensitive code
// match plus its argument, adapting to whichever database is in use:
// SQLite's COLLATE NOCASE, or a LOWER() comparison for Postgres (which has
// no NOCASE collation).
func (r *MedicineRepo) codeEqualsClause(code string) (string, string) {
	if r.db.Driver == db.DriverPostgres {
		return "LOWER(code) = LOWER(?)", code
	}
	return "code = ? COLLATE NOCASE", code
}

// generateCode derives a short, SMS-friendly code from a medicine name
// (e.g. "Paracetamol 500mg" -> "PARACETA"), appending a numeric suffix if
// that code is already taken.
func (r *MedicineRepo) generateCode(name string) (string, error) {
	base := nonAlnum.ReplaceAllString(strings.ToUpper(name), "")
	if len(base) > 8 {
		base = base[:8]
	}
	if base == "" {
		base = "MED"
	}

	candidate := base
	for i := 1; ; i++ {
		clause, arg := r.codeEqualsClause(candidate)
		var exists int
		err := r.db.QueryRow(`SELECT COUNT(*) FROM medicines WHERE `+clause, arg).Scan(&exists)
		if err != nil {
			return "", err
		}
		if exists == 0 {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s%d", base, i)
	}
}

func (r *MedicineRepo) Add(m models.Medicine) (int64, error) {
	if m.Code == "" {
		code, err := r.generateCode(m.Name)
		if err != nil {
			return 0, fmt.Errorf("generate code: %w", err)
		}
		m.Code = code
	} else {
		m.Code = strings.ToUpper(strings.TrimSpace(m.Code))
	}

	id, err := r.db.InsertReturningID(
		`INSERT INTO medicines (name, code, manufacturer, batch, expiry_date, price, quantity, reorder_level, max_per_order)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Name, m.Code, m.Manufacturer, m.Batch, m.ExpiryDate, m.Price, m.Quantity, m.ReorderLevel, m.MaxPerOrder,
	)
	if err != nil {
		return 0, fmt.Errorf("insert medicine: %w", err)
	}
	return id, nil
}

func (r *MedicineRepo) Update(m models.Medicine) error {
	_, err := r.db.Exec(
		`UPDATE medicines SET name=?, code=?, manufacturer=?, batch=?, expiry_date=?, price=?, quantity=?, reorder_level=?, max_per_order=?
		 WHERE id=?`,
		m.Name, strings.ToUpper(strings.TrimSpace(m.Code)), m.Manufacturer, m.Batch, m.ExpiryDate, m.Price, m.Quantity, m.ReorderLevel, m.MaxPerOrder, m.ID,
	)
	return err
}

func (r *MedicineRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM medicines WHERE id=?`, id)
	return err
}

func (r *MedicineRepo) Get(id int64) (models.Medicine, error) {
	row := r.db.QueryRow(`SELECT `+medicineColumns+` FROM medicines WHERE id=?`, id)
	return scanMedicine(row)
}

// FindByCode looks up a medicine by its short SMS-ordering code
// (case-insensitive). Used by the SMS order handler.
func (r *MedicineRepo) FindByCode(code string) (models.Medicine, error) {
	clause, arg := r.codeEqualsClause(strings.TrimSpace(code))
	row := r.db.QueryRow(`SELECT `+medicineColumns+` FROM medicines WHERE `+clause, arg)
	return scanMedicine(row)
}

func (r *MedicineRepo) List() ([]models.Medicine, error) {
	return r.queryList(`SELECT ` + medicineColumns + ` FROM medicines ORDER BY name ASC`)
}

// Search finds medicines whose name contains the given (case-insensitive) query.
func (r *MedicineRepo) Search(query string) ([]models.Medicine, error) {
	return r.queryList(`SELECT `+medicineColumns+` FROM medicines WHERE name LIKE ? ORDER BY name ASC`, "%"+query+"%")
}

// Available returns medicines currently in stock (quantity > 0), optionally
// filtered by a name search. Intended for the customer-facing kiosk, which
// should only show items people can actually receive.
func (r *MedicineRepo) Available(query string) ([]models.Medicine, error) {
	if query == "" {
		return r.queryList(`SELECT ` + medicineColumns + ` FROM medicines WHERE quantity > 0 ORDER BY name ASC`)
	}
	return r.queryList(`SELECT `+medicineColumns+` FROM medicines WHERE quantity > 0 AND name LIKE ? ORDER BY name ASC`, "%"+query+"%")
}

// LowStock returns medicines whose quantity is at or below their reorder level.
func (r *MedicineRepo) LowStock() ([]models.Medicine, error) {
	return r.queryList(`SELECT ` + medicineColumns + ` FROM medicines WHERE quantity <= reorder_level ORDER BY quantity ASC`)
}

func (r *MedicineRepo) queryList(query string, args ...interface{}) ([]models.Medicine, error) {
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Medicine
	for rows.Next() {
		m, err := scanMedicine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AdjustStock changes quantity by delta (can be negative). It errors if the
// resulting quantity would be negative.
func (r *MedicineRepo) AdjustStock(id int64, delta int) error {
	res, err := r.db.Exec(
		`UPDATE medicines SET quantity = quantity + ? WHERE id = ? AND quantity + ? >= 0`,
		delta, id, delta,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("insufficient stock or medicine not found (id=%d)", id)
	}
	return nil
}

// SetLowStockAlertSent records whether a reorder alert has already been
// sent for this medicine, so repeated stock checks don't re-send the same
// SMS alert on every single order. Callers reset it to false once stock is
// replenished above the reorder level.
func (r *MedicineRepo) SetLowStockAlertSent(id int64, sent bool) error {
	val := 0
	if sent {
		val = 1
	}
	_, err := r.db.Exec(`UPDATE medicines SET low_stock_alert_sent = ? WHERE id = ?`, val, id)
	return err
}
