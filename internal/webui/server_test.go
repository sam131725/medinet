package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"medistock/internal/alerts"
	"medistock/internal/db"
	"medistock/internal/mesh"
	"medistock/internal/models"
	"medistock/internal/repo"
	"medistock/internal/sms"
)

func testMedicineFor(t *testing.T, name, code string, qty int) models.Medicine {
	t.Helper()
	return models.Medicine{
		Name: name, Code: code, Manufacturer: "Test Co", Batch: "B1", ExpiryDate: "2030-01-01",
		Price: 5, Quantity: qty, ReorderLevel: 5,
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	medicines := repo.NewMedicineRepo(sqlDB)
	customers := repo.NewCustomerRepo(sqlDB)
	orders := repo.NewOrderRepo(sqlDB)
	modem, _ := sms.Open("", 0)
	notifier := alerts.New(medicines, modem, "")

	return New(medicines, customers, orders, notifier, "1234")
}

func TestHealthz(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestStaffAuth_WrongPINRejected(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/staff/medicines?pin=wrong", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong PIN, got %d", rec.Code)
	}
}

func TestStaffAuth_CorrectPINAccepted(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/staff/medicines?pin=1234", nil)
	req.RemoteAddr = "10.0.0.2:12345"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for correct PIN, got %d", rec.Code)
	}
}

func TestStaffAuth_LocksOutAfterRepeatedFailures(t *testing.T) {
	s := newTestServer(t)
	const ip = "10.0.0.3:12345"

	// 5 wrong attempts trips the lockout (see New()'s newLoginLimiter(5, ...)).
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/staff/medicines?pin=wrong", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, rec.Code)
		}
	}

	// The 6th attempt, even with the CORRECT PIN, should now be locked out.
	req := httptest.NewRequest(http.MethodGet, "/api/staff/medicines?pin=1234", nil)
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 (locked out) even with correct PIN after 5 failures, got %d", rec.Code)
	}

	// A different IP is unaffected by another IP's lockout.
	req2 := httptest.NewRequest(http.MethodGet, "/api/staff/medicines?pin=1234", nil)
	req2.RemoteAddr = "10.0.0.4:12345"
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("a different IP should not be affected by another IP's lockout, got %d", rec2.Code)
	}
}

// TestMesh_FindAcrossPeers_EndToEnd mirrors the manual two-kiosk
// verification done during development: two full Server instances, each
// with its own database and its own real HTTP handler stack, wired to
// each other as static mesh peers (the reliable path - see the comment on
// TestDiscovery_TwoNodesFindEachOther in internal/mesh for why broadcast
// itself isn't asserted on here). Kiosk A is out of Paracetamol; kiosk B
// has it. Kiosk A's mesh/find endpoint should report kiosk B has it.
func TestMesh_FindAcrossPeers_EndToEnd(t *testing.T) {
	serverA := newTestServer(t)
	serverB := newTestServer(t)

	// A is out of stock; B has 40.
	idA, _ := serverA.medicines.Add(testMedicineFor(t, "Paracetamol", "PARA", 0))
	_ = idA
	serverB.medicines.Add(testMedicineFor(t, "Paracetamol", "PARA", 40))

	httpA := httptest.NewServer(serverA.Handler())
	t.Cleanup(httpA.Close)
	httpB := httptest.NewServer(serverB.Handler())
	t.Cleanup(httpB.Close)

	discA, err := mesh.New("Camp-A", stripScheme(httpA.URL), 19393, nil)
	if err != nil {
		t.Fatalf("mesh.New for A failed: %v", err)
	}
	serverA.SetDiscovery(discA)
	discA.AddStaticPeer("B", "Camp-B", stripScheme(httpB.URL))

	resp, err := http.Get(httpA.URL + "/api/staff/mesh/find?code=PARA&pin=1234")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var results []mesh.StockElsewhere
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(results) != 1 || results[0].Peer.Name != "Camp-B" || results[0].Quantity != 40 {
		t.Errorf("expected to find Camp-B has 40 Paracetamol in stock, got %+v", results)
	}
}

func stripScheme(url string) string {
	const prefix = "http://"
	if len(url) > len(prefix) && url[:len(prefix)] == prefix {
		return url[len(prefix):]
	}
	return url
}
