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

// Discovery broadcasts this node's presence and tracks peers announcing
// themselves the same way.
type Discovery struct {
	nodeID   string
	name     string
	httpAddr string
	udpPort  int
	log      *slog.Logger

	mu    sync.Mutex
	peers map[string]Peer
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

// Run broadcasts this node's beacon every 5 seconds and listens for other
// nodes' beacons, until ctx is cancelled. Intended to run in its own
// goroutine for the process lifetime.
func (d *Discovery) Run(ctx context.Context) {
	conn, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", d.udpPort))
	if err != nil {
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
	if _, err := conn.WriteTo(payload, dst); err != nil && d.log != nil {
		d.log.Debug("mesh: failed to send beacon", "error", err)
	}
}

func (d *Discovery) listenLoop(ctx context.Context, conn net.PacketConn) {
	buf := make([]byte, 1024)
	go func() {
		<-ctx.Done()
		conn.Close() // unblocks the ReadFrom below
	}()

	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return // context cancelled / connection closed
		}
		var b beacon
		if err := json.Unmarshal(buf[:n], &b); err != nil {
			continue // not one of ours - ignore
		}
		if b.ID == d.nodeID {
			continue // our own broadcast, echoed back - ignore
		}

		d.mu.Lock()
		_, known := d.peers[b.ID]
		d.peers[b.ID] = Peer{ID: b.ID, Name: b.Name, HTTPAddr: b.HTTPAddr, LastSeen: time.Now()}
		d.mu.Unlock()

		if !known && d.log != nil {
			d.log.Info("mesh: discovered new peer kiosk", "peerID", b.ID, "peerName", b.Name, "peerAddr", b.HTTPAddr)
		}
	}
}
