package models

import "time"

// Medicine represents a single medicine/product in stock.
type Medicine struct {
	ID   int64
	Name string
	// Code is a short, unique, typo-tolerant identifier (e.g. "PARA") used
	// for SMS ordering from basic phones, where typing a full medicine name
	// on a numeric keypad is impractical.
	Code         string
	Manufacturer string
	Batch        string
	ExpiryDate   string // YYYY-MM-DD
	Price        float64
	Quantity     int
	ReorderLevel int
	// MaxPerOrder caps how many units of this medicine a single order may
	// contain. Used to prevent hoarding during emergencies. 0 means no cap
	// (only available stock limits the quantity).
	MaxPerOrder int
	// LowStockAlertSent tracks whether a reorder SMS has already been sent
	// for the current low-stock episode, to avoid re-alerting on every
	// single subsequent order until stock is replenished.
	LowStockAlertSent bool
}

// Customer represents a buyer.
type Customer struct {
	ID    int64
	Name  string
	Phone string
}

// Order represents a completed bill/sale.
type Order struct {
	ID           int64
	CustomerID   int64
	CustomerName string
	CreatedAt    time.Time
	Total        float64
	Items        []OrderItem
}

// OrderItem represents one line item within an order.
type OrderItem struct {
	ID           int64
	OrderID      int64
	MedicineID   int64
	MedicineName string
	Quantity     int
	UnitPrice    float64
	Subtotal     float64
}
