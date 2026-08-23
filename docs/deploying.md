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
