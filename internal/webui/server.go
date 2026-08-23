// Package webui serves a touch-friendly, fully offline local web interface
// for MediStock: a customer self-checkout kiosk page and a staff inventory
// page. It talks to the same SQLite-backed repositories as the terminal CLI
// - no network access beyond the local machine/hotspot is ever required.
package webui

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"medistock/internal/alerts"
	"medistock/internal/applog"
	"medistock/internal/mesh"
	"medistock/internal/models"
	"medistock/internal/repo"
)

type Server struct {
	medicines *repo.MedicineRepo
	customers *repo.CustomerRepo
	orders    *repo.OrderRepo
	notifier  *alerts.Notifier
	staffPIN  string
	limiter   *loginLimiter
	startedAt time.Time
	discovery *mesh.Discovery // optional - nil if mesh networking with other kiosks isn't enabled
}

// SetDiscovery attaches mesh peer discovery to this server, enabling the
// /api/mesh/* endpoints and the staff "Nearby Kiosks" panel. Optional -
// without it, mesh endpoints report the feature as disabled.
func (s *Server) SetDiscovery(d *mesh.Discovery) { s.discovery = d }

func New(medicines *repo.MedicineRepo, customers *repo.CustomerRepo, orders *repo.OrderRepo, notifier *alerts.Notifier, staffPIN string) *Server {
	return &Server{
		medicines: medicines, customers: customers, orders: orders, notifier: notifier, staffPIN: staffPIN,
		limiter:   newLoginLimiter(5, 5*time.Minute), // 5 failed PINs -> 5 minute lockout for that IP
		startedAt: time.Now(),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Pages
	mux.HandleFunc("/", s.handleCustomerPage)
	mux.HandleFunc("/staff", s.handleStaffPage)

	// Operational
	mux.HandleFunc("/healthz", s.handleHealthz)

	// Customer-facing API
	mux.HandleFunc("/api/medicines", s.handleAvailableMedicines) // GET, only in-stock items
	mux.HandleFunc("/api/checkout", s.handleCheckout)            // POST
	mux.HandleFunc("/api/order", s.handleGetOrder)               // GET ?id=

	// Staff API (PIN-protected, rate-limited)
	mux.HandleFunc("/api/staff/medicines", s.staffAuth(s.handleStaffMedicines)) // GET (all) / POST (add)
	mux.HandleFunc("/api/staff/stock", s.staffAuth(s.handleStaffAdjustStock))   // POST
	mux.HandleFunc("/api/staff/orders", s.staffAuth(s.handleStaffOrders))       // GET

	// Mesh: other MediStock kiosks discovered on the local network
	mux.HandleFunc("/api/staff/mesh/peers", s.staffAuth(s.handleMeshPeers)) // GET
	mux.HandleFunc("/api/staff/mesh/find", s.staffAuth(s.handleMeshFind))   // GET ?code=

	return withRequestLogging(mux)
}

// withRequestLogging wraps a handler so every request is logged with its
// method, path, remote address, status code, and duration - the minimum
// needed to investigate "what happened" after the fact on an unattended
// kiosk, instead of having no record at all.
func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		applog.L.Info("http request",
			"method", r.Method, "path", r.URL.Path, "remote", clientIP(r),
			"status", sw.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "ok",
		"uptimeSecond": int(time.Since(s.startedAt).Seconds()),
	})
}

