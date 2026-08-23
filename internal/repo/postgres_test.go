package repo

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	_ "github.com/lib/pq"

	"medistock/internal/db"
)

// postgresTestConfig reads connection details for a local Postgres test
// instance from the environment (matching the flags main.go exposes),
// falling back to the common local defaults used throughout development
// (see README's Postgres section). It never points anywhere but a local
// host - there's no scenario where these tests should touch the network.
func postgresTestConfig() db.Config {
	host := os.Getenv("MEDISTOCK_TEST_PG_HOST")
	if host == "" {
		host = "localhost"
	}
	user := os.Getenv("MEDISTOCK_TEST_PG_USER")
	if user == "" {
		user = "postgres"
	}
	password := os.Getenv("MEDISTOCK_TEST_PG_PASSWORD")
	if password == "" {
		password = "postgres"
	}
	return db.Config{Driver: db.DriverPostgres, Host: host, Port: 5432, User: user, Password: password, SSLMode: "disable"}
}

// newPostgresTestDB creates a throwaway database on a local Postgres
// server, opens it through the same db.Open path the real app uses, and
// registers cleanup to drop it afterwards. If no Postgres server is
// reachable, the test is skipped rather than failed - this exercises real
// Postgres behavior (schema, RETURNING id, case-insensitive code lookups)
// when a server is available, the same "skip, don't fail CI, when the
// environment can't provide it" approach used for the mesh UDP broadcast
// test.
func newPostgresTestDB(t *testing.T) *db.DB {
	t.Helper()
	cfg := postgresTestConfig()

	adminDSN := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=postgres sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.SSLMode)
	admin, err := sql.Open("postgres", adminDSN)
	if err != nil {
		t.Skipf("postgres not available for testing: %v", err)
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		t.Skipf("postgres not reachable at %s:%d for testing (set MEDISTOCK_TEST_PG_* to point at a real instance): %v", cfg.Host, cfg.Port, err)
	}

	dbName := fmt.Sprintf("medistock_test_%d", os.Getpid())
	// Best-effort: a stale db from a crashed previous run shouldn't fail this one.
	admin.Exec(`DROP DATABASE IF EXISTS ` + dbName)
	if _, err := admin.Exec(`CREATE DATABASE ` + dbName); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		// Can't drop a database while other connections are open against it.
		cleanupAdmin, err := sql.Open("postgres", adminDSN)
		if err != nil {
			return
		}
		defer cleanupAdmin.Close()
		cleanupAdmin.Exec(`DROP DATABASE IF EXISTS ` + dbName)
	})

	cfg.DBName = dbName
	sqlDB, err := db.Open(cfg)
	if err != nil {
		t.Fatalf("open postgres test db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

// TestPostgres_MedicineAddGetAndCodeUniqueness mirrors
// TestMedicine_AddAndGet / TestMedicine_AutoCodeUniqueness but against a
// real Postgres database, to confirm the RETURNING-id insert path and the
// LOWER(code) case-insensitive uniqueness index actually work there (not
// just against SQLite).
func TestPostgres_MedicineAddGetAndCodeUniqueness(t *testing.T) {
	sqlDB := newPostgresTestDB(t)
	repo := NewMedicineRepo(sqlDB)

	id, err := repo.Add(testMedicine("Paracetamol 500mg", 100, 0))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected a non-zero id from Postgres RETURNING id, got 0")
	}

	got, err := repo.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Paracetamol 500mg" || got.Quantity != 100 {
		t.Errorf("unexpected medicine: %+v", got)
	}
	if got.Code == "" {
		t.Errorf("expected an auto-generated code, got empty")
	}

	// Same name again should get a different, still-unique code.
	id2, err := repo.Add(testMedicine("Paracetamol 500mg", 50, 0))
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	got2, err := repo.Get(id2)
	if err != nil {
		t.Fatalf("Get second: %v", err)
	}
	if got2.Code == got.Code {
		t.Errorf("expected unique codes, both got %q", got.Code)
	}

	// FindByCode should match case-insensitively via the LOWER(code) index.
	found, err := repo.FindByCode(got.Code)
	if err != nil {
		t.Fatalf("FindByCode exact: %v", err)
	}
	if found.ID != id {
		t.Errorf("FindByCode exact case: expected id %d, got %d", id, found.ID)
	}

	foundLower, err := repo.FindByCode(lowerCode(got.Code))
	if err != nil {
		t.Fatalf("FindByCode lowercase: %v", err)
	}
	if foundLower.ID != id {
		t.Errorf("FindByCode lowercase: expected id %d, got %d", id, foundLower.ID)
	}
}

// TestPostgres_OrderCreate_TransactionalStockDeduction mirrors
// TestOrder_Create_DeductsStockAndComputesTotal and
// TestOrder_Create_RollsBackOnInsufficientStock against real Postgres, to
// confirm the transaction (and its RETURNING-id inserts for both the order
// and its line items) really commits/rolls back atomically there.
func TestPostgres_OrderCreate_TransactionalStockDeduction(t *testing.T) {
	sqlDB := newPostgresTestDB(t)
	medicines := NewMedicineRepo(sqlDB)
	orders := NewOrderRepo(sqlDB)

	medID, err := medicines.Add(testMedicine("ORS Sachet", 10, 0))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	order, err := orders.Create(0, []CartLine{{MedicineID: medID, Quantity: 4}})
	if err != nil {
		t.Fatalf("Create order: %v", err)
	}
	if order.ID == 0 {
		t.Fatalf("expected non-zero order id")
	}
	if len(order.Items) != 1 || order.Items[0].ID == 0 {
		t.Fatalf("expected one order item with a non-zero id, got %+v", order.Items)
	}

	med, err := medicines.Get(medID)
	if err != nil {
		t.Fatalf("Get after order: %v", err)
	}
	if med.Quantity != 6 {
		t.Errorf("expected 10-4=6 remaining, got %d", med.Quantity)
	}

	// Ordering more than remains must roll back entirely - stock unchanged.
	if _, err := orders.Create(0, []CartLine{{MedicineID: medID, Quantity: 999}}); err == nil {
		t.Fatalf("expected an error ordering more than available stock")
	}
	medAfter, err := medicines.Get(medID)
	if err != nil {
		t.Fatalf("Get after failed order: %v", err)
	}
	if medAfter.Quantity != 6 {
		t.Errorf("failed order should not have changed stock: expected 6, got %d", medAfter.Quantity)
	}
}

func lowerCode(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
