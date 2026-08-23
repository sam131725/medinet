// Command medistock is a fully offline medicine ordering and pharmacy
// billing system. All data is stored locally in a SQLite file next to the
// executable - no internet connection is ever required, whether run as a
// terminal menu, a local touch-friendly web kiosk, or via two-way SMS
// ordering for customers on basic phones with only cellular signal. SMS
// can go over a dedicated GSM modem, or a spare Android phone running a
// small local gateway app (see docs/phone-sms-gateway) - no special
// hardware required for the latter.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"medistock/internal/alerts"
	"medistock/internal/applog"
	"medistock/internal/backup"
	"medistock/internal/cli"
	"medistock/internal/db"
	"medistock/internal/mesh"
	"medistock/internal/repo"
	"medistock/internal/sms"
	"medistock/internal/smsorder"
	"medistock/internal/webui"
)

func main() {
	web := flag.Bool("web", false, "serve a touch-friendly local web kiosk instead of the terminal menu")
	port := flag.Int("port", 8080, "port for the web kiosk (only used with -web)")
	dbPath := flag.String("db", "medistock.db", "path to the local SQLite database file (used unless -db-driver=postgres)")
	dbDriver := flag.String("db-driver", "sqlite", "database engine: \"sqlite\" (default, single local file, no server needed) or \"postgres\" (a Postgres server running locally)")
	pgHost := flag.String("pg-host", "localhost", "Postgres host (only used with -db-driver=postgres; should be a machine on your local network, not the internet)")
	pgPort := flag.Int("pg-port", 5432, "Postgres port")
	pgUser := flag.String("pg-user", "", "Postgres username")
	pgPassword := flag.String("pg-password", "", "Postgres password")
	pgDBName := flag.String("pg-dbname", "medistock", "Postgres database name")
	pgSSLMode := flag.String("pg-sslmode", "disable", "Postgres sslmode (\"disable\" is normal for a local/offline instance with no certificate)")
	staffPIN := flag.String("staff-pin", "1234", "PIN required to access the staff page/API in web mode (change this!)")

	smsPort := flag.String("sms-port", "", "serial device for a GSM modem, e.g. /dev/ttyUSB0")
	smsBaud := flag.Int("sms-baud", 9600, "baud rate for the GSM modem serial connection")
	smsGatewayURL := flag.String("sms-gateway-url", "", "base URL of a phone-based SMS gateway instead of a modem, e.g. http://192.168.1.50:8090 (see docs/phone-sms-gateway)")
	smsGatewayToken := flag.String("sms-gateway-token", "", "optional bearer token for the phone SMS gateway")

	distributorPhone := flag.String("distributor-phone", "", "phone number to text low-stock reorder alerts to (empty = alerts disabled)")
	smsOrdering := flag.Bool("sms-ordering", false, "let customers on basic phones place orders by texting the SMS transport's number")
	smsPollInterval := flag.Duration("sms-poll-interval", 15*time.Second, "how often to check for new incoming SMS orders")

	logFile := flag.String("log-file", "", "also write structured logs to this file (in addition to stderr); empty = stderr only")
	debug := flag.Bool("debug", false, "enable debug-level logging")

	backupDir := flag.String("backup-dir", "backups", "directory to write periodic database backups into")
	backupInterval := flag.Duration("backup-interval", 1*time.Hour, "how often to back up the database; 0 disables periodic backups (an initial backup still runs at startup)")
	backupKeep := flag.Int("backup-keep", 24, "how many recent backups to retain (0 = keep all, not recommended long-term)")

	meshEnabled := flag.Bool("mesh", false, "discover other MediStock kiosks on the local network (zero-config, UDP broadcast) so staff can look up which nearby kiosk has a medicine in stock; requires -web")
	meshName := flag.String("mesh-name", "", "this kiosk's display name to other kiosks on the mesh (default: auto-generated)")
	meshUDPPort := flag.Int("mesh-udp-port", 9191, "shared UDP port all MediStock kiosks broadcast/listen on for mesh discovery")
	meshPeers := flag.String("mesh-peers", "", "comma-separated host:port list of other kiosks to always treat as peers, bypassing broadcast discovery (useful on networks that block UDP broadcast, e.g. locked-down WiFi)")
	flag.Parse()

	// Backward-compatible: `medistock somefile.db` still works as before.
	if flag.NArg() > 0 {
		*dbPath = flag.Arg(0)
	}

	if err := applog.Init(*logFile, *debug); err != nil {
		fmt.Fprintln(os.Stderr, "failed to set up logging:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sqlDB, err := db.Open(db.Config{
		Driver:   *dbDriver,
		Path:     *dbPath,
		Host:     *pgHost,
		Port:     *pgPort,
		User:     *pgUser,
		Password: *pgPassword,
		DBName:   *pgDBName,
		SSLMode:  *pgSSLMode,
	})
	if err != nil {
		applog.L.Error("failed to open database", "error", err, "driver", *dbDriver)
		os.Exit(1)
	}
	defer sqlDB.Close()

	medicineRepo := repo.NewMedicineRepo(sqlDB)
	customerRepo := repo.NewCustomerRepo(sqlDB)
	orderRepo := repo.NewOrderRepo(sqlDB)

	backupRunner := backup.New(sqlDB, *backupDir, *backupKeep, applog.L)
	if *backupInterval > 0 {
		backupStop := make(chan struct{})
		go backupRunner.Run(*backupInterval, backupStop)
		go func() { <-ctx.Done(); close(backupStop) }()
	} else if _, err := backupRunner.Once(); err != nil {
		applog.L.Warn("startup backup failed", "error", err)
	}

	transport, err := openSMSTransport(*smsGatewayURL, *smsGatewayToken, *smsPort, *smsBaud)
	if err != nil {
		applog.L.Error("failed to set up SMS transport", "error", err)
		os.Exit(1)
	}
	defer transport.Close()
	notifier := alerts.New(medicineRepo, transport, *distributorPhone)

	if *smsOrdering {
		if *smsGatewayURL == "" && *smsPort == "" {
			applog.L.Warn("sms-ordering enabled with no modem or phone gateway configured - incoming SMS polling is a no-op until -sms-port or -sms-gateway-url is set")
		}
		handler := smsorder.New(medicineRepo, customerRepo, orderRepo, notifier)
		go runSMSOrderLoop(ctx, transport, handler, *smsPollInterval)
	}

	if *web {
		server := webui.New(medicineRepo, customerRepo, orderRepo, notifier, *staffPIN)

		if *meshEnabled {
			setUpMesh(ctx, server, *meshName, *port, *meshUDPPort, *meshPeers)
		} else if *meshPeers != "" {
			applog.L.Warn("-mesh-peers was set but -mesh is not enabled; ignoring")
		}

		if err := server.Run(ctx, *port); err != nil {
			applog.L.Error("web server error", "error", err)
			os.Exit(1)
		}
		return
	}

	app := cli.NewApp(medicineRepo, customerRepo, orderRepo, notifier)
	app.Run()
}

