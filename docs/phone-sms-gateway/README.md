# Phone-based SMS gateway (alternative to a GSM modem)

If you don't have (or don't want to buy) a dedicated USB GSM modem, an
ordinary spare Android phone with a SIM card and signal can do the same job.
This is often easier to get hold of in an emergency than modem hardware,
and just as offline - it only needs cellular signal, never mobile
data/internet.

## How it fits together

```
customer's phone --SMS--> [gateway phone, running gateway.py] <--local WiFi--> medistock kiosk
```

The gateway phone runs `gateway.py` (in this folder) via Termux, which
exposes a tiny local HTTP API. medistock talks to it over your offline
WiFi/hotspot instead of a serial port. Point medistock at it with:

```bash
./medistock -sms-ordering -sms-gateway-url http://<phone-ip>:8090 -distributor-phone "+91..."
```

## Setting up the gateway phone

1. Install **Termux** and **Termux:API** from F-Droid (not the Play Store
   versions, which are outdated and no longer compatible). Both are free.
2. Open Termux and run:
   ```bash
   pkg update && pkg install python termux-api
   ```
3. Grant the Termux:API app SMS permission when Android prompts for it
   (Settings > Apps > Termux:API > Permissions > SMS, if it doesn't prompt
   automatically).
4. Copy `gateway.py` from this folder onto the phone (e.g. via
   `termux-setup-storage` + copying to `/sdcard/`, or `scp`/a USB cable),
   then run it:
   ```bash
   python gateway.py 8090 optional-secret-token
   ```
5. Find the phone's local IP address (Settings > WiFi > tap the connected
   network > IP address) and use that in `-sms-gateway-url`.
6. Keep the phone charged (ideally on a charger continuously) and Termux
   running - run `termux-wake-lock` first so Android doesn't suspend it in
   the background, and keep the Termux app open/foregrounded if possible.

## Verifying it works before relying on it

From another device on the same network, or from the medistock machine:

```bash
curl http://<phone-ip>:8090/inbox/unread
curl -X POST http://<phone-ip>:8090/send -H 'Content-Type: application/json' \
  -d '{"to":"+91XXXXXXXXXX","message":"test"}'
```

Send a real text to the gateway phone's number from another phone, then
check `/inbox/unread` picks it up.

## Honesty check

This script is written carefully against the documented `termux-sms-send`
and `termux-sms-list` command-line tools, and the HTTP side of it (the
contract medistock's Go client speaks) has been tested end-to-end against a
stand-in for these commands. What hasn't been tested is the actual
Termux:API integration on a real Android device, since this environment has
no Android hardware to test against. Termux:API's exact command flags can
drift between versions - before trusting this in an actual emergency, run
`termux-sms-send --help` and `termux-sms-list --help` on your phone and
confirm they match what `gateway.py` expects, and do one real send/receive
test.

## Security note

This gateway has no built-in encryption (plain HTTP) and only optional
token auth - it's designed to run on a local, physically-controlled offline
network (your own hotspot), not the open internet. Don't expose it beyond
that network.
