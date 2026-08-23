# Deploying MediStock

There's no "the" deployment target here — a kiosk is meant to run on
whatever machine ends up on-site (a mini-PC, an old laptop, a Raspberry Pi,
a rack server for a bigger location), and it needs to keep running
unattended without anyone SSHing in to restart it after a crash or a power
cut. This doc covers the three realistic ways to run it, in order of how
much infrastructure they assume.

## Option 1: just the binary

The simplest path, and the right one if you're not sure yet what hardware
you'll end up on. Build it (see the main README's "Build" section), copy
the single `medistock` binary (plus the `docs/` folder if you'll use the
phone SMS gateway) to the target machine, and run it. No Docker, no
dependencies beyond a C library that's already on essentially every Linux
distro (SQLite mode) or a locally installed Postgres (Postgres mode).

To survive a reboot or a crash without manual intervention, wrap it in a
systemd service (Linux) — create `/etc/systemd/system/medistock.service`:

```ini
[Unit]
Description=MediStock offline medicine ordering kiosk
After=network.target

[Service]
ExecStart=/opt/medistock/medistock -web -port 8080 -db /opt/medistock/data/medistock.db -backup-dir /opt/medistock/data/backups -staff-pin CHANGE_ME
WorkingDirectory=/opt/medistock
Restart=always
RestartSec=5
User=medistock

[Install]
WantedBy=multi-user.target
```

Then:

```bash
sudo useradd -r -s /usr/sbin/nologin medistock
sudo mkdir -p /opt/medistock/data && sudo chown -R medistock: /opt/medistock
sudo cp medistock /opt/medistock/
sudo systemctl daemon-reload
sudo systemctl enable --now medistock
```

`Restart=always` is the important part — if the process dies for any
reason, systemd brings it back within 5 seconds, no one needs to notice.

### Concretely: on a real Raspberry Pi

This is Option 1 above, worked through end to end for a Raspberry Pi 4 or
5 (2GB RAM or more is plenty), since it's the cheapest, lowest-power,
easiest-to-source hardware that fits this project well.

