# syntax=docker/dockerfile:1

# --- build stage --------------------------------------------------------
# CGO_ENABLED=0 is viable because modernc.org/sqlite is pure Go — no C
# toolchain needed here, keeping this stage (and its cache) small.
FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/leafmark ./cmd/leafmark

# --- runtime stage -------------------------------------------------------
# Deliberately NOT scratch/distroless, and deliberately NOT
# chromedp/headless-shell: automated Goodreads re-login (see
# internal/goodreads/login.go) needs the login page's own JS to compute
# Amazon's client-side password encryption and device-fingerprint fields
# — reproducing those over raw HTTP was evaluated and ruled out (see the
# project plan) — but it needs a genuinely *headed* Chrome to get there:
# AWS WAF Bot Control's JS challenge blocks Chrome's --headless mode from
# ever reaching Goodreads' real sign-in page (confirmed by testing; see
# internal/goodreads/login.go's allocator-options comment). The fix is a
# real headed Chromium running under Xvfb (a virtual X11 framebuffer) —
# Chrome never takes the --headless code path, while the container still
# has no visible window and needs no real display. Debian's `chromium`
# package is used instead of Google's official .deb because the latter
# only ships amd64 builds; Debian's is multi-arch (works on arm64 hosts
# too, e.g. Apple Silicon Docker Desktop).
FROM debian:bookworm-slim

# tini reaps zombie processes Chrome tends to leave behind; xvfb (just
# the Xvfb binary — docker-entrypoint.sh starts/execs around it directly,
# not via the xvfb-run wrapper script, which doesn't forward signals to
# its target process; see that script's own comment).
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        tini ca-certificates \
        chromium xvfb \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# Fixed, deliberately non-1000 UID:GID (avoids colliding with a host
# user's own UID when bind-mounting volumes) — chown your DB_PATH
# directory and any LEAFMARK_ENV_FILE secrets file to this before mounting.
RUN groupadd --gid 52416 leafmark \
    && useradd --uid 52416 --gid leafmark --no-create-home --shell /usr/sbin/nologin leafmark \
    && mkdir -p /data \
    && chown -R leafmark:leafmark /data

# X servers refuse to create /tmp/.X11-unix themselves unless running as
# root ("euid != 0"), so it has to pre-exist with the standard 1777 perms
# before Xvfb ever runs as the non-root leafmark user below.
RUN mkdir -p /tmp/.X11-unix && chmod 1777 /tmp/.X11-unix

COPY --from=build /out/leafmark /usr/local/bin/leafmark
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

USER 52416:52416
WORKDIR /data
VOLUME ["/data"]

# CMD is passed to docker-entrypoint.sh as "$@", which starts Xvfb and
# then execs `leafmark "$@"` — so `docker run leafmark --once --dry-run`
# works directly (those args replace CMD, still routed through the
# entrypoint script) without needing to know Xvfb is even involved. CMD
# is empty by default: no flags means the long-running daemon mode.
ENTRYPOINT ["tini", "--", "/usr/local/bin/docker-entrypoint.sh"]
CMD []
