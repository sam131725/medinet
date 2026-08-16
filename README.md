# MediStock — Offline Emergency Medicine Kiosk (Go)

A medicine ordering and pharmacy billing system built for places with **no
internet at all** — a relief camp, a disaster response point, or a pharmacy
counter that has to keep working through an outage. Every interface it
offers (a terminal menu, a touch-friendly local web kiosk, and two-way SMS
ordering for basic feature phones) runs against one local SQLite file with
zero network dependency beyond, at most, a local WiFi hotspot or cellular
signal for SMS — never an internet uplink. Even *building* it requires no
internet, since every Go dependency is vendored into this repo.

**Three ways to order:**

- **Terminal menu** — a keyboard-driven CLI for staff, or anywhere typing works fine.
- **Touch web kiosk** (`-web`) — a phone/tablet-friendly self-checkout page for the public, served locally over your own WiFi/hotspot.
- **SMS ordering** (`-sms-ordering`) — customers with a plain feature phone text a short code (`ORDER PARA 2`) to order, no smartphone or app needed, over a GSM modem or a spare Android phone acting as a gateway.

All three share the same inventory and the same emergency safeguards: a
configurable max-quantity-per-order cap to prevent hoarding, real-time stock
enforcement, and automatic low-stock SMS alerts to a distributor. See below
for full setup of each.

## Two staff/customer modes

**Customer kiosk (self-service).** Anyone can walk up to the machine, browse
or search medicines that are currently in stock, add what they need to a
cart, and check out themselves — no staff required. Each medicine can have
an emergency "max per order" cap (e.g. 3 units) set by staff, so no single
person can clear out scarce stock; the kiosk enforces this automatically
alongside real-time stock limits. Checking out deducts stock immediately and
prints a **token number** the person shows at the counter to collect their
order — no payment step, since the priority is getting medicine to people
fast in an emergency.

**Staff menu.** Pharmacy/relief staff use a separate menu to add medicines,
adjust stock, check a low-stock report, place staff-assisted orders, and
look up any past order (including ones customers placed themselves at the
kiosk) by its token/order number.

## Features

- Customer self-service ordering kiosk with emergency per-item quantity limits
- Add / list / search medicines (name, manufacturer, batch, expiry, price, stock, reorder level, max per order)
- Update stock (receive new stock or manually adjust)
- Low-stock report (alerts when quantity <= reorder level)
- Staff-assisted order creation, for when someone can't use the kiosk directly
- View past orders and full order details/receipts, looked up by token number
- All data persisted locally in a single `medistock.db` SQLite file — safe to back up, copy, or move

## Two interfaces: terminal or touchscreen

