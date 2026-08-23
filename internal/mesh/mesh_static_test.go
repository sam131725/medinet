package mesh

import "testing"

// TestAddStaticPeer_MakesPeerImmediatelyVisible exercises the fallback
// path used on networks that block UDP broadcast (locked-down WiFi, some
// cloud/container networks) - the same path verified manually end-to-end
// against two real running kiosk instances during development.
func TestAddStaticPeer_MakesPeerImmediatelyVisible(t *testing.T) {
	d, err := New("Camp-A", "10.0.0.1:8080", 19292, nil)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if len(d.Peers()) != 0 {
		t.Fatalf("expected no peers before AddStaticPeer, got %v", d.Peers())
	}

	d.AddStaticPeer("10.0.0.2:8080", "Camp-B", "10.0.0.2:8080")

	peers := d.Peers()
	if len(peers) != 1 || peers[0].Name != "Camp-B" || peers[0].HTTPAddr != "10.0.0.2:8080" {
		t.Errorf("expected Camp-B to be immediately visible as a peer, got %+v", peers)
	}
}
