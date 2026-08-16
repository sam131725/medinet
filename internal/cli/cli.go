// Package cli implements the interactive terminal menu for the offline
// medicine ordering (pharmacy billing) system.
package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"medistock/internal/alerts"
	"medistock/internal/models"
	"medistock/internal/repo"
)

type App struct {
	medicines *repo.MedicineRepo
	customers *repo.CustomerRepo
	orders    *repo.OrderRepo
	notifier  *alerts.Notifier
	in        *bufio.Reader
}

func NewApp(medicines *repo.MedicineRepo, customers *repo.CustomerRepo, orders *repo.OrderRepo, notifier *alerts.Notifier) *App {
	return &App{
		medicines: medicines,
		customers: customers,
		orders:    orders,
		notifier:  notifier,
		in:        bufio.NewReader(os.Stdin),
	}
}

func (a *App) Run() {
	fmt.Println("=================================================")
	fmt.Println(" MediStock - Offline Emergency Medicine Kiosk")
	fmt.Println(" (fully local, no internet or network required)")
	fmt.Println("=================================================")

	for {
		fmt.Println()
		fmt.Println("--------- WELCOME ---------")
		fmt.Println("1. Customer - Order medicine (self-service kiosk)")
		fmt.Println("2. Staff - Manage inventory & orders")
		fmt.Println("3. Exit")
		fmt.Println("---------------------------")
		choice := a.prompt("Select an option: ")
		switch choice {
		case "1":
			a.runKiosk()
		case "2":
			a.runStaffMenu()
		case "3", "q", "Q":
			fmt.Println("Goodbye!")
			return
		default:
			fmt.Println("Invalid option, please try again.")
		}
	}
}

func (a *App) runStaffMenu() {
	for {
		a.printStaffMenu()
		choice := a.prompt("Select an option: ")
		switch choice {
		case "1":
			a.addMedicine()
		case "2":
			a.listMedicines()
		case "3":
			a.searchMedicine()
		case "4":
			a.updateStock()
		case "5":
			a.lowStockReport()
		case "6":
			a.createOrder()
		case "7":
			a.listOrders()
		case "8":
			a.viewOrder()
		case "9", "q", "Q":
			return
		default:
			fmt.Println("Invalid option, please try again.")
		}
	}
}

func (a *App) printStaffMenu() {
	fmt.Println()
	fmt.Println("--------- STAFF MENU ---------")
	fmt.Println("1. Add medicine")
	fmt.Println("2. List all medicines")
	fmt.Println("3. Search medicine")
	fmt.Println("4. Update stock quantity")
	fmt.Println("5. Low stock report")
	fmt.Println("6. Create new order (bill, staff-assisted)")
	fmt.Println("7. List past orders")
	fmt.Println("8. View order details")
	fmt.Println("9. Back to main menu")
	fmt.Println("------------------------------")
}

// ---- helpers ----

func (a *App) prompt(label string) string {
	fmt.Print(label)
	text, err := a.in.ReadString('\n')
	if err != nil {
		// Input closed (e.g. piped input ran out, or Ctrl-D). Exit cleanly
		// instead of looping forever on an empty read.
		fmt.Println("\nInput closed - exiting.")
		os.Exit(0)
	}
	return strings.TrimSpace(text)
}

func (a *App) promptFloat(label string) float64 {
	for {
		v, err := strconv.ParseFloat(a.prompt(label), 64)
		if err == nil && v >= 0 {
			return v
		}
		fmt.Println("Please enter a valid non-negative number.")
	}
}

func (a *App) promptInt(label string) int {
	for {
		v, err := strconv.Atoi(a.prompt(label))
		if err == nil && v >= 0 {
			return v
		}
		fmt.Println("Please enter a valid non-negative whole number.")
	}
}

// ---- medicine management ----

func (a *App) addMedicine() {
	fmt.Println("\n-- Add Medicine --")
	m := models.Medicine{
		Name:         a.prompt("Name: "),
		Code:         a.prompt("Short SMS order code (blank = auto-generate, e.g. PARA): "),
		Manufacturer: a.prompt("Manufacturer: "),
		Batch:        a.prompt("Batch number: "),
		ExpiryDate:   a.prompt("Expiry date (YYYY-MM-DD): "),
		Price:        a.promptFloat("Price per unit: "),
		Quantity:     a.promptInt("Initial stock quantity: "),
		ReorderLevel: a.promptInt("Reorder level (alert threshold): "),
		MaxPerOrder:  a.promptInt("Max quantity per order, emergency limit (0 = no limit): "),
	}
	id, err := a.medicines.Add(m)
	if err != nil {
		fmt.Println("Error adding medicine:", err)
		return
	}
	saved, _ := a.medicines.Get(id)
	fmt.Printf("Added %q with ID %d, SMS order code: %s\n", m.Name, id, saved.Code)
	a.notifier.Check(id)
}

func (a *App) listMedicines() {
	items, err := a.medicines.List()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	printMedicineTable(items)
}

func (a *App) searchMedicine() {
	q := a.prompt("Search by name: ")
	items, err := a.medicines.Search(q)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	printMedicineTable(items)
}

