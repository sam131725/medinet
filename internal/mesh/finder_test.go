package mesh

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakePeerServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func addrOf(server *httptest.Server) string {
	return strings.TrimPrefix(server.URL, "http://")
}

func TestFindAcrossPeers_FindsMatchingStock(t *testing.T) {
	serverA := fakePeerServer(t, `[{"ID":1,"Name":"Paracetamol","Code":"PARA","Price":5,"Quantity":40}]`)
	serverB := fakePeerServer(t, `[{"ID":1,"Name":"ORS Sachet","Code":"ORS1","Price":10,"Quantity":0}]`) // has it, but zero stock

	peers := []Peer{
		{ID: "a", Name: "Camp A", HTTPAddr: addrOf(serverA)},
		{ID: "b", Name: "Camp B", HTTPAddr: addrOf(serverB)},
	}

	results := FindAcrossPeers(peers, "para")
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 result for PARA, got %d: %+v", len(results), results)
	}
	if results[0].Peer.Name != "Camp A" || results[0].Quantity != 40 {
		t.Errorf("unexpected result: %+v", results[0])
	}
}

func TestFindAcrossPeers_ExcludesZeroStock(t *testing.T) {
	serverA := fakePeerServer(t, `[{"ID":1,"Name":"ORS Sachet","Code":"ORS1","Price":10,"Quantity":0}]`)
	peers := []Peer{{ID: "a", Name: "Camp A", HTTPAddr: addrOf(serverA)}}

	results := FindAcrossPeers(peers, "ORS1")
	if len(results) != 0 {
		t.Errorf("a peer reporting 0 stock should not be returned as having it, got %+v", results)
	}
}

func TestFindAcrossPeers_UnreachablePeerIgnoredNotFatal(t *testing.T) {
	peers := []Peer{{ID: "x", Name: "Unreachable", HTTPAddr: "127.0.0.1:1"}} // nothing listens here
	results := FindAcrossPeers(peers, "PARA")
	if len(results) != 0 {
		t.Errorf("expected no results and no panic from an unreachable peer, got %+v", results)
	}
}

func TestFindAcrossPeers_CaseInsensitiveCodeMatch(t *testing.T) {
	server := fakePeerServer(t, `[{"ID":1,"Name":"Paracetamol","Code":"PARA","Price":5,"Quantity":10}]`)
	peers := []Peer{{ID: "a", Name: "Camp A", HTTPAddr: addrOf(server)}}

	results := FindAcrossPeers(peers, "para") // lowercase query against uppercase stored code
	if len(results) != 1 {
		t.Errorf("expected case-insensitive code match to find 1 result, got %d", len(results))
	}
}
