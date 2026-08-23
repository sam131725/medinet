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

## Why this exists

The gap this fills is specific: the moment right after a disaster —
earthquake, flood, cyclone — when broadband and mobile data are the first
things to go down, but people still urgently need medicine, and a pharmacy
or relief camp still has stock sitting on a shelf with no way to organize
who gets what. Paper logs work but don't stop hoarding, don't track stock
in real time, and don't scale past one line of people. This project's bet
is that a machine that keeps working through exactly that failure mode —
no internet, possibly no reliable power, staff who may not be trained on
it in advance — is worth more in that moment than a much more capable
system that needs connectivity it won't have.

A few design choices follow directly from that: the per-order quantity cap
exists because uncontrolled demand during a shortage is its own hazard, not
just an inconvenience — it's there to keep a limited supply reaching more
people rather than emptying to whoever's fastest. SMS ordering exists
because a smartphone and a data plan are real barriers to plenty of the
people this is meant to serve, and a feature phone with cell signal often
isn't. And every fallback (mesh's static-peer option, the phone-based SMS
gateway instead of dedicated modem hardware, the plain terminal mode with
no network at all) exists because "the ideal setup isn't available" should
degrade the experience, not break it.

None of this replaces a properly staffed, properly supplied relief
operation — it's a tool for the people running one, not a substitute for
them. What it can genuinely do is remove "we have no way to track this"
as a failure point during the exact window when everything else is
already strained.

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

## Multiple kiosks finding each other: mesh networking

Each kiosk is normally its own island — one machine, one local database, no
awareness of any other kiosk. `-mesh` changes that: kiosks on the same local
network automatically discover each other (no IP addresses to type in, the
same idea behind how a printer or Chromecast just shows up on WiFi), and
staff get a "which nearby kiosk has this in stock" lookup — useful when a
relief camp's kiosk runs dry on something a nearby one still has plenty of.
Still fully offline: this only ever talks over the local network these
machines already share, never the internet.

```bash
./medistock -web -mesh -mesh-name "Camp-A Pharmacy"
```

Every few seconds each kiosk broadcasts a small "I exist, here's my address"
packet over the local network (UDP broadcast) and listens for the same from
others, building a live peer list automatically — that's the zero-config
path. On the staff page, a "Nearby kiosks" panel lists everyone currently
discovered, and a medicine-code search asks every known peer whether they
have it in stock (over the same `/api/medicines` endpoint the peer's own
customer kiosk page already uses — no extra API needed on their end).

Some networks — locked-down enterprise WiFi, and some cloud/virtualized
network setups — block UDP broadcast between devices as a security measure.
For that case, `-mesh-peers` lets you list known kiosk addresses directly,
bypassing broadcast entirely:

```bash
./medistock -web -mesh -mesh-name "Camp-A Pharmacy" -mesh-peers "192.168.1.6:8080,192.168.1.7:8080"
```

**What's actually verified:** the peer registry, staleness/expiry, the
static-peer fallback, and the full cross-kiosk stock lookup are all covered
by automated tests (`go test ./internal/mesh/... ./internal/webui/...`),
including an end-to-end test running two real kiosk servers and confirming
one correctly finds stock on the other. I also ran this manually against
two real running instances of the actual binary and watched it work.
Genuine UDP *broadcast* auto-discovery, specifically, was built and tested
against real sockets, but the cloud environment this was developed in
silently drops broadcast traffic (a documented limitation of most
cloud/container networks, confirmed here by checking that plain unicast UDP
works fine in the same environment — it's not a bug in the code). On a real
WiFi hotspot or office LAN, broadcast works normally; the automated test for
it skips (rather than fails) when the network doesn't cooperate, and
`-mesh-peers` is there as a guaranteed-to-work fallback either way.

## Reaching a distributor with no internet: SMS reorder alerts

Beyond mesh (which only lets kiosks find each other), when a medicine's
stock drops to or below its reorder level, it can automatically text a
distributor/retailer a reorder request over SMS (via either transport
above), rather than mobile data. This matters because in many outages
phone/cell signal survives even when internet and data connectivity are
down — SMS travels over the carrier's signalling channel, not the data
network.

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

## Networking principles

The features above (web kiosk, mesh, SMS ordering, SMS alerts) each touch a
different kind of network, and the short version of how they fit together
is: every layer degrades to something that still works when the layer
above it is gone — nothing here ever assumes the internet exists, the web
kiosk and mesh don't assume WiFi/LAN is reliable beyond broadcast working,
and SMS doesn't assume anything but bare cell tower signal. `docs/networking.md`
writes this out in full — what runs at each layer, why mesh has no
authentication of its own beyond the staff PIN (it assumes a trusted LAN,
not a public network), and what this deliberately doesn't do (no
cross-kiosk data sync beyond mesh's live stock lookup, no TLS on local
traffic) and why those are scoping decisions, not oversights.

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

## Choosing a database: SQLite (default) or Postgres

