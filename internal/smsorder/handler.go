// Package smsorder implements the text-message ordering protocol for
// customers on basic/feature phones with no internet access at all - just
// the ability to send and receive SMS. A customer texts simple commands to
// the kiosk's phone number and gets a reply back, entirely over SMS.
//
// Supported commands (case-insensitive):
//
//	LIST                          - list available medicines and their codes
//	HELP                          - show how to order
//	ORDER <code> <qty>[, <code> <qty>]...
//	                              - place an order, e.g. "ORDER PARA 2, ORS1 1"
package smsorder

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"medistock/internal/alerts"
	"medistock/internal/repo"
)

type Handler struct {
	medicines *repo.MedicineRepo
	customers *repo.CustomerRepo
	orders    *repo.OrderRepo
	notifier  *alerts.Notifier
}

func New(medicines *repo.MedicineRepo, customers *repo.CustomerRepo, orders *repo.OrderRepo, notifier *alerts.Notifier) *Handler {
	return &Handler{medicines: medicines, customers: customers, orders: orders, notifier: notifier}
}

const helpText = "MediStock SMS ordering: Text LIST to see medicines & codes. " +
	"Text ORDER <code> <qty> to order, e.g. ORDER PARA 2, ORS1 1. Text HELP for this message."

// HandleMessage processes one incoming SMS body from fromNumber and returns
// the reply text to send back. It never returns an error - any problem
// (unknown command, bad stock, etc.) becomes a human-readable SMS reply
// instead, since that's the only channel available to tell the customer
// what went wrong.
func (h *Handler) HandleMessage(fromNumber, body string) string {
	text := strings.TrimSpace(body)
	upper := strings.ToUpper(text)

	switch {
	case upper == "" || upper == "HELP":
		return helpText
	case upper == "LIST" || upper == "STOCK":
		return h.handleList()
	case strings.HasPrefix(upper, "ORDER"):
		return h.handleOrder(fromNumber, text[len("ORDER"):])
	default:
		return "Sorry, I didn't understand that. " + helpText
	}
}

func (h *Handler) handleList() string {
	items, err := h.medicines.Available("")
	if err != nil {
		return "Sorry, could not look up stock right now. Please try again shortly."
	}
	if len(items) == 0 {
		return "No medicines are currently available. Please check back later."
	}

	const maxItems = 8
	shown := items
	more := 0
	if len(shown) > maxItems {
		more = len(shown) - maxItems
		shown = shown[:maxItems]
	}

	var b strings.Builder
	b.WriteString("Available (code: name price, qty): ")
	for i, m := range shown {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s: %s %.2f, qty %d", m.Code, m.Name, m.Price, m.Quantity)
	}
	if more > 0 {
		fmt.Fprintf(&b, ". +%d more not shown - ask staff for the full list.", more)
	}
	return b.String()
}

// orderItemPattern matches a "<code> <qty>" pair, tolerating a comma/x/*
// between them (people on numeric keypads type inconsistently), e.g.
// "PARA 2", "PARA x2", "PARA2" (falls back to splitting trailing digits).
var orderItemPattern = regexp.MustCompile(`(?i)([A-Z0-9]+?)\s*[xX*]?\s*(\d+)`)

func (h *Handler) handleOrder(fromNumber, rest string) string {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "To order, text: ORDER <code> <qty>, e.g. ORDER PARA 2. Text LIST for codes."
	}

	matches := orderItemPattern.FindAllStringSubmatch(rest, -1)
	if len(matches) == 0 {
		return "Could not read your order. Format: ORDER <code> <qty>, e.g. ORDER PARA 2, ORS1 1."
	}

	var lines []repo.CartLine
	var summary []string
	var problems []string

	for _, match := range matches {
		code := strings.ToUpper(match[1])
		qty, err := strconv.Atoi(match[2])
		if err != nil || qty <= 0 {
			problems = append(problems, fmt.Sprintf("%s: invalid quantity", code))
			continue
		}

		m, err := h.medicines.FindByCode(code)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: unknown code", code))
			continue
		}
		if m.Quantity <= 0 {
			problems = append(problems, fmt.Sprintf("%s: out of stock", m.Code))
			continue
		}

		capped := qty
		if m.MaxPerOrder > 0 && capped > m.MaxPerOrder {
			capped = m.MaxPerOrder
		}
		if capped > m.Quantity {
			capped = m.Quantity
		}
		if capped <= 0 {
			problems = append(problems, fmt.Sprintf("%s: unavailable", m.Code))
			continue
		}

		lines = append(lines, repo.CartLine{MedicineID: m.ID, Quantity: capped})
		note := fmt.Sprintf("%s x%d", m.Code, capped)
		if capped != qty {
			note += fmt.Sprintf(" (capped from %d)", qty)
		}
		summary = append(summary, note)
	}

	if len(lines) == 0 {
		return "Order failed: " + strings.Join(problems, "; ") + ". Text LIST for available codes."
	}

	customerID, err := h.resolveCustomer(fromNumber)
	if err != nil {
		return "Sorry, something went wrong saving your order. Please try again."
	}

	order, err := h.orders.Create(customerID, lines)
	if err != nil {
		return "Order failed: " + err.Error() + ". Text LIST to check current stock."
	}

	for _, it := range order.Items {
		h.notifier.Check(it.MedicineID)
	}

	reply := fmt.Sprintf("Order confirmed! Token #%d. Items: %s. Total %.2f. Show this token number at the counter to collect.",
		order.ID, strings.Join(summary, ", "), order.Total)
	if len(problems) > 0 {
		reply += " (Skipped: " + strings.Join(problems, "; ") + ".)"
	}
	return reply
}

func (h *Handler) resolveCustomer(phone string) (int64, error) {
	if phone == "" {
		return 0, nil
	}
	c, err := h.customers.FindOrCreateByPhone(phone, phone)
	if err != nil {
		return 0, err
	}
	return c.ID, nil
}
