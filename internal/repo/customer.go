package repo

import (
	"database/sql"

	"medistock/internal/db"
	"medistock/internal/models"
)

type CustomerRepo struct {
	db *db.DB
}

func NewCustomerRepo(d *db.DB) *CustomerRepo {
	return &CustomerRepo{db: d}
}

func (r *CustomerRepo) Add(c models.Customer) (int64, error) {
	return r.db.InsertReturningID(`INSERT INTO customers (name, phone) VALUES (?, ?)`, c.Name, c.Phone)
}

func (r *CustomerRepo) List() ([]models.Customer, error) {
	rows, err := r.db.Query(`SELECT id, name, phone FROM customers ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Customer
	for rows.Next() {
		var c models.Customer
		if err := rows.Scan(&c.ID, &c.Name, &c.Phone); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// FindOrCreateByPhone returns an existing customer matching the phone number,
// or creates a new one if none exists.
func (r *CustomerRepo) FindOrCreateByPhone(name, phone string) (models.Customer, error) {
	var c models.Customer
	row := r.db.QueryRow(`SELECT id, name, phone FROM customers WHERE phone = ?`, phone)
	err := row.Scan(&c.ID, &c.Name, &c.Phone)
	if err == nil {
		return c, nil
	}
	if err != sql.ErrNoRows {
		return c, err
	}

	id, err := r.Add(models.Customer{Name: name, Phone: phone})
	if err != nil {
		return c, err
	}
	return models.Customer{ID: id, Name: name, Phone: phone}, nil
}