By default MediStock stores everything in a single local SQLite file
(`medistock.db`) — no database server to install or run, which is why it's
the default for a kiosk that just needs to work. If you'd rather run a
proper Postgres server locally (still fully offline — this only ever talks
to a host on your own machine or local network, never the internet),
`-db-driver postgres` switches to it:

```bash
./medistock -web -db-driver postgres \
  -pg-host localhost -pg-port 5432 \
  -pg-user postgres -pg-password yourpassword -pg-dbname medistock
```

The database (`medistock` by default) must already exist on the Postgres
server — MediStock creates its tables inside it on first run, but won't
create the database itself. `-pg-sslmode` defaults to `disable`, which is
normal for a local instance with no certificate; set it if your setup needs
otherwise.

Everything works the same either way — same CLI, same web kiosk, same SMS
ordering, same mesh networking. The one difference: scheduled backups
(`-backup-interval`) use SQLite's `VACUUM INTO` for a SQLite database, or
shell out to `pg_dump` for Postgres (install the `postgresql-client`
package to get it — `sudo apt install postgresql-client` on Debian/Ubuntu).

**What's verified:** every repo-layer operation (adding medicines,
generating unique SMS codes, placing orders inside a transaction that
rolls back on insufficient stock, case-insensitive code lookups) is covered
by tests that run for real against a local Postgres server
(`internal/repo/postgres_test.go` — they skip, rather than fail, if no
Postgres is reachable, and CI runs a real Postgres service so they aren't
just skipped there either). I also ran the actual compiled binary as a web
kiosk against a real local Postgres database end-to-end: added a medicine,
placed an order through the customer checkout API, and confirmed via `psql`
that the stock deduction, order, and order line item all landed correctly
— and confirmed a real `pg_dump` backup file was produced and readable.

## Deploying

Three ways to actually run this on a real machine, from simplest to most
involved: copy the binary and run it directly (with a systemd service so it
auto-restarts), run it with Docker/`docker-compose.yml`, or a bare restart
loop for hardware that can't run either. Full walkthrough, including the
mesh-networking-vs-Docker-bridge-networking caveat and an honest note on
what was and wasn't possible to test end-to-end in this project's own
development environment, is in `docs/deploying.md`.

## Scaling to more kiosks/locations

