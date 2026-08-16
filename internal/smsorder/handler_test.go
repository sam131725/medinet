package smsorder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"medistock/internal/alerts"
	"medistock/internal/db"
	"medistock/internal/models"
	"medistock/internal/repo"
	"medistock/internal/sms"
)

func testMedicine(name string, price float64, qty, maxPerOrder int) models.Medicine {
	return models.Medicine{
		Name: name, Manufacturer: "Test Co", Batch: "B1", ExpiryDate: "2030-01-01",
		Price: price, Quantity: qty, ReorderLevel: 5, MaxPerOrder: maxPerOrder,
	}
}

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close(); os.Remove(dbPath) })

	medicines := repo.NewMedicineRepo(sqlDB)
	customers := repo.NewCustomerRepo(sqlDB)
	orders := repo.NewOrderRepo(sqlDB)
	modem, _ := sms.Open("", 0) // dry-run modem, no hardware needed
	notifier := alerts.New(medicines, modem, "")

	return New(medicines, customers, orders, notifier)
}

func TestHandleMessage_HelpAndUnknown(t *testing.T) {
	h := newTestHandler(t)

	if reply := h.HandleMessage("+1000", "HELP"); !strings.Contains(reply, "MediStock SMS ordering") {
		t.Errorf("HELP reply missing instructions: %q", reply)
	}
	if reply := h.HandleMessage("+1000", "blah blah"); !strings.Contains(reply, "didn't understand") {
		t.Errorf("unknown command should say it didn't understand: %q", reply)
	}
}

func TestHandleMessage_ListShowsCodes(t *testing.T) {
	h := newTestHandler(t)
	id, err := h.medicines.Add(testMedicine("Paracetamol 500mg", 5, 20, 0))
	if err != nil {
		t.Fatalf("add medicine: %v", err)
	}
	m, _ := h.medicines.Get(id)

	reply := h.HandleMessage("+1000", "LIST")
	if !strings.Contains(reply, m.Code) || !strings.Contains(reply, "Paracetamol") {
		t.Errorf("LIST reply should include code and name: %q", reply)
	}
}

func TestHandleMessage_OrderSuccess(t *testing.T) {
	h := newTestHandler(t)
	id, _ := h.medicines.Add(testMedicine("ORS Sachet", 10, 50, 0))
	m, _ := h.medicines.Get(id)

	reply := h.HandleMessage("+919999999999", "ORDER "+m.Code+" 3")
	if !strings.Contains(reply, "Order confirmed") || !strings.Contains(reply, "Token #1") {
		t.Errorf("expected order confirmation with token, got: %q", reply)
	}

	after, err := h.medicines.Get(id)
	if err != nil {
		t.Fatalf("get medicine: %v", err)
	}
	if after.Quantity != 47 {
		t.Errorf("expected stock 47 after ordering 3 of 50, got %d", after.Quantity)
	}
}

func TestHandleMessage_OrderRespectsMaxPerOrderCap(t *testing.T) {
	h := newTestHandler(t)
	id, _ := h.medicines.Add(testMedicine("ORS Sachet", 10, 50, 3)) // max 3 per order
	m, _ := h.medicines.Get(id)

	reply := h.HandleMessage("+919999999999", "ORDER "+m.Code+" 10")
	if !strings.Contains(reply, "capped from 10") {
		t.Errorf("expected cap notice in reply, got: %q", reply)
	}

	after, _ := h.medicines.Get(id)
	if after.Quantity != 47 { // 50 - 3, not 50 - 10
		t.Errorf("expected only 3 deducted due to cap, stock = %d", after.Quantity)
	}
}

func TestHandleMessage_UnknownCode(t *testing.T) {
	h := newTestHandler(t)
	reply := h.HandleMessage("+1000", "ORDER NOPE 2")
	if !strings.Contains(reply, "unknown code") {
		t.Errorf("expected unknown code error, got: %q", reply)
	}
}

func TestHandleMessage_MultiItemOrder(t *testing.T) {
	h := newTestHandler(t)
	id1, _ := h.medicines.Add(testMedicine("Paracetamol", 5, 20, 0))
	id2, _ := h.medicines.Add(testMedicine("ORS Sachet", 10, 20, 0))
	m1, _ := h.medicines.Get(id1)
	m2, _ := h.medicines.Get(id2)

	reply := h.HandleMessage("+1000", "ORDER "+m1.Code+" 2, "+m2.Code+" 1")
	if !strings.Contains(reply, "Order confirmed") {
		t.Fatalf("expected success, got: %q", reply)
	}
	after1, _ := h.medicines.Get(id1)
	after2, _ := h.medicines.Get(id2)
	if after1.Quantity != 18 || after2.Quantity != 19 {
		t.Errorf("unexpected stock after multi-item order: m1=%d m2=%d", after1.Quantity, after2.Quantity)
	}
}