func (a *App) updateStock() {
	id := int64(a.promptInt("Medicine ID: "))
	m, err := a.medicines.Get(id)
	if err != nil {
		fmt.Println("Medicine not found:", err)
		return
	}
	fmt.Printf("Current stock for %q: %d\n", m.Name, m.Quantity)
	delta := a.promptInt("Quantity to add (enter as positive number, use option below for removal): ")
	sign := a.prompt("Type 'add' to add stock or 'remove' to remove stock: ")
	if strings.EqualFold(sign, "remove") {
		delta = -delta
	}
	if err := a.medicines.AdjustStock(id, delta); err != nil {
		fmt.Println("Error updating stock:", err)
		return
	}
	fmt.Println("Stock updated.")
	a.notifier.Check(id)
}

func (a *App) lowStockReport() {
	items, err := a.medicines.LowStock()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	if len(items) == 0 {
		fmt.Println("No medicines are low on stock.")
		return
	}
	fmt.Println("\n-- Low Stock Alert --")
	printMedicineTable(items)
}

func printMedicineTable(items []models.Medicine) {
	if len(items) == 0 {
		fmt.Println("No medicines found.")
		return
	}
	fmt.Printf("\n%-4s %-8s %-22s %-14s %-10s %-9s %-8s %-9s %s\n", "ID", "Code", "Name", "Manufacturer", "Expiry", "Price", "Stock", "Reorder@", "MaxOrder")
	fmt.Println(strings.Repeat("-", 105))
	for _, m := range items {
		fmt.Printf("%-4d %-8s %-22s %-14s %-10s %-9.2f %-8d %-9d %s\n",
			m.ID, m.Code, truncate(m.Name, 22), truncate(m.Manufacturer, 14), m.ExpiryDate, m.Price, m.Quantity, m.ReorderLevel, maxOrderLabel(m.MaxPerOrder))
	}
}

func maxOrderLabel(n int) string {
	if n <= 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// ---- orders / billing ----

func (a *App) createOrder() {
	fmt.Println("\n-- New Order --")
	name := a.prompt("Customer name (blank for walk-in): ")
	phone := ""
	var customerID int64
	if name != "" {
		phone = a.prompt("Customer phone: ")
		c, err := a.customers.FindOrCreateByPhone(name, phone)
		if err != nil {
			fmt.Println("Error with customer record:", err)
			return
		}
		customerID = c.ID
	}

	var lines []repo.CartLine
	for {
		idStr := a.prompt("Medicine ID to add (blank to finish): ")
		if idStr == "" {
			break
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			fmt.Println("Invalid ID.")
			continue
		}
		m, err := a.medicines.Get(id)
		if err != nil {
			fmt.Println("Medicine not found.")
			continue
		}
		fmt.Printf("  %s - price %.2f, in stock: %d\n", m.Name, m.Price, m.Quantity)
		qty := a.promptInt("  Quantity: ")
		if qty <= 0 {
			fmt.Println("  Skipped (quantity must be > 0).")
			continue
		}
		lines = append(lines, repo.CartLine{MedicineID: id, Quantity: qty})
		fmt.Println("  Added to cart.")
	}

	if len(lines) == 0 {
		fmt.Println("Order cancelled - no items added.")
		return
	}

	order, err := a.orders.Create(customerID, lines)
	if err != nil {
		fmt.Println("Error creating order:", err)
		return
	}
	printReceipt(order)
	for _, it := range order.Items {
		a.notifier.Check(it.MedicineID)
	}
}

func (a *App) listOrders() {
	orders, err := a.orders.List()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	if len(orders) == 0 {
		fmt.Println("No orders yet.")
		return
	}
	fmt.Printf("\n%-5s %-20s %-20s %s\n", "ID", "Customer", "Date/Time", "Total")
	fmt.Println(strings.Repeat("-", 65))
	for _, o := range orders {
		fmt.Printf("%-5d %-20s %-20s %.2f\n", o.ID, truncate(o.CustomerName, 20), o.CreatedAt.Format("2006-01-02 15:04"), o.Total)
	}
}

func (a *App) viewOrder() {
	id := int64(a.promptInt("Order ID: "))
	order, err := a.orders.Get(id)
	if err != nil {
		fmt.Println("Order not found:", err)
		return
	}
	printReceipt(order)
}

func printReceipt(o models.Order) {
	fmt.Println("\n============ RECEIPT ============")
	fmt.Printf("Order #%d\n", o.ID)
	fmt.Printf("Customer: %s\n", nonEmpty(o.CustomerName, "Walk-in"))
	fmt.Printf("Date: %s\n", o.CreatedAt.Format("2006-01-02 15:04"))
	fmt.Println(strings.Repeat("-", 34))
	for _, it := range o.Items {
		fmt.Printf("%-18s x%-3d %8.2f\n", truncate(it.MedicineName, 18), it.Quantity, it.Subtotal)
	}
	fmt.Println(strings.Repeat("-", 34))
	fmt.Printf("TOTAL: %26.2f\n", o.Total)
	fmt.Println("==================================")
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