**1. Flash the OS.** Use the [Raspberry Pi
Imager](https://www.raspberrypi.com/software/) on your own computer.
Choose **Raspberry Pi OS Lite (64-bit)** — no desktop environment needed,
this only ever runs headless. Before writing, open the imager's advanced
options (gear icon) and set a hostname, enable SSH, and enter your WiFi
details — this lets you set the Pi up completely headless, no monitor or
keyboard needed for it ever.

**2. Get the binary onto the Pi.** Two ways:

- *Build it on the Pi itself* (simplest, needs the Pi to have internet for
  this one-time step — normal, since it's still being provisioned, not yet
  deployed to the field):
  ```bash
  ssh pi@<pi's address>
  sudo apt update && sudo apt install -y golang build-essential git
  git clone https://github.com/sam131725/medinet.git medistock
  cd medistock
  go build -mod=vendor -o medistock .
  ```
- *Cross-compile elsewhere and copy it over* (faster, and means the Pi
  never needs internet or a Go toolchain at all — useful if you're setting
  up several Pis). From a Linux machine with a cross-compiler
  (`sudo apt install gcc-aarch64-linux-gnu`):
  ```bash
  CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc \
    go build -mod=vendor -ldflags="-s -w" -o medistock-linux-arm64 .
  scp medistock-linux-arm64 pi@<pi's address>:~/medistock
  ```
  This cross-compilation path was actually run and verified — the
  resulting `arm64` binary was executed under `qemu-aarch64` emulation
  (which runs real ARM64 machine code, not a simulation of Pi-specific
  behavior) and confirmed to serve the web kiosk and handle real SQLite
  reads/writes correctly. That's strong evidence it'll run correctly on
  real Pi hardware, though emulation isn't a perfect substitute for the
  real thing — if you go this route, still worth doing a quick smoke test
  after copying it over.

**3. Set it up as a systemd service** — follow the systemd steps earlier
in this section (`/etc/systemd/system/medistock.service`), pointing
`ExecStart` at wherever you put the binary (e.g. `/home/pi/medistock`).
Once `systemctl enable --now medistock` is run, it starts on every boot
and restarts itself if it ever crashes — exactly the "plug it in and
forget it" behavior a kiosk needs.

**4. Reach it from other devices.** Once running, find the Pi's local IP
(`hostname -I` on the Pi, or check your router's connected-devices list)
and open `http://<that IP>:8080` from any phone/tablet on the same WiFi —
that's the customer kiosk. `/staff` is the staff page. If you want the Pi
itself to also be the WiFi hotspot (rather than joining an existing
network — useful if there's no router at all on-site), set it up as a
WiFi access point using Raspberry Pi OS's built-in `nmcli`/`raspi-config`
tools; that's a standard Pi networking task independent of this project,
well covered by Raspberry Pi's own documentation.

## Option 2: Docker

Useful if you're comfortable with containers, want the same reproducible
image across every site regardless of what Linux distro is on the box, or
plan to manage several kiosks' deployments centrally (even though each
kiosk still runs and stores data fully independently — see
[Scaling](#see-also) in the main README).

```bash
docker compose up -d --build
```

This builds the image from the `Dockerfile` (a multi-stage build — the
compiled binary ends up in a small `debian:bookworm-slim` runtime image,
nothing else), starts it with SQLite storage in a named Docker volume (so
data survives `docker compose down` / image rebuilds), and publishes port
8080. Open `http://<host>:8080` for the customer kiosk and
`http://<host>:8080/staff` for staff.

For Postgres instead of SQLite:

```bash
docker compose --profile postgres up -d --build
```

(then edit the `medistock` service's `command:` in `docker-compose.yml` to
the commented-out Postgres variant — see the comments in that file).

**Important — mesh networking and Docker:** `-mesh` broadcasts and listens
on UDP over the LAN. Docker's default bridge network gives the container
its own private IP, not your host's real network address, so mesh
discovery won't see other kiosks on your WiFi from inside the default
bridge network. If you want `-mesh` to work, run with host networking
(Linux only — uncomment `network_mode: host` in `docker-compose.yml`, and
drop the `ports:` mapping since host networking exposes the port directly).
On Docker Desktop for Mac/Windows, host networking isn't available the same
way — run the binary directly (Option 1) on those machines if you need
mesh discovery. `-mesh-peers` (the static-peer fallback, see the main
README) works fine over the Docker bridge network as long as you point it
at the right published ports.

**What's verified:** the underlying `go build -mod=vendor` step the
Dockerfile runs (compiling with cgo enabled, exactly as the Dockerfile's
`RUN` line does) was run and confirmed working directly in the environment
this was built in, and `docker compose config` was used to validate the
compose file parses and resolves correctly. The actual `docker build`
could **not** be run end-to-end in that environment, because it has no
access to any container registry (Docker Hub, GHCR, GCR, etc. were all
unreachable there — the same kind of network restriction documented
elsewhere in this project for the Go module proxy). The Dockerfile follows
a standard, well-established pattern (multi-stage build, vendored
dependencies so it never needs network access during the build, a slim
glibc-based runtime image matching the SQLite driver's dynamic linking) —
but you should be the first to actually run `docker build` on it. If
something doesn't work, the likely trouble spots are worth checking first:
the Go/Debian base image tags in the `FROM` lines (pin them to whatever's
current if `1.21-bookworm` / `bookworm-slim` have aged out), and confirming
`libsqlite3`'s runtime dependency is satisfied (it should be, via glibc in
the Debian base — Alpine/musl bases will NOT work without switching to a
static/musl SQLite build, which this project doesn't set up).

## Option 3: bare metal with no OS-level auto-restart

If the target machine can't run systemd or Docker (some embedded/POS
hardware), the minimum viable version is a tiny restart loop:

```bash
while true; do ./medistock -web -port 8080; sleep 2; done
```

Crude, but sufficient — the goal is only "if it dies, it comes back," which
this achieves. Prefer Option 1 or 2 if the hardware supports it.

## See also

- Main README's "Choosing a database" section for SQLite vs. Postgres.
- Main README's "Multiple kiosks finding each other: mesh networking"
  section, and "Scaling to more kiosks/locations" for how this fits
  together when you're not deploying just one kiosk.
- `docs/networking.md` for how all of this project's networking pieces
  (SMS/cellular, local WiFi web kiosk, mesh) fit together and why.
