package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"medistock/internal/db"
	"medistock/internal/models"
	"medistock/internal/repo"
)

func TestBackup_OnceCreatesRestorableSnapshot(t *testing.T) {
	dbDir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dbDir, "live.db"))
	if err != nil {
		t.Fatalf("open live db: %v", err)
	}
	defer sqlDB.Close()

	medicines := repo.NewMedicineRepo(sqlDB)
	id, err := medicines.Add(models.Medicine{
		Name: "Paracetamol", Manufacturer: "Test Co", Batch: "B1", ExpiryDate: "2030-01-01",
		Price: 5, Quantity: 20, ReorderLevel: 5,
	})
	if err != nil {
		t.Fatalf("add medicine: %v", err)
	}

	backupDir := filepath.Join(dbDir, "backups")
	r := New(sqlDB, backupDir, 3, nil)

	path, err := r.Once()
	if err != nil {
		t.Fatalf("Once() failed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}

	// Open the backup file as its own independent database and confirm the
	// data is really there - this is the actual point of a backup.
	restored, err := db.Open(path)
	if err != nil {
		t.Fatalf("open backup as db: %v", err)
	}
	defer restored.Close()

	restoredMedicines := repo.NewMedicineRepo(restored)
	m, err := restoredMedicines.Get(id)
	if err != nil {
		t.Fatalf("get medicine from backup: %v", err)
	}
	if m.Name != "Paracetamol" || m.Quantity != 20 {
		t.Errorf("backup data mismatch: got %+v", m)
	}
}

func TestBackup_PrunesOldBackups(t *testing.T) {
	dbDir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dbDir, "live.db"))
	if err != nil {
		t.Fatalf("open live db: %v", err)
	}
	defer sqlDB.Close()

	backupDir := filepath.Join(dbDir, "backups")
	r := New(sqlDB, backupDir, 2, nil)
	r.nowFunc = fakeClock(t)

	for i := 0; i < 5; i++ {
		if _, err := r.Once(); err != nil {
			t.Fatalf("backup #%d failed: %v", i, err)
		}
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected exactly 2 backups retained (keep=2), got %d", len(entries))
	}
}

// fakeClock returns a nowFunc that advances by one second on each call, so
// repeated backups in a fast test loop still get distinct, sortable
// filenames instead of colliding on the same second.
func fakeClock(t *testing.T) func() time.Time {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0
	return func() time.Time {
		n++
		return base.Add(time.Duration(n) * time.Second)
	}
}
