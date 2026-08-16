// Package alerts wires stock levels to the SMS reorder notifier: whenever a
// medicine's stock drops to or below its reorder level, it texts the
// configured distributor/retailer number so restocking can be requested
// without any internet connection - only cellular signal is needed, since
// SMS travels over the carrier's signalling channel rather than mobile data.
package alerts

import (
	"fmt"
	"log"

	"medistock/internal/repo"
	"medistock/internal/sms"
)

// Notifier checks medicines after stock-affecting operations and sends a
// reorder SMS when needed. If distributorPhone is empty, alerts are
// disabled entirely (Check becomes a no-op) - useful when this feature
// isn't configured for a given deployment.
type Notifier struct {
	medicines        *repo.MedicineRepo
	transport        sms.Transport
	distributorPhone string
}

func New(medicines *repo.MedicineRepo, transport sms.Transport, distributorPhone string) *Notifier {
	return &Notifier{medicines: medicines, transport: transport, distributorPhone: distributorPhone}
}

// Check re-reads the given medicine's current stock and sends (or clears)
// a low-stock SMS alert as needed. Call this after any operation that
// changes a medicine's quantity: adding stock, adjusting stock, or an
// order deducting stock. Errors are logged, not returned, so a flaky modem
// never blocks the actual inventory/order operation that triggered it.
func (n *Notifier) Check(medicineID int64) {
	if n == nil || n.distributorPhone == "" {
		return
	}

	m, err := n.medicines.Get(medicineID)
	if err != nil {
		log.Printf("alerts: could not look up medicine %d: %v", medicineID, err)
		return
	}

	low := m.Quantity <= m.ReorderLevel
	switch {
	case low && !m.LowStockAlertSent:
		msg := fmt.Sprintf("MediStock reorder alert: %q is low (qty %d, reorder threshold %d). Please resupply.",
			m.Name, m.Quantity, m.ReorderLevel)
		if err := n.transport.SendSMS(n.distributorPhone, msg); err != nil {
			log.Printf("alerts: failed to send reorder SMS for %q: %v", m.Name, err)
			return // don't mark as sent - retry on the next stock change
		}
		if err := n.medicines.SetLowStockAlertSent(medicineID, true); err != nil {
			log.Printf("alerts: sent SMS but failed to record it for %q: %v", m.Name, err)
		}
	case !low && m.LowStockAlertSent:
		// Restocked above the threshold - clear the flag so a future dip
		// triggers a fresh alert instead of staying silent forever.
		if err := n.medicines.SetLowStockAlertSent(medicineID, false); err != nil {
			log.Printf("alerts: failed to clear alert flag for %q: %v", m.Name, err)
		}
	}
}
