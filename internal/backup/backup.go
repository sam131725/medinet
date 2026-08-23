// Package backup periodically snapshots the live SQLite database to a
// separate directory, so a corrupted disk, a bad shutdown, or a mistaken
// stock edit doesn't mean losing the whole inventory/order history. This
// matters more here than in a typical web app: there's no cloud database
// with its own backup story - the SQLite file IS the whole system of
// record, sitting on one local disk.
package backup

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Runner periodically backs up a SQLite database using SQLite's own
// VACUUM INTO, which produces a consistent, compacted snapshot even while
// the database is in active use - no need to stop the app or lock writers.
type Runner struct {
	db      *sql.DB
	dir     string
	keep    int
	log     *slog.Logger
	nowFunc func() time.Time
}

// New creates a backup Runner. dir is created if missing. keep is how many
// recent backups to retain (older ones are deleted after each run); keep
// <= 0 means unlimited (not recommended for long-running kiosks - disk
// fills up eventually).
func New(db *sql.DB, dir string, keep int, log *slog.Logger) *Runner {
	return &Runner{db: db, dir: dir, keep: keep, log: log, nowFunc: time.Now}
}

// Once performs a single backup immediately and returns the path written.
func (r *Runner) Once() (string, error) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	name := fmt.Sprintf("medistock-%s.db", r.nowFunc().UTC().Format("20060102-150405"))
	path := filepath.Join(r.dir, name)

	// VACUUM INTO writes a fully consistent copy in one step, safe to run
	// concurrently with normal reads/writes on the source database.
	if _, err := r.db.Exec(`VACUUM INTO ?`, path); err != nil {
		return "", fmt.Errorf("backup database: %w", err)
	}

	if r.log != nil {
		r.log.Info("database backed up", "path", path)
	}

	if err := r.pruneOldBackups(); err != nil && r.log != nil {
		r.log.Warn("failed to prune old backups", "error", err)
	}

	return path, nil
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
		if !e.IsDir() && strings.HasPrefix(e.Name(), "medistock-") && strings.HasSuffix(e.Name(), ".db") {
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