// Run starts the HTTP server on the given port, printing every local
// network address it can be reached on (so staff can share the right URL
// with a phone/tablet connected to the same offline WiFi hotspot). It
// blocks until ctx is cancelled, then shuts down gracefully (in-flight
// requests get up to 10s to finish) before returning.
func (s *Server) Run(ctx context.Context, port int) error {
	addr := fmt.Sprintf(":%d", port)
	fmt.Println("=================================================")
	fmt.Println(" MediStock web kiosk starting")
	fmt.Println(" (fully local - no internet needed or used)")
	fmt.Println("=================================================")
	fmt.Printf("Customer kiosk: http://localhost:%d/\n", port)
	fmt.Printf("Staff page:     http://localhost:%d/staff  (PIN required)\n", port)
	for _, ip := range localIPv4s() {
		fmt.Printf("On this network try: http://%s:%d/\n", ip, port)
	}
	fmt.Println("Share that network address with devices on the same offline WiFi/hotspot.")
	fmt.Println("Press Ctrl+C to stop.")

	httpServer := &http.Server{Addr: addr, Handler: s.Handler()}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		applog.L.Info("shutting down web server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

func localIPv4s() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// staffAuth requires an "X-Staff-Pin" header (or "pin" query param) matching
// the configured staff PIN before allowing access to inventory-mutating
// endpoints, and rate-limits repeated failures per IP. This is a
// lightweight deterrent, not real authentication - it exists so a member
// of the public at the customer kiosk can't casually browse to /staff and
// edit stock, and can't brute-force a short PIN in a tight loop.
func (s *Server) staffAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !s.limiter.allowed(ip) {
			applog.L.Warn("staff auth locked out", "remote", ip)
			writeError(w, http.StatusTooManyRequests, "too many failed PIN attempts - try again in a few minutes")
			return
		}

		pin := r.Header.Get("X-Staff-Pin")
		if pin == "" {
			pin = r.URL.Query().Get("pin")
		}
		if subtle.ConstantTimeCompare([]byte(pin), []byte(s.staffPIN)) != 1 {
			s.limiter.recordFailure(ip)
			applog.L.Warn("staff auth failed", "remote", ip)
			writeError(w, http.StatusUnauthorized, "invalid or missing staff PIN")
			return
		}
		s.limiter.recordSuccess(ip)
		next(w, r)
	}
}

// ---- customer API ----

func (s *Server) handleAvailableMedicines(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	q := r.URL.Query().Get("q")
	items, err := s.medicines.Available(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type checkoutRequest struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
	Items []struct {
		MedicineID int64 `json:"medicineId"`
		Quantity   int   `json:"quantity"`
	} `json:"items"`
}

func (s *Server) handleCheckout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req checkoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "cart is empty")
		return
	}

	var customerID int64
	if req.Name != "" {
		c, err := s.customers.FindOrCreateByPhone(req.Name, req.Phone)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not save customer details: "+err.Error())
			return
		}
		customerID = c.ID
	}

	// Enforce the same emergency per-order caps the kiosk/staff CLI applies,
	// so the API can't be used to bypass hoarding limits.
	var lines []repo.CartLine
	for _, item := range req.Items {
		if item.Quantity <= 0 {
			continue
		}
		m, err := s.medicines.Get(item.MedicineID)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("medicine %d not found", item.MedicineID))
			return
		}
		qty := item.Quantity
		if m.MaxPerOrder > 0 && qty > m.MaxPerOrder {
			qty = m.MaxPerOrder
		}
		lines = append(lines, repo.CartLine{MedicineID: item.MedicineID, Quantity: qty})
	}

	order, err := s.orders.Create(customerID, lines)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	for _, it := range order.Items {
		s.notifier.Check(it.MedicineID)
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order id")
		return
	}
	order, err := s.orders.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}
	writeJSON(w, http.StatusOK, order)
}

// ---- staff API ----

func (s *Server) handleStaffMedicines(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.medicines.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var m models.Medicine
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if m.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		id, err := s.medicines.Add(m)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		m.ID = id
		s.notifier.Check(id)
		writeJSON(w, http.StatusOK, m)
	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

type stockAdjustRequest struct {
	MedicineID int64 `json:"medicineId"`
	Delta      int   `json:"delta"`
}

func (s *Server) handleStaffAdjustStock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req stockAdjustRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.medicines.AdjustStock(req.MedicineID, req.Delta); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.notifier.Check(req.MedicineID)
	m, err := s.medicines.Get(req.MedicineID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// ---- mesh (nearby kiosks) API ----

func (s *Server) handleMeshPeers(w http.ResponseWriter, r *http.Request) {
	if s.discovery == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": false, "peers": []mesh.Peer{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled": true,
		"nodeId":  s.discovery.NodeID(),
		"peers":   s.discovery.Peers(),
	})
}

func (s *Server) handleMeshFind(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}
	if s.discovery == nil {
		writeJSON(w, http.StatusOK, []mesh.StockElsewhere{})
		return
	}
	results := mesh.FindAcrossPeers(s.discovery.Peers(), code)
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleStaffOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	orders, err := s.orders.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

// ---- pages ----

func (s *Server) handleCustomerPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(customerPageHTML))
}

func (s *Server) handleStaffPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(staffPageHTML))
}
