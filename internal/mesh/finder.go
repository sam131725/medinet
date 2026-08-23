package mesh

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// medicineSummary mirrors the JSON shape returned by a peer's existing
// GET /api/medicines endpoint - the same endpoint its own customer kiosk
// page already uses, so no new API needs to exist on the peer side for
// this to work.
type medicineSummary struct {
	ID       int64   `json:"ID"`
	Name     string  `json:"Name"`
	Code     string  `json:"Code"`
	Price    float64 `json:"Price"`
	Quantity int     `json:"Quantity"`
}

// StockElsewhere is what one peer reports having for a requested medicine
// code.
type StockElsewhere struct {
	Peer     Peer    `json:"peer"`
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	Quantity int     `json:"quantity"`
}

// FindAcrossPeers asks every known peer (concurrently) whether they have
// the given medicine code in stock, and returns the ones that do. This is
// the actual point of mesh discovery for this app: if a customer's local
// kiosk is out of something, staff can immediately see the nearest kiosk
// that still has it, over the same offline local network - no internet,
// no phone calls needed.
func FindAcrossPeers(peers []Peer, code string) []StockElsewhere {
	code = strings.ToUpper(strings.TrimSpace(code))
	client := &http.Client{Timeout: 3 * time.Second}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []StockElsewhere

	for _, peer := range peers {
		wg.Add(1)
		go func(p Peer) {
			defer wg.Done()
			items, err := fetchPeerMedicines(client, p.HTTPAddr)
			if err != nil {
				return
			}
			for _, m := range items {
				if strings.EqualFold(m.Code, code) && m.Quantity > 0 {
					mu.Lock()
					results = append(results, StockElsewhere{Peer: p, Name: m.Name, Price: m.Price, Quantity: m.Quantity})
					mu.Unlock()
				}
			}
		}(peer)
	}
	wg.Wait()
	return results
}

func fetchPeerMedicines(client *http.Client, httpAddr string) ([]medicineSummary, error) {
	resp, err := client.Get("http://" + httpAddr + "/api/medicines")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var items []medicineSummary
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}
