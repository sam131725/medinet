# Networking principles

MediStock touches four different kinds of network at various points, and
it's worth writing down the principle that ties them together, because it's
not obvious from looking at any one feature in isolation: **every layer
degrades to something that still works when the layer above it is gone.**
There's no single "the network" this system depends on — internet, WiFi,
and cell signal can each be present, degraded, or absent independently, and
MediStock is built to keep functioning through as many of those
combinations as it can.

```
Internet          — never required, never used, anywhere in this system
     |
Local WiFi/LAN    — web kiosk, staff page, mesh discovery between kiosks
     |
Cell signal (SMS) — customer ordering + distributor alerts when there's no LAN at all
     |
Nothing           — terminal/CLI mode on the machine itself, no network of any kind
```

Each layer down is a fallback for the layer above being unavailable, not a
separate feature bolted on. The sections below cover what actually happens
at each layer.

## Layer 0: no network at all

The terminal/CLI mode (default, no `-web` flag) needs nothing but the local
disk. This is the floor everything else is built on top of — if every other
form of networking is unavailable, staff can still run the machine
directly at the keyboard, and the SQLite/Postgres database is still the
single source of truth for stock and orders.

## Layer 1: local WiFi/LAN, no internet

`-web` serves a customer self-checkout page and a staff management page
over plain HTTP on the local network — a WiFi router or ad-hoc
hotspot with no internet uplink is enough, since every device just needs to
reach the kiosk machine's local IP, not the outside world. There's no CDN
dependency, no external font/script loading, nothing that would break with
the WAN link down (see the customer/staff page HTML in
`internal/webui/templates.go` - everything's inlined into one file).

**Mesh networking** (`-mesh`) lives at this same layer: kiosks discover
each other by broadcasting a small UDP packet on the LAN
(`internal/mesh/discovery.go`) and listening for the same from others, no
IP addresses to configure, similar in spirit to how a network printer
announces itself. This only works within one broadcast domain — it won't
find a kiosk on a different subnet or across a router boundary, and some
locked-down networks block UDP broadcast outright as a security measure.
`-mesh-peers` is the fallback for both cases: an explicit host:port list,
bypassing broadcast entirely. Either way, mesh traffic never leaves the
local network — there's no server anywhere it phones home to.

A deliberate trade-off worth naming: mesh discovery and the cross-kiosk
stock lookup have no authentication of their own beyond whatever the
staff PIN already protects on each kiosk's own API. The assumption is that
the LAN itself is a trusted boundary (a relief camp's own WiFi, not a
public network) — MediStock doesn't add its own crypto/auth layer on top,
so don't run `-mesh` on a network you don't otherwise trust.

## Layer 2: cell signal, no WiFi/LAN at all

When there's no local network to speak of — the realistic worst case in an
actual emergency — MediStock falls back to plain SMS, which only needs
cell tower signal, not mobile data or WiFi. This cuts both ways:

- **Customers without smartphones** can place orders by texting a short
  code (`ORDER PARA 2`) to whatever phone number the kiosk's SMS transport
  is reachable at (`-sms-ordering`). No app, no smartphone, no data plan.
- **The kiosk reaching a distributor** works the same way in reverse: when
  stock drops to its reorder level, it texts a reorder request rather than
  needing any kind of internet API call (`-distributor-phone`).

Two interchangeable transports carry this SMS traffic
(`internal/sms/transport.go`'s `Transport` interface): a dedicated GSM
modem over a serial AT-command connection, or a spare Android phone running
a small local HTTP gateway (`docs/phone-sms-gateway/`) that the kiosk talks
to over the *same local LAN* — notice this is layer 1 (LAN) carrying layer
2 (cellular) traffic on the phone's behalf, one layer using another rather
than replacing it. Either way, the only network hop that actually needs
cell tower coverage is the phone/modem's own SMS radio - everything else
(kiosk-to-modem, kiosk-to-gateway-phone) is local.

## Why no layer relies on the one above it

The core discipline is that nothing in a *lower* layer ever depends on a
*higher* one being present. SMS ordering doesn't assume WiFi exists. The
web kiosk doesn't assume the internet exists. Mesh doesn't assume the
internet exists either, and doesn't even assume WiFi has DHCP working
perfectly (the static-peer fallback sidesteps broadcast entirely). This is
what "offline-first" means concretely in this codebase, rather than as a
slogan: every feature was built by first asking which of these four layers
it can realistically expect to have, and never assuming a better one.

## What this doesn't do (and why that's a deliberate line, not an oversight)

- **No cross-kiosk sync beyond mesh's live stock lookup.** Two kiosks on
  the same mesh can see each other's current stock, but there's no shared
  order history, no eventual-consistency replication, no central database
  behind the scenes. Each kiosk's data is its own system of record. Adding
  real sync would mean either a WAN link (which the whole system is built
  to not need) or a much more involved offline-sync protocol (conflict
  resolution, vector clocks, the works) — out of scope unless a future
  requirement actually needs it. See "Scaling to more kiosks/locations" in
  the main README for what this means in practice.
- **No TLS on the local HTTP traffic** (web kiosk, mesh, phone gateway).
  On an isolated local network this is a reasonable trade for zero
  certificate-management overhead on hardware nobody's maintaining
  day-to-day; it would not be reasonable if any of this traffic ever
  touched a network you don't control.
