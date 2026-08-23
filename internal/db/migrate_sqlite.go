package db

import "database/sql"
import "fmt"

func migrateSQLite(sqlDB *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS medicines (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
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
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		phone TEXT
	);

	CREATE TABLE IF NOT EXISTS orders (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		customer_id INTEGER,
		created_at TEXT NOT NULL,
		total REAL NOT NULL DEFAULT 0,
		FOREIGN KEY (customer_id) REFERENCES customers(id)
	);

	CREATE TABLE IF NOT EXISTS order_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
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

	// Backfill columns for databases created before these existed.
	if err := addColumnIfMissingSQLite(sqlDB, "medicines", "max_per_order", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissingSQLite(sqlDB, "medicines", "low_stock_alert_sent", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfMissingSQLite(sqlDB, "medicines", "code", "TEXT"); err != nil {
		return err
	}

	// Give any pre-existing rows without a code a unique placeholder so the
	// unique index below can't collide (e.g. two medicines both with a
	// blank/NULL code). Real codes are assigned when medicines are added
	// through the repo layer.
	if _, err := sqlDB.Exec(`UPDATE medicines SET code = 'MED' || id WHERE code IS NULL OR code = ''`); err != nil {
		return err
	}

	_, err := sqlDB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_medicines_code ON medicines(code COLLATE NOCASE)`)
	return err
}

func addColumnIfMissingSQLite(sqlDB *sql.DB, table, column, definition string) error {
	rows, err := sqlDB.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil // already present
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = sqlDB.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition))
	return err
}