// setUpMesh wires zero-config peer discovery (and any statically configured
// peers) into the web server, then starts the broadcast/listen loop in the
// background. Mesh networking is entirely best-effort: if it can't figure
// out this machine's LAN address, or the network blocks UDP broadcast, the
// rest of the app keeps working exactly as if -mesh had never been passed.
func setUpMesh(ctx context.Context, server *webui.Server, name string, httpPort, udpPort int, staticPeers string) {
	localIP := mesh.LocalIPv4()
	if localIP == "" {
		applog.L.Warn("mesh: could not determine a local network address; mesh discovery disabled")
		return
	}
	httpAddr := fmt.Sprintf("%s:%d", localIP, httpPort)

	discovery, err := mesh.New(name, httpAddr, udpPort, applog.L)
	if err != nil {
		applog.L.Error("mesh: failed to start", "error", err)
		return
	}
	applog.L.Info("mesh: starting peer discovery", "nodeId", discovery.NodeID(), "advertisedAddr", httpAddr)
	server.SetDiscovery(discovery)
	go discovery.Run(ctx)

	if staticPeers != "" {
		go refreshStaticPeers(ctx, discovery, staticPeers)
	}
}

// refreshStaticPeers re-registers every address in a comma-separated
// host:port list every 10s, since Discovery ages out any peer not heard
// from recently - a static peer needs the same periodic "still here"
// refresh a broadcast peer gets automatically.
func refreshStaticPeers(ctx context.Context, discovery *mesh.Discovery, peerList string) {
	addrs := strings.Split(peerList, ",")
	add := func() {
		for _, addr := range addrs {
			addr = strings.TrimSpace(addr)
			if addr == "" {
				continue
			}
			discovery.AddStaticPeer(addr, addr, addr)
		}
	}

	add()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			add()
		case <-ctx.Done():
			return
		}
	}
}

// openSMSTransport picks the SMS backend: a phone-based HTTP gateway if
// -sms-gateway-url is set, otherwise a serial GSM modem (or a no-op
// dry-run modem if -sms-port is also empty).
func openSMSTransport(gatewayURL, gatewayToken, modemPort string, modemBaud int) (sms.Transport, error) {
	if gatewayURL != "" {
		return sms.OpenHTTPGateway(gatewayURL, gatewayToken), nil
	}
	return sms.Open(modemPort, modemBaud)
}

// runSMSOrderLoop polls the SMS transport for unread messages at a fixed
// interval, hands each one to the order handler, texts back the reply, and
// acknowledges it so it isn't processed twice. Stops when ctx is cancelled.
func runSMSOrderLoop(ctx context.Context, transport sms.Transport, handler *smsorder.Handler, interval time.Duration) {
	for {
		messages, err := transport.ReadUnread()
		if err != nil {
			applog.L.Error("sms-ordering: failed to read incoming messages", "error", err)
		}
		for _, msg := range messages {
			reply := handler.HandleMessage(msg.From, msg.Body)
			if err := transport.SendSMS(msg.From, reply); err != nil {
				applog.L.Error("sms-ordering: failed to reply", "to", msg.From, "error", err)
			}
			if err := transport.Ack(msg.ID); err != nil {
				applog.L.Error("sms-ordering: failed to acknowledge processed message", "id", msg.ID, "error", err)
			}
		}

		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return
		}
	}
}