- **Terminal menu** (default) — good for staff, or anywhere typing on a keyboard is fine.
- **Local web kiosk** (`-web` flag) — a touch-friendly page for a tablet/touchscreen, meant for
  members of the public to use directly. It's still 100% offline: the same binary runs a tiny web
  server, and any device on the same local WiFi/hotspot (or the same machine's own browser) can
  load it — nothing ever leaves that local network, and there's no dependency on an internet
  uplink existing anywhere.

```bash
./medistock -web -staff-pin 4321
```

This prints every local network address the kiosk can be reached at (e.g. `http://192.168.1.5:8080/`)
so you can share that address with a phone/tablet connected to the same offline hotspot. The
customer page is at `/`, and the staff inventory page is at `/staff` (protected by the PIN you set
with `-staff-pin` — change it from the default!). Run `./medistock -web -h` to see all flags
(`-port`, `-db`, `-staff-pin`).

## Ordering by text message - for basic/feature phones with no internet

Beyond the terminal menu and the touch web kiosk, customers can place an
order just by sending a plain text message to the same GSM modem's SIM
number - no app, no smartphone, no internet, just the ability to send an
SMS. This is the option for people with ordinary feature phones during an
emergency.

Each medicine has a short, typo-tolerant **code** (e.g. `PARA`), shown next
to it everywhere in the app - it's set automatically from the medicine's
name when staff add it, or staff can pick a custom one. Customers use these
codes because typing a full medicine name on a numeric keypad is
impractical.

**What a customer texts to the kiosk's number:**

```
LIST                          -> see available medicines and their codes
ORDER PARA 2, ORS1 1          -> order 2 of PARA and 1 of ORS1
HELP                          -> get instructions
```

They get an instant SMS reply confirming the order, a token number to give
staff at pickup, and the total - or a clear error (unknown code, out of
stock, quantity capped by the emergency limit) if something couldn't be
fulfilled exactly as asked.

## Two ways to send/receive the SMS: a GSM modem, or a spare Android phone

SMS ordering and distributor reorder alerts both need something that can
actually send/receive text messages. Two options, either works:

**A dedicated GSM modem** — a small USB/serial modem with a SIM in it
(~$10-20). Point medistock at its serial device:

```bash
./medistock -sms-ordering -sms-port /dev/ttyUSB0 -distributor-phone "+911234567890"
```

**A spare Android phone, no modem hardware needed** — any old phone with a
SIM and signal, running a small local script (`docs/phone-sms-gateway/`) via
the free Termux app, exposing SMS over your local WiFi/hotspot instead of a
serial port. Often easier to get hold of in an emergency than buying modem
hardware. See `docs/phone-sms-gateway/README.md` for full setup:

```bash
./medistock -sms-ordering -sms-gateway-url http://192.168.1.50:8090 -distributor-phone "+911234567890"
```

Both are interchangeable — everything downstream (order parsing, stock
deduction, reorder alerts) works identically either way. `-sms-ordering`
starts a background loop that checks for new messages every 15 seconds by
default (`-sms-poll-interval` to change that), processes each one, texts
back a reply, and acknowledges it so it isn't handled twice. This runs
alongside the terminal menu or the web kiosk (`-web`) - all interfaces
share the same underlying inventory, so an SMS order deducts stock the same
way a kiosk or staff order does, and the existing per-medicine emergency
quantity caps are enforced identically.

Without either `-sms-port` or `-sms-gateway-url` set, `-sms-ordering` is a
safe no-op (nothing to poll) - useful for testing the terminal/web flows
without any SMS hardware attached.

**What's actually verified vs. not:** the order-parsing logic
(`internal/smsorder`) and the HTTP phone-gateway client (`internal/sms`)
both have real automated tests (`go test ./...`) that pass, and both were
also exercised in full end-to-end simulations (a fake GSM modem over a
virtual serial port, and a fake phone-gateway HTTP server) confirming real
orders, stock deduction, and SMS replies all work correctly through the
actual code paths. What hasn't been tested is either one against **real**
hardware — an actual GSM modem or an actual Android phone running Termux —
since this environment has neither. Do one real test with your specific
hardware before relying on this in an actual emergency.

## Reaching a distributor with no internet: SMS reorder alerts

This is a single-location system — by itself it has no way to contact anyone
outside the machine it runs on. But when a medicine's stock drops to or below
its reorder level, it can automatically text a distributor/retailer a reorder
request over SMS (via either transport above), rather than mobile data.
This matters because in many outages phone/cell signal survives even when
internet and data connectivity are down — SMS travels over the carrier's
signalling channel, not the data network.

If you leave both `-sms-port` and `-sms-gateway-url` unset, alerts still
fire on schedule but are simply printed to the console instead of sent —
useful for testing the feature, or for a manual process (e.g. staff relaying
the alert by radio) before you have SMS hardware set up.

Alerts only fire once per low-stock episode — restocking above the reorder
level clears it, and a future dip sends a fresh alert — so staff aren't
spammed with a text on every single order while stock stays low.

If there's no cell signal either, SMS won't help — you'd need alternative
hardware (LoRa/Meshtastic radio, ham radio/APRS, or a satellite messenger)
or a manual/physical relay process, which this project doesn't currently
implement.

## Requirements to build

- Go 1.21+ installed
- A C compiler (gcc/clang) — needed because the SQLite driver (`mattn/go-sqlite3`) uses cgo.
  On Debian/Ubuntu: `sudo apt install build-essential libsqlite3-dev`
  On macOS: Xcode Command Line Tools (`xcode-select --install`)
  On Windows: install a MinGW-w64 toolchain (e.g. via MSYS2) and ensure `gcc` is on PATH

No internet is needed to build: all Go dependencies are already vendored in the
`vendor/` folder and pinned in `go.mod` / `go.sum`.

## Build

```bash
cd medistock
go build -mod=vendor -o medistock .
```

This produces a single `medistock` (or `medistock.exe` on Windows) binary.

## Run

```bash
./medistock
```

By default it creates/uses `medistock.db` in the current directory. To use a
different file (e.g. per-store databases), pass a path:

```bash
./medistock /path/to/mystore.db
```

The program opens on a welcome screen:

```
1. Customer - Order medicine (self-service kiosk)
2. Staff - Manage inventory & orders
3. Exit
```

Choosing **Customer** drops into the self-service kiosk (browse, search, add
to cart, checkout for a token number). Choosing **Staff** opens the
management menu:

```
1. Add medicine
2. List all medicines
3. Search medicine
4. Update stock quantity
5. Low stock report
6. Create new order (bill, staff-assisted)
7. List past orders
8. View order details
9. Back to main menu
```

## Project layout

```
main.go                    entry point — opens the DB and starts the CLI
internal/db/db.go          SQLite connection + schema migration (auto-creates tables)
internal/models/models.go  data structures: Medicine, Customer, Order, OrderItem
internal/repo/             database access layer (medicine.go, customer.go, order.go)
internal/cli/cli.go        interactive terminal menu / UI logic
vendor/                    vendored dependencies (for offline builds)
```

## How data integrity is handled

Placing an order runs inside a single SQL transaction: it checks stock,
deducts it, records the order and its line items, and computes the total
together. If anything fails partway (e.g. insufficient stock on one item),
the whole order is rolled back — nothing is partially saved.

## Extending it

- Add a `reports` command to export daily sales to CSV
- Add prescription/expiry-date validation before allowing a sale
- Swap the CLI for a local web UI later (the `internal/repo` layer is UI-agnostic,
  so a Go web server could reuse it directly)
