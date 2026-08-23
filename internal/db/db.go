// Package db handles the database connection and schema migrations for the
// offline medicine ordering system. Two engines are supported: SQLite
// (the default - a single local file, no server, no setup) and PostgreSQL
// (for sites that want a heavier local database engine). Either way, the
// database server - if there is one - runs on the same machine as the
// kiosk; nothing here ever needs the internet.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

// Config selects and configures the database engine to connect to.
type Config struct {
	// Driver is DriverSQLite (default) or DriverPostgres.
	Driver string

	// SQLite
	Path string

	// Postgres
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string // e.g. "disable" for a local, offline instance
}

// Open connects to the configured database and ensures the schema exists.
// It works fully offline - even with Driver=DriverPostgres, this only ever
// talks to the host/port given in cfg, which is expected to be a database
// server running on the same local machine or network, never the internet.
func Open(cfg Config) (*DB, error) {
	switch cfg.Driver {
	case "", DriverSQLite:
		return openSQLite(cfg.Path)
	case DriverPostgres:
		return openPostgres(cfg)
	default:
		return nil, fmt.Errorf("unknown database driver %q (want %q or %q)", cfg.Driver, DriverSQLite, DriverPostgres)
	}
}

// OpenSQLite is a convenience wrapper for the common case (Open with
// Driver: DriverSQLite), kept so callers - and the many existing tests -
// that only ever used SQLite don't need to build a Config.
func OpenSQLite(dbPath string) (*DB, error) {
	return openSQLite(dbPath)
}

func openSQLite(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
	}

	sqlDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	// A local file DB is happiest with a single writer connection to avoid
	// "database is locked" errors from concurrent goroutines.
	sqlDB.SetMaxOpenConns(1)

	if _, err := sqlDB.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	d := &DB{sql: sqlDB, Driver: DriverSQLite}
	if err := migrateSQLite(sqlDB); err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return d, nil
}

func openPostgres(cfg Config) (*DB, error) {
	if cfg.Host == "" {
		cfg.Host = "localhost"
	}
	if cfg.Port == 0 {
		cfg.Port = 5432
	}
	if cfg.SSLMode == "" {
		cfg.SSLMode = "disable" // a local/offline Postgres instance has no cert to verify
	}
	if cfg.DBName == "" {
		return nil, fmt.Errorf("postgres database name is required")
	}

	dsn := fmt.Sprintf("host=%s port=%d dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.DBName, cfg.SSLMode)
	if cfg.User != "" {
		dsn += " user=" + cfg.User
	}
	if cfg.Password != "" {
		dsn += " password=" + cfg.Password
	}

	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("connect to postgres at %s:%d: %w", cfg.Host, cfg.Port, err)
	}

	d := &DB{sql: sqlDB, Driver: DriverPostgres, PostgresConfig: cfg}
	if err := migratePostgres(sqlDB); err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return d, nil
}

// rebind rewrites a `?`-style query (SQLite/MySQL style, used throughout
// the repo layer) into Postgres's `$1, $2, ...` positional style. SQLite
// queries pass through unchanged.
func rebind(driver, query string) string {
	if driver != DriverPostgres || !strings.Contains(query, "?") {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
