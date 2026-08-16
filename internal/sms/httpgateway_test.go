package sms

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakePhoneGateway spins up an in-process HTTP server implementing the same
// contract a real Android phone gateway would, so HTTPGateway's client code
// can be verified end-to-end without any actual phone hardware.
func fakePhoneGateway(t *testing.T) (*httptest.Server, *[]map[string]string) {
	t.Helper()
	var sent []map[string]string
	unread := []IncomingMessage{
		{ID: "1", From: "+919876543210", Body: "ORDER PARA 2"},
	}
	acked := map[string]bool{}

	mux := http.NewServeMux()
	mux.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		sent = append(sent, body)
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
	})
	mux.HandleFunc("/inbox/unread", func(w http.ResponseWriter, r *http.Request) {
		var out []IncomingMessage
		for _, m := range unread {
			if !acked[m.ID] {
				out = append(out, m)
			}
		}
		json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/inbox/ack", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		acked[body["id"]] = true
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &sent
}

func TestHTTPGateway_SendSMS(t *testing.T) {
	server, sent := fakePhoneGateway(t)
	gw := OpenHTTPGateway(server.URL, "")

	if err := gw.SendSMS("+911111111111", "hello"); err != nil {
		t.Fatalf("SendSMS failed: %v", err)
	}
	if len(*sent) != 1 || (*sent)[0]["to"] != "+911111111111" || (*sent)[0]["message"] != "hello" {
		t.Errorf("gateway did not receive expected send payload, got: %+v", *sent)
	}
}

func TestHTTPGateway_ReadUnreadAndAck(t *testing.T) {
	server, _ := fakePhoneGateway(t)
	gw := OpenHTTPGateway(server.URL, "")

	msgs, err := gw.ReadUnread()
	if err != nil {
		t.Fatalf("ReadUnread failed: %v", err)
	}
	if len(msgs) != 1 || msgs[0].From != "+919876543210" || msgs[0].Body != "ORDER PARA 2" {
		t.Fatalf("unexpected unread messages: %+v", msgs)
	}

	if err := gw.Ack(msgs[0].ID); err != nil {
		t.Fatalf("Ack failed: %v", err)
	}

	msgs2, err := gw.ReadUnread()
	if err != nil {
		t.Fatalf("ReadUnread after ack failed: %v", err)
	}
	if len(msgs2) != 0 {
		t.Errorf("expected no unread messages after ack, got: %+v", msgs2)
	}
}

func TestHTTPGateway_UnreachableGateway(t *testing.T) {
	gw := OpenHTTPGateway("http://127.0.0.1:1", "") // nothing listens here
	if err := gw.SendSMS("+911111111111", "hello"); err == nil {
		t.Error("expected an error when the phone gateway is unreachable")
	}
}
