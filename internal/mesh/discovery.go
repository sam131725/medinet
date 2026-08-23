// Package mesh implements zero-configuration peer discovery between
// MediStock kiosks on the same local network - the same networking idea
// behind how a printer or Chromecast shows up automatically on WiFi
// (mDNS/Bonjour-style), and the building block enterprise disaster-response
// networking gear (the kind Cisco builds for emergency deployments) relies
// on: nodes announce themselves over UDP broadcast, everyone else on the
// LAN builds a live peer list with no central server and no manual IP
// entry. Still fully offline - broadcast never leaves the local subnet.
package mesh

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Peer is one other MediStock kiosk discovered on the local network.
type Peer struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	HTTPAddr string    `json:"httpAddr"` // e.g. "192.168.1.7:8080" - reach its /api/medicines etc. here
	LastSeen time.Time `json:"lastSeen"`
}

// beacon is the small UDP packet each node broadcasts announcing itself.
type beacon struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	HTTPAddr string `json:"httpAddr"`
}

// staleAfter is how long a peer is kept in the list without hearing from
// it again before it's considered gone (e.g. that kiosk lost power/left
// the network) - mirrors how real mesh/discovery protocols age out peers.
const staleAfter = 30 * time.Second

// maxDiagEvents caps how many recent packet events Discovery remembers for
// the diagnostics view - enough to see what's happening right now without
// growing unbounded on a kiosk that runs for weeks.
const maxDiagEvents = 50

// DiagEvent is one UDP packet MediStock's mesh discovery sent or received,
// kept around for the staff "Network diagnostics" panel - the point is to
// let someone standing at the kiosk (or looking at a support screenshot)
// actually see what's happening at the packet level, instead of just "it's
// not working" with no visibility into why. This deliberately only inspects
// MediStock's own discovery socket (no raw packet capture, no extra OS
// privileges needed) - broad enough to answer "is this kiosk sending/
// receiving anything at all on the mesh port," which is the actual
// question when broadcast discovery seems stuck.
type DiagEvent struct {
	Time       time.Time `json:"time"`
	Direction  string    `json:"direction"` // "out" (we sent) or "in" (we received)
	RemoteAddr string    `json:"remoteAddr"`
	Bytes      int       `json:"bytes"`
	HexPreview string    `json:"hexPreview"` // first bytes of the raw payload, hex-encoded
	Parsed     bool      `json:"parsed"`
	PeerID     string    `json:"peerId,omitempty"`
	PeerName   string    `json:"peerName,omitempty"`
	Note       string    `json:"note"`
}

// Diagnostics is the full diagnostic snapshot returned to the staff page:
// this node's own identity/config plus its recent packet history, so
// someone debugging "why can't kiosk A see kiosk B" has everything needed
// in one place rather than needing shell/log access to the machine.
type Diagnostics struct {
	NodeID       string      `json:"nodeId"`
	Name         string      `json:"name"`
	HTTPAddr     string      `json:"httpAddr"`
	UDPPort      int         `json:"udpPort"`
	BroadcastDst string      `json:"broadcastDst"`
	ListenError  string      `json:"listenError,omitempty"`
	Events       []DiagEvent `json:"events"` // most recent first
}

// Discovery broadcasts this node's presence and tracks peers announcing
// themselves the same way.
type Discovery struct {
	nodeID   string
	name     string
	httpAddr string
	udpPort  int
	log      *slog.Logger

	mu         sync.Mutex
	peers      map[string]Peer
	diagEvents []DiagEvent
	listenErr  string
}

// New creates a Discovery instance. name is a human-readable label for this
// kiosk (e.g. "Camp-3 Pharmacy"); httpAddr is where peers can reach this
// node's HTTP API (host:port on the local network); udpPort is the shared
// broadcast port every MediStock node listens/broadcasts on.
func New(name, httpAddr string, udpPort int, log *slog.Logger) (*Discovery, error) {
	id, err := randomID()
	if err != nil {
		return nil, fmt.Errorf("generate node id: %w", err)
	}
	if name == "" {
		name = "kiosk-" + id
	}
	return &Discovery{
		nodeID: id, name: name, httpAddr: httpAddr, udpPort: udpPort,
		log: log, peers: make(map[string]Peer),
	}, nil
}

// LocalIPv4 returns the first non-loopback IPv4 address found on this
// machine's network interfaces - used to build the address this node
// advertises to peers (a peer obviously can't reach us at "localhost").
// Returns "" if none is found (e.g. genuinely offline with no LAN/WiFi at
// all - mesh discovery can't do anything useful in that case anyway).
func LocalIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
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
			return ip.String()
		}
	}
	return ""
}

func randomID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// NodeID returns this node's randomly generated identifier.
func (d *Discovery) NodeID() string { return d.nodeID }

// AddStaticPeer registers a peer by its known HTTP address directly,
// bypassing broadcast discovery entirely. Some networks - locked-down
// enterprise WiFi, or certain cloud/virtualized network setups - block UDP
// broadcast/multicast between clients as a security measure, so relying on
// auto-discovery alone isn't always realistic. A static peer, once added,
// is refreshed as "seen" on every AddStaticPeer call - call this
// periodically (e.g. from a config reload) to keep it from going stale.
func (d *Discovery) AddStaticPeer(id, name, httpAddr string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.peers[id] = Peer{ID: id, Name: name, HTTPAddr: httpAddr, LastSeen: time.Now()}
}

