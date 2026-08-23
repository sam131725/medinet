package repo

import (
	"path/filepath"
	"testing"

	"medistock/internal/db"
	"medistock/internal/models"
)

func newTestDB(t *testing.T) (*MedicineRepo, *CustomerRepo, *OrderRepo) {
	t.Helper()
	sqlDB, err := db.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return NewMedicineRepo(sqlDB), NewCustomerRepo(sqlDB), NewOrderRepo(sqlDB)
}

func testMedicine(name string, qty, maxPerOrder int) models.Medicine {
	return models.Medicine{
		Name: name, Manufacturer: "Test Co", Batch: "B1", ExpiryDate: "2030-01-01",
		Price: 5, Quantity: qty, ReorderLevel: 5, MaxPerOrder: maxPerOrder,
	}
}

func TestMedicine_AddAndGet(t *testing.T) {
	medicines, _, _ := newTestDB(t)
	id, err := medicines.Add(testMedicine("Paracetamol", 20, 0))
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	m, err := medicines.Get(id)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if m.Name != "Paracetamol" || m.Quantity != 20 {
		t.Errorf("unexpected medicine: %+v", m)
	}
	if m.Code == "" {
		t.Error("expected an auto-generated code, got empty string")
	}
}

func TestMedicine_AutoCodeUniqueness(t *testing.T) {
	medicines, _, _ := newTestDB(t)
	id1, _ := medicines.Add(testMedicine("Paracetamol", 10, 0))
	id2, _ := medicines.Add(testMedicine("Paracetamol", 10, 0)) // same name again

	m1, _ := medicines.Get(id1)
	m2, _ := medicines.Get(id2)
	if m1.Code == m2.Code {
		t.Errorf("expected distinct auto-generated codes for two medicines with the same name, both got %q", m1.Code)
	}
}

func TestMedicine_AdjustStock_CannotGoNegative(t *testing.T) {
	medicines, _, _ := newTestDB(t)
	id, _ := medicines.Add(testMedicine("ORS", 5, 0))

	if err := medicines.AdjustStock(id, -10); err == nil {
		t.Error("expected an error removing more stock than available, got nil")
	}

	m, _ := medicines.Get(id)
	if m.Quantity != 5 {
		t.Errorf("stock should be unchanged after a failed adjustment, got %d", m.Quantity)
	}
}

func TestMedicine_LowStockAndAvailable(t *testing.T) {
	medicines, _, _ := newTestDB(t)
	lowID, _ := medicines.Add(testMedicine("LowStock", 2, 0)) // reorder level 5, so qty 2 is low
	medicines.Add(testMedicine("PlentyStock", 100, 0))
	outID, _ := medicines.Add(testMedicine("OutOfStock", 0, 0)) // 0 is also <= reorder level - correctly "low"

	low, err := medicines.LowStock()
	if err != nil {
		t.Fatalf("LowStock failed: %v", err)
	}
	gotIDs := map[int64]bool{}
	for _, m := range low {
		gotIDs[m.ID] = true
	}
	if len(low) != 2 || !gotIDs[lowID] || !gotIDs[outID] {
		t.Errorf("expected LowStock and OutOfStock medicines (0 qty also counts as low), got %+v", low)
	}

	available, err := medicines.Available("")
	if err != nil {
		t.Fatalf("Available failed: %v", err)
	}
	for _, m := range available {
		if m.Name == "OutOfStock" {
			t.Error("Available() should exclude zero-stock medicines")
		}
	}
}

func TestOrder_Create_DeductsStockAndComputesTotal(t *testing.T) {
	medicines, _, orders := newTestDB(t)
	id, _ := medicines.Add(testMedicine("Paracetamol", 20, 0))

	order, err := orders.Create(0, []CartLine{{MedicineID: id, Quantity: 3}})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if order.Total != 15 { // price 5 * qty 3
		t.Errorf("expected total 15, got %v", order.Total)
	}

	m, _ := medicines.Get(id)
	if m.Quantity != 17 {
		t.Errorf("expected stock 17 after ordering 3 of 20, got %d", m.Quantity)
	}
}

func TestOrder_Create_RollsBackOnInsufficientStock(t *testing.T) {
	medicines, _, orders := newTestDB(t)
	id1, _ := medicines.Add(testMedicine("Paracetamol", 20, 0))
	id2, _ := medicines.Add(testMedicine("ORS", 2, 0)) // only 2 in stock

	_, err := orders.Create(0, []CartLine{
		{MedicineID: id1, Quantity: 5},
		{MedicineID: id2, Quantity: 10}, // this line should fail
	})
	if err == nil {
		t.Fatal("expected an error when one line item exceeds available stock")
	}

	// Neither line should have deducted anything - the whole order rolls back.
	m1, _ := medicines.Get(id1)
	m2, _ := medicines.Get(id2)
	if m1.Quantity != 20 {
		t.Errorf("expected medicine 1 stock untouched at 20, got %d", m1.Quantity)
	}
	if m2.Quantity != 2 {
		t.Errorf("expected medicine 2 stock untouched at 2, got %d", m2.Quantity)
	}

	orderList, _ := orders.List()
	if len(orderList) != 0 {
		t.Errorf("expected no order to have been recorded after rollback, got %d", len(orderList))
	}
}

func TestOrder_Create_AnonymousCustomerAllowed(t *testing.T) {
	medicines, _, orders := newTestDB(t)
	id, _ := medicines.Add(testMedicine("Paracetamol", 20, 0))

	// customerID 0 means "anonymous" - this must not violate the
	// orders.customer_id foreign key (a real regression this project hit).
	order, err := orders.Create(0, []CartLine{{MedicineID: id, Quantity: 1}})
	if err != nil {
		t.Fatalf("anonymous order should succeed, got error: %v", err)
	}
	if order.ID == 0 {
		t.Error("expected a valid order ID")
	}
}

func TestCustomer_FindOrCreateByPhone_Idempotent(t *testing.T) {
	_, customers, _ := newTestDB(t)

	c1, err := customers.FindOrCreateByPhone("Alice", "+911111111111")
	if err != nil {
		t.Fatalf("first FindOrCreateByPhone failed: %v", err)
	}
	c2, err := customers.FindOrCreateByPhone("Alice Again", "+911111111111")
	if err != nil {
		t.Fatalf("second FindOrCreateByPhone failed: %v", err)
	}
	if c1.ID != c2.ID {
		t.Errorf("expected the same customer record for the same phone number, got IDs %d and %d", c1.ID, c2.ID)
	}
}
