// Package sms sends text messages through a serial-attached GSM modem using
// plain AT commands. SMS travels over the cellular network's signalling
// channel, not mobile data - so this keeps working in places where data /
// internet is down but phone signal is still up, which is the common case
// in an emergency or rural outage.
//
// No third-party Go modules are used here on purpose (this project vendors
// its dependencies for fully offline builds, and keeping the hardware layer
// dependency-free keeps that simple). Serial port setup is delegated to the
// standard `stty` command, which ships on essentially every Linux system.
// On Windows or systems without `stty`, run in dry-run mode (leave the port
// empty) or extend Open() for your platform's serial APIs.
package sms

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Modem represents a connection to a GSM modem over a serial port. When
// port is empty, it operates in "dry run" mode: SendSMS logs what it would
// have sent instead of touching any hardware. This makes it safe to enable
// the reorder-alert feature everywhere and only wire up a real modem when
// the hardware is actually present.
type Modem struct {
	port   string
	baud   int
	dryRun bool
	file   *os.File
}

// Open prepares a modem connection. If port is empty, Open returns a modem
// in dry-run mode (always succeeds, never touches hardware).
func Open(port string, baud int) (*Modem, error) {
	if port == "" {
		return &Modem{dryRun: true}, nil
	}
	if baud <= 0 {
		baud = 9600
	}

	if err := configureSerial(port, baud); err != nil {
		return nil, fmt.Errorf("configure serial port %s: %w", port, err)
	}

	f, err := os.OpenFile(port, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open serial port %s: %w", port, err)
	}

	return &Modem{port: port, baud: baud, file: f}, nil
}

var _ Transport = (*Modem)(nil)

func configureSerial(port string, baud int) error {
	// `stty -F <port> <baud> raw -echo time 20 min 0` puts the line into raw
	// mode (no line editing / echoing AT commands back at us) and makes
	// reads return after ~2 seconds of silence instead of blocking forever.
	cmd := exec.Command("stty", "-F", port, fmt.Sprintf("%d", baud), "raw", "-echo", "time", "20", "min", "0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Modem) Close() error {
	if m.file != nil {
		return m.file.Close()
	}
	return nil
}

// SendSMS sends a text message to the given phone number via standard AT
// commands (AT+CMGF for text mode, AT+CMGS to send). It returns an error if
// the modem doesn't acknowledge the message was sent.
func (m *Modem) SendSMS(number, message string) error {
	if m.dryRun {
		fmt.Printf("[SMS dry-run - no modem configured] would text %s: %s\n", number, message)
		return nil
	}

	if err := m.sendCommand("AT\r", 3*time.Second); err != nil {
		return fmt.Errorf("modem not responding: %w", err)
	}
	if err := m.sendCommand("AT+CMGF=1\r", 3*time.Second); err != nil {
		return fmt.Errorf("set text mode: %w", err)
	}
	if err := m.sendCommand(fmt.Sprintf("AT+CMGS=%q\r", number), 3*time.Second); err != nil {
		return fmt.Errorf("start message to %s: %w", number, err)
	}
	// The modem replies with a "> " prompt waiting for the message body,
	// terminated by Ctrl+Z (0x1A).
	if err := m.sendCommand(message+"\x1a", 15*time.Second); err != nil {
		return fmt.Errorf("send message body: %w", err)
	}
	return nil
}

// ReadUnread fetches unread SMS messages from the modem using AT+CMGL in
// text mode and returns them. It does not mark them read or delete them -
// call Ack once you've handled and replied to one.
func (m *Modem) ReadUnread() ([]IncomingMessage, error) {
	if m.dryRun {
		return nil, nil
	}
	if err := m.sendCommand("AT+CMGF=1\r", 3*time.Second); err != nil {
		return nil, fmt.Errorf("set text mode: %w", err)
	}

	if _, err := m.file.Write([]byte(`AT+CMGL="REC UNREAD"` + "\r")); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(5 * time.Second)
	reader := bufio.NewReader(m.file)
	var raw strings.Builder
	buf := make([]byte, 512)
	for time.Now().Before(deadline) {
		n, err := reader.Read(buf)
		if n > 0 {
			raw.Write(buf[:n])
			if strings.Contains(raw.String(), "OK") || strings.Contains(raw.String(), "ERROR") {
				break
			}
		}
		if err != nil {
			continue
		}
	}

	return parseCMGL(raw.String()), nil
}

// listHeaderPattern matches a +CMGL header line, e.g.:
// +CMGL: 3,"REC UNREAD","+919999999999",,"24/08/16,10:00:00+22"
var listHeaderPattern = regexp.MustCompile(`\+CMGL:\s*(\d+)\s*,\s*"[^"]*"\s*,\s*"([^"]*)"`)

// parseCMGL parses the text-mode response to AT+CMGL into a list of
// messages. Each match is a header line followed by the message body on
// the next line(s) until the next header or OK/ERROR.
func parseCMGL(raw string) []IncomingMessage {
	lines := strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n")
	var out []IncomingMessage
	var current *IncomingMessage

	flush := func() {
		if current != nil {
			current.Body = strings.TrimSpace(current.Body)
			out = append(out, *current)
			current = nil
		}
	}

	for _, line := range lines {
		if m := listHeaderPattern.FindStringSubmatch(line); m != nil {
			flush()
			current = &IncomingMessage{ID: m[1], From: m[2]}
			continue
		}
		if current != nil && strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "OK") {
			if current.Body != "" {
				current.Body += " "
			}
			current.Body += line
		}
	}
	flush()
	return out
}

// Ack removes a message from modem storage by its ID (the modem's storage
// index, as returned in IncomingMessage.ID), so it isn't processed again.
func (m *Modem) Ack(id string) error {
	if m.dryRun {
		return nil
	}
	index, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("invalid message id %q: %w", id, err)
	}
	return m.sendCommand(fmt.Sprintf("AT+CMGD=%d\r", index), 3*time.Second)
}

// sendCommand writes an AT command and reads back whatever the modem sends
// within the timeout, returning an error if the response contains "ERROR"
// or nothing came back at all.
func (m *Modem) sendCommand(cmd string, timeout time.Duration) error {
	if _, err := m.file.Write([]byte(cmd)); err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	reader := bufio.NewReader(m.file)
	var response strings.Builder
	buf := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, err := reader.Read(buf)
		if n > 0 {
			response.Write(buf[:n])
			text := response.String()
			if strings.Contains(text, "OK") || strings.Contains(text, ">") {
				return nil
			}
			if strings.Contains(text, "ERROR") {
				return fmt.Errorf("modem returned error: %s", strings.TrimSpace(text))
			}
		}
		if err != nil {
			// timed-out non-blocking read (stty time/min settings); keep polling
			continue
		}
	}
	if response.Len() == 0 {
		return fmt.Errorf("no response from modem")
	}
	return fmt.Errorf("unexpected response: %s", strings.TrimSpace(response.String()))
}
