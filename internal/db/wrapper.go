package db

import "database/sql"

// DB wraps *sql.DB so the repo layer can write one set of queries (using
// `?` placeholders and relying on LastInsertId, both SQLite conventions)
// that work unchanged against Postgres too - the wrapper rewrites
// placeholders and, for inserts, uses Postgres's `RETURNING id` instead of
// LastInsertId (which Postgres's database/sql driver doesn't support).
type DB struct {
	sql    *sql.DB
	Driver string

	// PostgresConfig holds the connection details used to open this DB,
	// when Driver is DriverPostgres - kept around so tools like the backup
	// runner (which shells out to pg_dump) don't need the caller to thread
	// connection details through separately.
	PostgresConfig Config
}

func (d *DB) Exec(query string, args ...interface{}) (sql.Result, error) {
	return d.sql.Exec(rebind(d.Driver, query), args...)
}

func (d *DB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return d.sql.Query(rebind(d.Driver, query), args...)
}

func (d *DB) QueryRow(query string, args ...interface{}) *sql.Row {
	return d.sql.QueryRow(rebind(d.Driver, query), args...)
}

// InsertReturningID runs an INSERT and returns the new row's id, using
// whichever mechanism the driver supports (LastInsertId for SQLite,
// `RETURNING id` for Postgres). query must not already contain RETURNING.
func (d *DB) InsertReturningID(query string, args ...interface{}) (int64, error) {
	if d.Driver == DriverPostgres {
		var id int64
		err := d.sql.QueryRow(rebind(d.Driver, query)+" RETURNING id", args...).Scan(&id)
		return id, err
	}
	res, err := d.sql.Exec(rebind(d.Driver, query), args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) Begin() (*Tx, error) {
	tx, err := d.sql.Begin()
	if err != nil {
		return nil, err
	}
	return &Tx{sql: tx, driver: d.Driver}, nil
}

// Ping, Close, SetMaxOpenConns and Raw give callers (backups, health
// checks) an escape hatch to the underlying *sql.DB when they need it.
func (d *DB) Ping() error           { return d.sql.Ping() }
func (d *DB) Close() error          { return d.sql.Close() }
func (d *DB) SetMaxOpenConns(n int) { d.sql.SetMaxOpenConns(n) }
func (d *DB) Raw() *sql.DB          { return d.sql }
func (d *DB) IsPostgres() bool      { return d.Driver == DriverPostgres }

// Tx mirrors DB's placeholder-rewriting behavior for transactions.
type Tx struct {
	sql    *sql.Tx
	driver string
}

func (t *Tx) Exec(query string, args ...interface{}) (sql.Result, error) {
	return t.sql.Exec(rebind(t.driver, query), args...)
}

func (t *Tx) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return t.sql.Query(rebind(t.driver, query), args...)
}

func (t *Tx) QueryRow(query string, args ...interface{}) *sql.Row {
	return t.sql.QueryRow(rebind(t.driver, query), args...)
}

func (t *Tx) InsertReturningID(query string, args ...interface{}) (int64, error) {
	if t.driver == DriverPostgres {
		var id int64
		err := t.sql.QueryRow(rebind(t.driver, query)+" RETURNING id", args...).Scan(&id)
		return id, err
	}
	res, err := t.sql.Exec(rebind(t.driver, query), args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (t *Tx) Commit() error   { return t.sql.Commit() }
func (t *Tx) Rollback() error { return t.sql.Rollback() }