// Peers returns a snapshot of currently known, non-stale peers.
func (d *Discovery) Peers() []Peer {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Peer, 0, len(d.peers))
	cutoff := time.Now().Add(-staleAfter)
	for _, p := range d.peers {
		if p.LastSeen.After(cutoff) {
			out = append(out, p)
		}
	}
	return out
}

// recordEvent appends a diagnostic event, trimming the oldest once the
// ring buffer is full.
func (d *Discovery) recordEvent(e DiagEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.diagEvents = append(d.diagEvents, e)
	if len(d.diagEvents) > maxDiagEvents {
		d.diagEvents = d.diagEvents[len(d.diagEvents)-maxDiagEvents:]
	}
}

func hexPreview(b []byte) string {
	const max = 32
	if len(b) <= max {
		return hex.EncodeToString(b)
	}
	return hex.EncodeToString(b[:max]) + "..."
}

// Diagnostics returns this node's identity/config plus its recent
// send/receive packet history, most recent first - see DiagEvent.
func (d *Discovery) Diagnostics() Diagnostics {
	d.mu.Lock()
	defer d.mu.Unlock()
	events := make([]DiagEvent, len(d.diagEvents))
	copy(events, d.diagEvents)
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return Diagnostics{
		NodeID:       d.nodeID,
		Name:         d.name,
		HTTPAddr:     d.httpAddr,
		UDPPort:      d.udpPort,
		BroadcastDst: fmt.Sprintf("%s:%d", net.IPv4bcast.String(), d.udpPort),
		ListenError:  d.listenErr,
		Events:       events,
	}
}

// Run broadcasts this node's beacon every 5 seconds and listens for other
// nodes' beacons, until ctx is cancelled. Intended to run in its own
// goroutine for the process lifetime.
func (d *Discovery) Run(ctx context.Context) {
	conn, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", d.udpPort))
	if err != nil {
		d.mu.Lock()
		d.listenErr = err.Error()
		d.mu.Unlock()
		if d.log != nil {
			d.log.Warn("mesh discovery disabled: could not listen for peer beacons", "error", err)
		}
		return
	}
	defer conn.Close()

	go d.listenLoop(ctx, conn)
	d.broadcastLoop(ctx, conn)
}

func (d *Discovery) broadcastLoop(ctx context.Context, conn net.PacketConn) {
	payload, _ := json.Marshal(beacon{ID: d.nodeID, Name: d.name, HTTPAddr: d.httpAddr})
	dst := &net.UDPAddr{IP: net.IPv4bcast, Port: d.udpPort}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	// Send one immediately so peers don't wait a full interval to see us.
	d.sendBeacon(conn, dst, payload)
	for {
		select {
		case <-ticker.C:
			d.sendBeacon(conn, dst, payload)
		case <-ctx.Done():
			return
		}
	}
}

func (d *Discovery) sendBeacon(conn net.PacketConn, dst net.Addr, payload []byte) {
	_, err := conn.WriteTo(payload, dst)
	note := "beacon broadcast"
	if err != nil {
		note = "broadcast failed: " + err.Error()
		if d.log != nil {
			d.log.Debug("mesh: failed to send beacon", "error", err)
		}
	}
	d.recordEvent(DiagEvent{
		Time: time.Now(), Direction: "out", RemoteAddr: dst.String(),
		Bytes: len(payload), HexPreview: hexPreview(payload), Parsed: err == nil,
		PeerID: d.nodeID, PeerName: d.name, Note: note,
	})
}

func (d *Discovery) listenLoop(ctx context.Context, conn net.PacketConn) {
	buf := make([]byte, 1024)
	go func() {
		<-ctx.Done()
		conn.Close() // unblocks the ReadFrom below
	}()

	for {
		n, remote, err := conn.ReadFrom(buf)
		if err != nil {
			return // context cancelled / connection closed
		}
		raw := append([]byte(nil), buf[:n]...) // ReadFrom reuses buf next iteration
		remoteAddr := ""
		if remote != nil {
			remoteAddr = remote.String()
		}

		var b beacon
		if err := json.Unmarshal(raw, &b); err != nil {
			d.recordEvent(DiagEvent{
				Time: time.Now(), Direction: "in", RemoteAddr: remoteAddr,
				Bytes: n, HexPreview: hexPreview(raw), Parsed: false,
				Note: "not a MediStock beacon (unrecognized payload) - ignored",
			})
			continue // not one of ours - ignore
		}
		if b.ID == d.nodeID {
			d.recordEvent(DiagEvent{
				Time: time.Now(), Direction: "in", RemoteAddr: remoteAddr,
				Bytes: n, HexPreview: hexPreview(raw), Parsed: true,
				PeerID: b.ID, PeerName: b.Name, Note: "our own broadcast, echoed back - ignored",
			})
			continue // our own broadcast, echoed back - ignore
		}

		d.mu.Lock()
		_, known := d.peers[b.ID]
		d.peers[b.ID] = Peer{ID: b.ID, Name: b.Name, HTTPAddr: b.HTTPAddr, LastSeen: time.Now()}
		d.mu.Unlock()

		note := "known peer refreshed"
		if !known {
			note = "new peer discovered"
			if d.log != nil {
				d.log.Info("mesh: discovered new peer kiosk", "peerID", b.ID, "peerName", b.Name, "peerAddr", b.HTTPAddr)
			}
		}
		d.recordEvent(DiagEvent{
			Time: time.Now(), Direction: "in", RemoteAddr: remoteAddr,
			Bytes: n, HexPreview: hexPreview(raw), Parsed: true,
			PeerID: b.ID, PeerName: b.Name, Note: note,
		})
	}
}
