package db

import "database/sql"
import "fmt"

func migratePostgres(sqlDB *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS medicines (
		id SERIAL PRIMARY KEY,
		name TEXT NOT NULL,
		code TEXT,
		manufacturer TEXT,
		batch TEXT,
		expiry_date TEXT,
		price REAL NOT NULL DEFAULT 0,
		quantity INTEGER NOT NULL DEFAULT 0,
		reorder_level INTEGER NOT NULL DEFAULT 10,
		max_per_order INTEGER NOT NULL DEFAULT 0,
		low_stock_alert_sent INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS customers (
		id SERIAL PRIMARY KEY,
		name TEXT NOT NULL,
		phone TEXT
	);

	CREATE TABLE IF NOT EXISTS orders (
		id SERIAL PRIMARY KEY,
		customer_id INTEGER,
		created_at TEXT NOT NULL,
		total REAL NOT NULL DEFAULT 0,
		FOREIGN KEY (customer_id) REFERENCES customers(id)
	);

	CREATE TABLE IF NOT EXISTS order_items (
		id SERIAL PRIMARY KEY,
		order_id INTEGER NOT NULL,
		medicine_id INTEGER NOT NULL,
		quantity INTEGER NOT NULL,
		unit_price REAL NOT NULL,
		subtotal REAL NOT NULL,
		FOREIGN KEY (order_id) REFERENCES orders(id),
		FOREIGN KEY (medicine_id) REFERENCES medicines(id)
	);

	CREATE INDEX IF NOT EXISTS idx_medicines_name ON medicines(name);
	CREATE INDEX IF NOT EXISTS idx_orders_created_at ON orders(created_at);
	`

	if _, err := sqlDB.Exec(schema); err != nil {
		return err
	}

	if err := addColumnIfMissingPostgres(sqlDB, "medicines", "max_per_order", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissingPostgres(sqlDB, "medicines", "low_stock_alert_sent", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissingPostgres(sqlDB, "medicines", "code", "TEXT"); err != nil {
		return err
	}

	if _, err := sqlDB.Exec(`UPDATE medicines SET code = 'MED' || id WHERE code IS NULL OR code = ''`); err != nil {
		return err
	}

	// Postgres has no COLLATE NOCASE; a case-insensitive uniqueness
	// constraint is done with a functional index on lower(code) instead -
	// FindByCode/generateCode match it with LOWER(code) = LOWER($1).
	_, err := sqlDB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_medicines_code ON medicines(LOWER(code))`)
	return err
}

func addColumnIfMissingPostgres(sqlDB *sql.DB, table, column, definition string) error {
	var exists bool
	err := sqlDB.QueryRow(
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = $1 AND column_name = $2
		)`, table, column).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	_, err = sqlDB.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition))
	return err
}