MediStock scales the way an offline-first system has to: horizontally, by
running more independent copies, not by adding capacity to one central
server (there isn't one). A few different things "scale" can mean here:

- **A single kiosk under heavier local load** — more staff/customers hitting
  it at once. SQLite defaults to a single writer connection
  (`internal/db/db.go`), which is intentionally conservative for a small
  kiosk; if one location genuinely needs more concurrent throughput than
  that comfortably handles, `-db-driver postgres` removes that
  single-writer constraint (Postgres handles concurrent writes natively) at
  the cost of needing a database server running on that machine. This is
  a per-kiosk decision, not something that needs coordinating across sites.
- **Rolling out to more locations** — this is the common case, and it's the
  easy one: every kiosk is fully self-contained (own database, own binary,
  own config), so adding a tenth or hundredth kiosk is "install and run it
  there too," not a change to any existing kiosk. There's no shared
  bottleneck to hit, because there's no shared component at all. Docker
  makes this repeatable (the same image, deployed the same way, at every
  site) but even without it, "copy the binary, set the flags for this
  site" scales fine for as many locations as there are people willing to
  set up a machine.
- **Kiosks at the same location coordinating** — this is what mesh
  networking already does, and it's the ceiling of what this project
  currently offers for cross-kiosk awareness: kiosks on the same LAN see
  each other's live stock. There's deliberately no attempt at scaling that
  into cross-*site* coordination (a camp's kiosks talking to a different
  camp's kiosks, or a head-office dashboard aggregating all of them) - that
  would need either an internet link (which this system is built to not
  require) or a genuine offline-sync protocol, and neither is in scope
  unless a real future requirement calls for it. See
  `docs/networking.md`'s "What this doesn't do" section for the reasoning.

In short: the honest scaling story is "add more independent kiosks," which
this architecture already supports for free, rather than "make one kiosk
bigger," which was never really the design goal for hardware that's meant
to be cheap, replaceable, and running unattended in the field.

## Project layout

```
main.go                       entry point — wires everything together, handles flags & shutdown
internal/db/                  SQLite/Postgres connection + schema migration (both engines)
internal/mesh/                multi-kiosk peer discovery (UDP broadcast) + cross-kiosk stock lookup
internal/models/models.go     data structures: Medicine, Customer, Order, OrderItem
internal/repo/                database access layer (medicine.go, customer.go, order.go)
internal/cli/                 interactive terminal menu + kiosk flow
internal/webui/               local web kiosk (customer + staff pages, HTTP API)
internal/sms/                 SMS transports: GSM modem (AT commands) and phone HTTP gateway
internal/smsorder/            SMS order-parsing (LIST/ORDER/HELP commands)
internal/alerts/              low-stock -> distributor SMS notifications
internal/backup/              periodic database backups (VACUUM INTO / pg_dump)
internal/applog/              shared structured (JSON) logger
docs/phone-sms-gateway/       reference Termux script for the no-modem SMS option
docs/deploying.md             how to actually run this on real hardware
docs/networking.md            how the networking layers (LAN, mesh, SMS) fit together
Dockerfile, docker-compose.yml container-based deployment (see docs/deploying.md)
vendor/                       vendored dependencies (for offline builds)
```

## How data integrity is handled

Placing an order runs inside a single SQL transaction: it checks stock,
deducts it, records the order and its line items, and computes the total
together. If anything fails partway (e.g. insufficient stock on one item),
the whole order is rolled back — nothing is partially saved.

## Production-readiness notes

This started as a prototype and has since had several rounds of hardening.
Where it stands:

**Tested.** `go test ./...` covers the database/order transaction layer
(including the rollback-on-insufficient-stock case and the anonymous-order
foreign-key regression this project actually hit once), the SMS
order-parsing logic, the HTTP phone-gateway client, database backup/restore,
and staff auth rate-limiting — all against real SQLite databases and real
HTTP servers, not mocks; the same transaction/rollback/unique-code behavior
is also covered against a real Postgres server (see "Choosing a database"
above). `.github/workflows/ci.yml` runs the full suite (build, vet, gofmt
check, `go test -race`), including the Postgres tests against a real
Postgres service container, on every push/PR.

**Logging.** Every component logs through `internal/applog` as structured
JSON (`log/slog`) to stderr and optionally a file (`-log-file`) — request
logs for the web kiosk, SMS transport errors, backup results, auth
failures/lockouts. This is what you'd actually look at after the fact on an
unattended kiosk.

**Backups.** The database is backed up automatically on startup and on a
schedule (`-backup-dir`, `-backup-interval`, `-backup-keep`) — using
SQLite's `VACUUM INTO` for a SQLite database, or `pg_dump` for Postgres —
which produces a consistent snapshot without needing to stop the app. This
is the single most important safety net for a system whose entire state
lives in one local file or one local database server.

**Staff auth.** Still a shared PIN, not per-user accounts — but it's now
rate-limited per IP (5 failed attempts locks that IP out for 5 minutes),
closing the "brute-force a 4-digit PIN in seconds" hole. Real accounts with
per-user audit logging would be the next step if that matters for your
deployment.

**Graceful shutdown.** `Ctrl+C`/`SIGTERM` stops the web server cleanly
(finishing in-flight requests, up to 10s) and stops the backup/SMS-polling
goroutines instead of dying mid-request.

**Still open, deliberately out of scope for now** (per an explicit choice
not to build deployment-specific tooling until a target machine/hardware is
picked): no Dockerfile or systemd unit, no HTTPS/TLS on the local web kiosk
(fine on an isolated offline hotspot, not fine if that assumption ever
breaks), and — as covered above — the SMS transports are verified against
simulated modems/gateways, not real physical hardware yet.

## Future implementation

Ideas that are deliberately not built yet — either because nothing in this
project needed them, or because they only make sense once a real
deployment surfaces the actual requirement rather than a guessed one:

**Reporting and accountability**
- A `reports` command to export daily sales/dispensing logs to CSV, for
  whatever paperwork a relief operation or pharmacy has to file afterward.
- Real per-user staff accounts instead of a single shared PIN, with an
  audit log of who adjusted stock or completed which order — the shared
  PIN (see "Production-readiness notes" above) is fine for a small team
  but doesn't scale to accountability across a larger one.
- Prescription/expiry-date validation before allowing a sale, for
  medicines where that matters more than emergency throughput does.

**Reaching more people**
- Vernacular-language SMS commands — right now `ORDER PARA 2` only
  understands English keywords; the people this is meant to serve don't
  all read English, and that's a real access gap worth closing.
- Offline text-to-speech or a simpler, larger-text kiosk mode for elderly
  users or anyone less comfortable with a touchscreen.

**When there's truly no cell signal either**
- Support for LoRa/Meshtastic radio, ham radio/APRS, or a satellite
  messenger as an alternative to SMS, for the scenario the "Reaching a
  distributor" section already flags as currently unhandled: cell towers
  themselves down, not just data.

**Once a real multi-site deployment exists**
- Opportunistic sync between sites when *any* connectivity briefly
  reappears (a staff phone with signal, a weekly supply-run to a location
  with WiFi) — batching stock/order deltas rather than requiring the
  always-on link a cloud sync would need. This is real design work
  (conflict resolution between two kiosks' concurrent edits, at minimum)
  and is intentionally not something this project has guessed at without
  a concrete deployment to design it against — see `docs/networking.md`'s
  "What this doesn't do" for why that line is drawn where it is today.
- A pre-built, flashable image for cheap deployment hardware (a Raspberry
  Pi SD card image, for instance) so a new kiosk is "flash a card, plug
  it in" rather than "build from source on-site."

None of this is a commitment or a roadmap with dates — it's the honest
list of "here's what would extend this, and roughly why it isn't built
yet," kept here so it doesn't get reinvented from scratch by whoever picks
it up next (including a future me).
