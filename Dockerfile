# Multi-stage build so the final image doesn't carry a Go toolchain or
# build tools - just the compiled binary and its runtime dependencies.
#
# CGO is required (the SQLite driver, mattn/go-sqlite3, uses cgo), so the
# build stage needs a C compiler even though the final image doesn't.

FROM golang:1.21-bookworm AS build
WORKDIR /src

# All Go dependencies are vendored, so this build never touches the
# network - it works the same offline as building directly on a host.
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY . .

RUN CGO_ENABLED=1 GOFLAGS=-mod=vendor go build -o /out/medistock .

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates postgresql-client \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /out/medistock /app/medistock

# Data (SQLite file + backups) lives here so it survives container
# restarts/upgrades when this path is mounted as a volume.
VOLUME ["/data"]
ENV MEDISTOCK_DB=/data/medistock.db
ENV MEDISTOCK_BACKUP_DIR=/data/backups

EXPOSE 8080

# Runs the web kiosk by default. Override the command (or pass extra
# flags after --) to run the SMS/mesh-enabled configuration your site
# needs - see docs/deploying.md.
ENTRYPOINT ["/app/medistock"]
CMD ["-web", "-port", "8080", "-db", "/data/medistock.db", "-backup-dir", "/data/backups"]
