package mesh

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"
)

// TestDiagnostics_RecordsSentAndReceivedPackets exercises the actual wire
// traffic, not just the in-memory peer map: it starts a real Discovery
// listening on a UDP socket, sends it a genuine beacon packet plus a
// garbage (non-beacon) packet over real unicast UDP (the same reliable
// path used elsewhere in this package - see the comment on
// TestDiscovery_TwoNodesFindEachOther for why broadcast itself isn't
// asserted on here), and confirms Diagnostics() reports both: what was
// received, its raw bytes, and why it was or wasn't treated as a peer.
func TestDiagnostics_RecordsSentAndReceivedPackets(t *testing.T) {
	const udpPort = 19494 // fixed test port, unlikely to collide

	d, err := New("Camp-Diag", "10.0.0.9:8080", udpPort, nil)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	time.Sleep(150 * time.Millisecond) // let the listener bind

	sender, err := net.Dial("udp4", "127.0.0.1:"+strconv.Itoa(udpPort))
	if err != nil {
		t.Fatalf("dial test sender: %v", err)
	}
	defer sender.Close()

	// A real beacon from a "peer".
	peerBeacon, _ := json.Marshal(beacon{ID: "peer-99", Name: "Camp-B", HTTPAddr: "10.0.0.2:8080"})
	if _, err := sender.Write(peerBeacon); err != nil {
		t.Fatalf("send beacon: %v", err)
	}

	// Garbage that isn't a MediStock beacon at all.
	if _, err := sender.Write([]byte("not json at all")); err != nil {
		t.Fatalf("send garbage: %v", err)
	}

	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	var diag Diagnostics
	for {
		select {
		case <-tick.C:
			diag = d.Diagnostics()
			inbound := 0
			for _, e := range diag.Events {
				if e.Direction == "in" {
					inbound++
				}
			}
			if inbound >= 2 {
				goto checked
			}
		case <-deadline:
			t.Fatalf("timed out waiting for diagnostics to record both packets; got %+v", diag.Events)
		}
	}

checked:
	if diag.NodeID != d.NodeID() {
		t.Errorf("expected diagnostics NodeID %q, got %q", d.NodeID(), diag.NodeID)
	}
	if diag.UDPPort != udpPort {
		t.Errorf("expected UDPPort %d, got %d", udpPort, diag.UDPPort)
	}

	var sawParsedBeacon, sawGarbage bool
	for _, e := range diag.Events {
		if e.Direction != "in" {
			continue
		}
		if e.Parsed && e.PeerID == "peer-99" {
			sawParsedBeacon = true
			if e.Note != "new peer discovered" {
				t.Errorf("expected note %q for the first beacon from a new peer, got %q", "new peer discovered", e.Note)
			}
			if e.HexPreview == "" {
				t.Errorf("expected a non-empty hex preview for the received beacon")
			}
		}
		if !e.Parsed && e.Bytes == len("not json at all") {
			sawGarbage = true
			if e.Note == "" {
				t.Errorf("expected a note explaining the unparseable packet, got empty")
			}
		}
	}
	if !sawParsedBeacon {
		t.Errorf("expected a recorded event for the real beacon, got %+v", diag.Events)
	}
	if !sawGarbage {
		t.Errorf("expected a recorded event for the non-beacon garbage packet, got %+v", diag.Events)
	}

	// The outbound side: this node's own periodic self-broadcast should
	// show up too (sent within the first 5s of Run starting).
	sawOutbound := false
	deadline2 := time.After(3 * time.Second)
	for !sawOutbound {
		diag = d.Diagnostics()
		for _, e := range diag.Events {
			if e.Direction == "out" {
				sawOutbound = true
				break
			}
		}
		if sawOutbound {
			break
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-deadline2:
			t.Fatalf("timed out waiting for an outbound beacon event; got %+v", diag.Events)
		}
	}
}

// TestDiagnostics_ReportsListenError confirms a bound-port failure surfaces
// through Diagnostics() rather than only through logs - so the staff page
// can explain "mesh isn't working" concretely instead of just going quiet.
func TestDiagnostics_ReportsListenError(t *testing.T) {
	const udpPort = 19495

	blocker, err := net.ListenPacket("udp4", ":"+strconv.Itoa(udpPort))
	if err != nil {
		t.Skipf("could not reserve test port to block: %v", err)
	}
	defer blocker.Close()

	d, err := New("Camp-Blocked", "10.0.0.9:8080", udpPort, nil)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.Run(ctx) // returns quickly - can't bind, so Run exits after logging/recording the error

	diag := d.Diagnostics()
	if diag.ListenError == "" {
		t.Errorf("expected a non-empty ListenError when the UDP port is already in use, got empty")
	}
}
