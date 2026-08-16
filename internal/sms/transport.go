package sms

// IncomingMessage is one unread SMS retrieved from a transport (a modem or
// an HTTP gateway). ID is opaque to callers - pass it back to Ack to mark
// the message handled, whatever that means for the underlying transport
// (deleting it from modem memory, or telling a phone app it's been read).
type IncomingMessage struct {
	ID   string
	From string
	Body string
}

// Transport is anything that can send and receive SMS on medistock's
// behalf. There are two implementations: Modem (a serial-attached GSM
// modem/SIM, talked to over AT commands) and HTTPGateway (a phone running
// a small local app that exposes SMS send/receive over HTTP on the same
// offline network) - see httpgateway.go for when to use which.
type Transport interface {
	SendSMS(number, message string) error
	ReadUnread() ([]IncomingMessage, error)
	Ack(id string) error
	Close() error
}
