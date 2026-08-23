// Package backup periodically snapshots the live database to a separate
// directory, so a corrupted disk, a bad shutdown, or a mistaken stock edit
// doesn't mean losing the whole inventory/order history. This matters more
// here than in a typical web app: there's no cloud database with its own
// backup story - whichever local database is in use IS the whole system of
// record, sitting on one local disk (or one local server).
package backup

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"medistock/internal/db"
)

// Runner periodically backs up the database. For SQLite it uses SQLite's
// own VACUUM INTO, which produces a consistent, compacted snapshot even
// while the database is in active use - no need to stop the app or lock
// writers. For Postgres it shells out to pg_dump, the standard Postgres
// backup tool, if it's installed on the machine.
type Runner struct {
	db      *db.DB
	dir     string
	keep    int
	log     *slog.Logger
	nowFunc func() time.Time

	// pgDump lets tests swap out the real pg_dump invocation; nil means
	// "use the real command".
	pgDump func(path string) error
}

// New creates a backup Runner. dir is created if missing. keep is how many
// recent backups to retain (older ones are deleted after each run); keep
// <= 0 means unlimited (not recommended for long-running kiosks - disk
// fills up eventually).
func New(sqlDB *db.DB, dir string, keep int, log *slog.Logger) *Runner {
	return &Runner{db: sqlDB, dir: dir, keep: keep, log: log, nowFunc: time.Now}
}

func (r *Runner) fileExt() string {
	if r.db.IsPostgres() {
		return "sql"
	}
	return "db"
}

// Once performs a single backup immediately and returns the path written.
func (r *Runner) Once() (string, error) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	name := fmt.Sprintf("medistock-%s.%s", r.nowFunc().UTC().Format("20060102-150405"), r.fileExt())
	path := filepath.Join(r.dir, name)

	if r.db.IsPostgres() {
		if err := r.backupPostgres(path); err != nil {
			return "", err
		}
	} else {
		// VACUUM INTO writes a fully consistent copy in one step, safe to
		// run concurrently with normal reads/writes on the source database.
		if _, err := r.db.Exec(`VACUUM INTO ?`, path); err != nil {
			return "", fmt.Errorf("backup database: %w", err)
		}
	}

	if r.log != nil {
		r.log.Info("database backed up", "path", path)
	}

	if err := r.pruneOldBackups(); err != nil && r.log != nil {
		r.log.Warn("failed to prune old backups", "error", err)
	}

	return path, nil
}

func (r *Runner) backupPostgres(path string) error {
	if r.pgDump != nil {
		return r.pgDump(path)
	}
	if _, err := exec.LookPath("pg_dump"); err != nil {
		return fmt.Errorf("pg_dump not found on PATH - install the postgresql-client tools to enable backups, or back up the Postgres server through your usual means: %w", err)
	}

	cfg := r.db.PostgresConfig
	args := []string{"-h", cfg.Host, "-p", fmt.Sprintf("%d", cfg.Port), "-U", cfg.User, "-d", cfg.DBName, "-f", path, "--no-password"}
	cmd := exec.Command("pg_dump", args...)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+cfg.Password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_dump failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (r *Runner) pruneOldBackups() error {
	if r.keep <= 0 {
		return nil
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return err
	}

	var backups []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "medistock-") {
			backups = append(backups, e.Name())
		}
	}
	sort.Strings(backups) // timestamped names sort chronologically

	if len(backups) <= r.keep {
		return nil
	}
	toDelete := backups[:len(backups)-r.keep]
	for _, name := range toDelete {
		if err := os.Remove(filepath.Join(r.dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// Run backs up the database immediately, then again every interval, until
// stop is closed. Intended to run in its own goroutine for the lifetime of
// the process.
func (r *Runner) Run(interval time.Duration, stop <-chan struct{}) {
	if _, err := r.Once(); err != nil && r.log != nil {
		r.log.Error("initial backup failed", "error", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := r.Once(); err != nil && r.log != nil {
				r.log.Error("scheduled backup failed", "error", err)
			}
		case <-stop:
			return
		}
	}
}
