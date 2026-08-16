package cli

import (
	"fmt"
	"strconv"
	"strings"

	"medistock/internal/models"
	"medistock/internal/repo"
)

// runKiosk drives the self-service customer flow: browse available
// medicines, build a cart (capped per emergency limits and live stock),
// and check out. Stock is deducted immediately on checkout and a token
// number is printed so the customer can collect their order.
func (a *App) runKiosk() {
	fmt.Println()
	fmt.Println("=========================================")
	fmt.Println("   MEDICINE ORDER KIOSK - SELF SERVICE")
	fmt.Println("   Please select what you need below.")
	fmt.Println("=========================================")

	cart := map[int64]int{} // medicineID -> quantity
	medCache := map[int64]models.Medicine{}

	for {
		fmt.Println()
		fmt.Println("--- KIOSK MENU ---")
		fmt.Println("1. View available medicines")
		fmt.Println("2. Search for a medicine")
		fmt.Println("3. Add item to my order")
		fmt.Println("4. View my current order")
		fmt.Println("5. Remove item from my order")
		fmt.Println("6. Checkout / submit order")
		fmt.Println("7. Cancel and exit kiosk")
		fmt.Println("------------------")
		choice := a.prompt("Select an option: ")

		switch choice {
		case "1":
			items, err := a.medicines.Available("")
			if err != nil {
				fmt.Println("Error:", err)
				continue
			}
			cacheAndPrintKiosk(items, medCache)
		case "2":
			q := a.prompt("Search by name: ")
			items, err := a.medicines.Available(q)
			if err != nil {
				fmt.Println("Error:", err)
				continue
			}
			cacheAndPrintKiosk(items, medCache)
		case "3":
			a.kioskAddToCart(cart, medCache)
		case "4":
			printCart(cart, medCache)
		case "5":
			a.kioskRemoveFromCart(cart)
		case "6":
			if a.kioskCheckout(cart) {
				return // order placed, kick this person back to the welcome screen
			}
		case "7", "q", "Q":
			fmt.Println("Order cancelled. Nothing was submitted.")
			return
		default:
			fmt.Println("Invalid option, please try again.")
		}
	}
}

func cacheAndPrintKiosk(items []models.Medicine, cache map[int64]models.Medicine) {
	if len(items) == 0 {
		fmt.Println("No medicines currently available.")
		return
	}
	fmt.Printf("\n%-4s %-8s %-24s %-9s %-8s %s\n", "ID", "Code", "Name", "Price", "In Stock", "Max/Order")
	fmt.Println(strings.Repeat("-", 75))
	for _, m := range items {
		cache[m.ID] = m
		fmt.Printf("%-4d %-8s %-24s %-9.2f %-8d %s\n", m.ID, m.Code, truncate(m.Name, 24), m.Price, m.Quantity, maxOrderLabel(m.MaxPerOrder))
	}
}

func (a *App) kioskAddToCart(cart map[int64]int, cache map[int64]models.Medicine) {
	idStr := a.prompt("Medicine ID to add (see 'View available medicines' for IDs): ")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		fmt.Println("Invalid ID.")
		return
	}

	m, ok := cache[id]
	if !ok {
		m, err = a.medicines.Get(id)
		if err != nil || m.Quantity <= 0 {
			fmt.Println("That medicine is not available right now.")
			return
		}
		cache[id] = m
	}

	limit := m.Quantity
	if m.MaxPerOrder > 0 && m.MaxPerOrder < limit {
		limit = m.MaxPerOrder
	}
	already := cart[id]
	remaining := limit - already
	if remaining <= 0 {
		fmt.Printf("You've already reached the max allowed (%d) for %s.\n", limit, m.Name)
		return
	}

	fmt.Printf("  %s - price %.2f, you can order up to %d more (emergency limit + stock applied)\n", m.Name, m.Price, remaining)
	qty := a.promptInt("  Quantity: ")
	if qty <= 0 {
		fmt.Println("  Skipped.")
		return
	}
	if qty > remaining {
		fmt.Printf("  Only %d allowed - adding %d instead.\n", remaining, remaining)
		qty = remaining
	}
	cart[id] = already + qty
	fmt.Println("  Added to your order.")
}

func (a *App) kioskRemoveFromCart(cart map[int64]int) {
	if len(cart) == 0 {
		fmt.Println("Your order is empty.")
		return
	}
	idStr := a.prompt("Medicine ID to remove: ")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		fmt.Println("Invalid ID.")
		return
	}
	if _, ok := cart[id]; !ok {
		fmt.Println("That item is not in your order.")
		return
	}
	delete(cart, id)
	fmt.Println("Removed.")
}

func printCart(cart map[int64]int, cache map[int64]models.Medicine) {
	if len(cart) == 0 {
		fmt.Println("Your order is empty.")
		return
	}
	fmt.Println("\n-- Your Current Order --")
	var total float64
	for id, qty := range cart {
		m := cache[id]
		subtotal := m.Price * float64(qty)
		total += subtotal
		fmt.Printf("%-24s x%-3d %8.2f\n", truncate(m.Name, 24), qty, subtotal)
	}
	fmt.Printf("Running total: %.2f\n", total)
}

// kioskCheckout finalizes the cart into an order. Returns true if an order
// was successfully placed (caller should return to the welcome screen).
func (a *App) kioskCheckout(cart map[int64]int) bool {
	if len(cart) == 0 {
		fmt.Println("Your order is empty - add at least one item before checking out.")
		return false
	}

	name := a.prompt("Your name (optional, blank for anonymous): ")
	var customerID int64
	if name != "" {
		phone := a.prompt("Phone number (optional): ")
		c, err := a.customers.FindOrCreateByPhone(name, phone)
		if err != nil {
			fmt.Println("Error saving your details:", err)
			return false
		}
		customerID = c.ID
	}

	var lines []repo.CartLine
	for id, qty := range cart {
		lines = append(lines, repo.CartLine{MedicineID: id, Quantity: qty})
	}

	order, err := a.orders.Create(customerID, lines)
	if err != nil {
		fmt.Println("Sorry, your order could not be placed:", err)
		fmt.Println("(Stock may have changed since you added items - check quantities and try again.)")
		return false
	}

	printKioskReceipt(order)
	for _, it := range order.Items {
		a.notifier.Check(it.MedicineID)
	}
	return true
}

func printKioskReceipt(o models.Order) {
	fmt.Println()
	fmt.Println("=========================================")
	fmt.Println("        ORDER CONFIRMED - PLEASE COLLECT")
	fmt.Println("=========================================")
	fmt.Printf(">>> YOUR TOKEN NUMBER: #%d <<<\n", o.ID)
	fmt.Println("Show this token number at the counter to collect your medicine.")
	fmt.Println(strings.Repeat("-", 41))
	for _, it := range o.Items {
		fmt.Printf("%-24s x%-3d %8.2f\n", truncate(it.MedicineName, 24), it.Quantity, it.Subtotal)
	}
	fmt.Println(strings.Repeat("-", 41))
	fmt.Printf("TOTAL: %33.2f\n", o.Total)
	fmt.Println("=========================================")
}
