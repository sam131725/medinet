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
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"medistock/internal/alerts"
	"medistock/internal/cli"
	"medistock/internal/db"
	"medistock/internal/repo"
	"medistock/internal/sms"
	"medistock/internal/smsorder"
	"medistock/internal/webui"
)

func main() {
	web := flag.Bool("web", false, "serve a touch-friendly local web kiosk instead of the terminal menu")
	port := flag.Int("port", 8080, "port for the web kiosk (only used with -web)")
	dbPath := flag.String("db", "medistock.db", "path to the local SQLite database file")
	staffPIN := flag.String("staff-pin", "1234", "PIN required to access the staff page/API in web mode (change this!)")

	smsPort := flag.String("sms-port", "", "serial device for a GSM modem, e.g. /dev/ttyUSB0")
	smsBaud := flag.Int("sms-baud", 9600, "baud rate for the GSM modem serial connection")
	smsGatewayURL := flag.String("sms-gateway-url", "", "base URL of a phone-based SMS gateway instead of a modem, e.g. http://192.168.1.50:8090 (see docs/phone-sms-gateway)")
	smsGatewayToken := flag.String("sms-gateway-token", "", "optional bearer token for the phone SMS gateway")

	distributorPhone := flag.String("distributor-phone", "", "phone number to text low-stock reorder alerts to (empty = alerts disabled)")
	smsOrdering := flag.Bool("sms-ordering", false, "let customers on basic phones place orders by texting the SMS transport's number")
	smsPollInterval := flag.Duration("sms-poll-interval", 15*time.Second, "how often to check for new incoming SMS orders")
	flag.Parse()

	// Backward-compatible: `medistock somefile.db` still works as before.
	if flag.NArg() > 0 {
		*dbPath = flag.Arg(0)
	}

	sqlDB, err := db.Open(*dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to open database:", err)
		os.Exit(1)
	}
	defer sqlDB.Close()

	medicineRepo := repo.NewMedicineRepo(sqlDB)
	customerRepo := repo.NewCustomerRepo(sqlDB)
	orderRepo := repo.NewOrderRepo(sqlDB)

	transport, err := openSMSTransport(*smsGatewayURL, *smsGatewayToken, *smsPort, *smsBaud)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to set up SMS transport:", err)
		os.Exit(1)
	}
	defer transport.Close()
	notifier := alerts.New(medicineRepo, transport, *distributorPhone)

	if *smsOrdering {
		if *smsGatewayURL == "" && *smsPort == "" {
			fmt.Println("Note: -sms-ordering is on but no modem or phone gateway is configured, so incoming SMS polling is a no-op. Set -sms-port or -sms-gateway-url once hardware is ready.")
		}
		handler := smsorder.New(medicineRepo, customerRepo, orderRepo, notifier)
		go runSMSOrderLoop(transport, handler, *smsPollInterval)
	}

	if *web {
		server := webui.New(medicineRepo, customerRepo, orderRepo, notifier, *staffPIN)
		if err := server.Run(*port); err != nil {
			fmt.Fprintln(os.Stderr, "web server error:", err)
			os.Exit(1)
		}
		return
	}

	app := cli.NewApp(medicineRepo, customerRepo, orderRepo, notifier)
	app.Run()
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
// acknowledges it so it isn't processed twice. Runs until the process exits.
func runSMSOrderLoop(transport sms.Transport, handler *smsorder.Handler, interval time.Duration) {
	for {
		messages, err := transport.ReadUnread()
		if err != nil {
			log.Printf("sms-ordering: failed to read incoming messages: %v", err)
		}
		for _, msg := range messages {
			reply := handler.HandleMessage(msg.From, msg.Body)
			if err := transport.SendSMS(msg.From, reply); err != nil {
				log.Printf("sms-ordering: failed to reply to %s: %v", msg.From, err)
			}
			if err := transport.Ack(msg.ID); err != nil {
				log.Printf("sms-ordering: failed to acknowledge processed message %s: %v", msg.ID, err)
			}
		}
		time.Sleep(interval)
	}
}
