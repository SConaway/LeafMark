# LeafMark

A small self-hosted bridge: it watches a [koreader-sync](https://github.com/nperez0111/koreader-sync)
server for reading-progress updates and pushes them to Goodreads, so
"currently reading" progress on Goodreads stays in sync with what's
actually being read on an e-reader, without manual updates.

Single Go binary, single SQLite file, one Docker container. Binds to a
Tailscale-only address behind an existing reverse proxy — **there is no
application-level authentication**; network-level trust (Tailscale) is the
security boundary.

## Why this exists

Goodreads killed its official developer API in Dec 2020 and never issued
new keys. KOReader's native "Goodreads" plugin depended on that dead API
and never supported writing progress in the first place, only browsing.
There's no existing open-source tool that bridges KOReader progress sync to
Goodreads — this is new, not a reimplementation.

It works by treating Goodreads as a logged-in web app: LeafMark drives a
real headless Chromium through Goodreads' own sign-in form (see
[Automated Goodreads login](#automated-goodreads-login) below) and then
replicates the same internal request Goodreads' frontend makes to update
reading progress. This is inherently scraping-adjacent against an
undocumented, unversioned surface, and will break if Goodreads changes
internal endpoints — an accepted tradeoff for a personal tool. Build for
graceful, loud failure, not resilience against Goodreads changing things.

## How it works

1. **Poll** the koreader-sync server for the most recently updated document
   (`GET /syncs/documents`, first entry — the server already sorts by most
   recent).
2. **Diff** against `sync_state` — if the percentage hasn't changed since
   the last successful push, do nothing.
3. **Resolve identity** via `book_mappings`.
   - Mapped → push straight to Goodreads using the cached book ID.
   - Not mapped → fuzzy-match the document's title/author against your
     **want-to-read** and **currently-reading** shelves only (not the full
     Goodreads catalog — this is what makes a strict auto-confirm threshold
     usable).
     - Clears the threshold → auto-confirm and push.
     - Doesn't → create a pending match, send an ntfy notification with
       near-miss candidates as tap-to-confirm action buttons, push nothing
       this cycle.
4. A human resolves a pending match later (ntfy button tap, or the WebUI at
   `/pending`) → `POST /confirm` → mapping is written and the
   originally-pending percentage is pushed immediately.

## Setup

### Requirements

- Go 1.26+ (for local dev)
- A running [koreader-sync](https://github.com/nperez0111/koreader-sync)
  server, with your KOReader device already registered/syncing to it
- A Goodreads account with **public** want-to-read/currently-reading
  shelves (Goodreads → Settings → the shelf-fetch is an unauthenticated
  public RSS feed — see [Design notes](#design-notes))
- An [ntfy](https://ntfy.sh) topic (hosted or self-hosted)
- Docker, for the packaged deployment (see below)

### Local development

`go run ./cmd/leafmark` alone isn't enough to test a real poll cycle
anymore: automated Goodreads login needs a genuinely headed Chromium
running under Xvfb (see [Automated Goodreads
login](#automated-goodreads-login)), so local testing now goes through
Docker instead — `docker run` gives you the same fast inner loop `go
run` used to.

```sh
cp .env.example .env   # fill in real values
docker build -t leafmark .
docker run --rm --shm-size=1gb -p 8080:8080 \
  -v "$(pwd)/.env:/run/secrets/leafmark.env:ro" \
  -e LEAFMARK_ENV_FILE=/run/secrets/leafmark.env \
  -e DB_PATH=/tmp/leafmark.db \
  leafmark --once --dry-run
```

Two flags (appended after the image name, as above) help with testing
before you trust it with your real account:

- `--dry-run` — poll and match as usual, but log instead of actually
  pushing to Goodreads or publishing to ntfy. Skips the startup Goodreads
  login entirely (nothing that follows needs a real session), so this
  works even before you've set real `GOODREADS_*` credentials.
- `--once` — run a single poll cycle and exit, instead of starting the
  HTTP server and looping on `POLL_INTERVAL`. Useful for cron-style
  invocation or a quick end-to-end check.

They compose (`--once --dry-run` runs one full poll → match cycle with
zero side effects, just logging what would have happened) and rebuilding
is just `docker build -t leafmark .` again — see [Docker](#docker) below
for the full breakdown of that `docker run` command's flags, plus the
persistent-deployment (`docker compose`) path. Rebuild the image after
any code change; there's no live-reload.

### Tests

```sh
go test ./...
```

Everything except `internal/goodreads/login.go` (the chromedp-driven
Goodreads login) is unit-tested against mocks/`httptest` with zero live
external dependencies. `login.go` is inherently an integration point with
live Amazon/Goodreads infrastructure and isn't meaningfully mockable —
verify it by actually running the binary against your real credentials.

### Docker

Build the image:

```sh
docker build -t leafmark .
```

**One-off test run**, no daemon and no compose file — this is the fastest
way to `docker run` a single poll cycle against your real `.env` without
committing to the long-running service:

```sh
docker run --rm --shm-size=1gb -p 8080:8080 \
  -v "$(pwd)/.env:/run/secrets/leafmark.env:ro" \
  -e LEAFMARK_ENV_FILE=/run/secrets/leafmark.env \
  -e DB_PATH=/tmp/leafmark.db \
  leafmark --once --dry-run
```

Notes on that command:

- `--shm-size=1gb` — Docker's default 64MB `/dev/shm` is too small for
  Chromium and causes intermittent crashes.
- Automated Goodreads login (see [Design
  notes](#automated-goodreads-login)) drives a real, genuinely *headed*
  Chromium — headless Chrome gets blocked by Goodreads' bot-detection —
  so the container starts Xvfb (a virtual display) and execs into
  `leafmark` underneath it via `docker-entrypoint.sh`, the image's fixed
  `ENTRYPOINT`. `docker run <image> <args...>` only replaces `CMD`
  (routed through that same entrypoint), so `--once`/`--dry-run` above
  can just be passed directly — no need to know Xvfb is even involved.
- Drop `--dry-run` to exercise the real Goodreads login end-to-end
  (harmless on its own — it only mints a session cookie) plus a real
  match/push if a confirmed mapping and changed progress exist. Drop
  `--once` too to run it as a foreground long-poll loop instead of a
  single cycle.
- If your host user's `.env` isn't world-readable, the container (which
  runs as non-root UID `52416`) won't be able to read the bind mount;
  either loosen its permissions or add `--user 0:0` for this throwaway
  container.

**Persistent deployment**, via `docker-compose.yml`:

```sh
mkdir -p data
cp .env.example secrets.env   # fill in real values; chown to 52416:52416 (see below)
docker compose up -d --build
```

See the comments in `docker-compose.yml` for the Tailscale-binding options
(this varies by homelab setup and is deliberately not assumed for you).

## Configuration

All config is via environment variables, optionally layered with a mounted
dotenv-format file (`LEAFMARK_ENV_FILE`) for secrets delivery — see
[Secrets delivery](#secrets-delivery) below. Every required variable
missing or invalid is reported together at startup, not one at a time.

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `GOODREADS_USER` | yes | — | Goodreads login email |
| `GOODREADS_PASSWORD` | yes | — | Goodreads login password |
| `GOODREADS_USER_ID` | yes | — | Numeric Goodreads user ID (from your profile URL) |
| `KOREADER_SYNC_URL` | yes | — | Base URL of the koreader-sync server |
| `KOREADER_SYNC_USERNAME` | yes | — | koreader-sync account username |
| `KOREADER_SYNC_PASSWORD` | yes | — | koreader-sync account password |
| `NTFY_URL` | yes | — | Full ntfy topic URL |
| `LEAFMARK_BASE_URL` | yes | — | Public HTTPS base URL LeafMark is reachable at (must be `https://`) |
| `DB_PATH` | yes | — | Path to the SQLite file |
| `POLL_INTERVAL` | no | `5m` | How often to poll koreader-sync |
| `MATCH_THRESHOLD` | no | `0.8` | Fuzzy-match auto-confirm cutoff, 0.0–1.0 |
| `LISTEN_ADDR` | no | `:8080` | Address for the WebUI/confirm HTTP server |
| `LEAFMARK_ENV_FILE` | no | — | Path to a mounted dotenv file providing any of the above |

## Design notes

A few things the original design doc left open, resolved by reading the
actual systems involved rather than guessing:

### Goodreads progress push

Confirmed via a captured browser request: `POST /user_status.json`,
form-encoded `user_status[book_id/body/percent]`, requiring an
`X-CSRF-Token` header (scraped from a `<meta name="csrf-token">` tag on an
authenticated page, cached, re-scraped on rejection) **and** the full raw
`Cookie:` header — not a single "session cookie" value.

### Goodreads shelf fetch

Needs **no authentication at all** — it's Goodreads' public
`review/list_rss` feed, confirmed by reading
[piratereads](https://github.com/mariannefeng/piratereads)'s source. This
only works if your want-to-read/currently-reading shelves are set to
public; a private shelf fails (empty/404) rather than erroring loudly, so
this is worth double-checking directly if matches mysteriously never
happen.

### KOReader contract

Specific to [gh:nperez0111/koreader-sync](https://github.com/nperez0111/koreader-sync)
(confirmed by reading its source), not KOReader's general Progress-sync
protocol: per-request `X-Auth-User`/`X-Auth-Key` headers, and
`GET /syncs/documents` returns every synced document sorted by most recent
first — so "the last-synced document" is just the first entry, no need to
track a set of known document hashes client-side. Progress is stored there
as a `0.0–1.0` fraction and converted to a rounded integer percent.

### Automated Goodreads login

The write-path session cookie expires roughly every 24h. Rather than
requiring a manual devtools recapture on that cadence, LeafMark drives a
real headless Chromium (`chromedp`) through Goodreads' sign-in form,
triggered at startup and again whenever a request comes back
session-invalid. This was evaluated against a captured real login flow
before committing to it: the login POST includes a client-side-encrypted
password and a device-fingerprint blob, both computed by Amazon's page JS —
reproducing that over raw HTTP isn't practical, but a real browser executes
the actual JS and produces valid values naturally. No CAPTCHA/2FA step
appeared in the captured flow, but that's not guaranteed to always hold;
a re-login failure surfaces through the same loud-failure ntfy path as any
other Goodreads error, throttled to one alert per failure streak rather
than one per poll interval.

This is a deliberate departure from "no automated login" as a design
default for scraping-adjacent personal tools — accepted here because the
alternative (near-daily manual cookie recapture) defeats the point of an
unattended bridge.

### Matching

Token-set similarity (Sørensen–Dice coefficient over word tokens, not
character n-grams) so word reordering and subtitle noise ("Project Hail
Mary: A Novel" vs. "Project Hail Mary") score as equal token sets. This is
a small hand-rolled implementation rather than `github.com/adrg/strutil`
(the library considered) — strutil's metrics operate on whole-string
character n-grams, not word token sets, which only partially gives the
reordering robustness a title/author match needs. Author is a confidence
booster, never a hard filter — a missed match costs an ntfy tap; a wrong
match silently corrupts progress on the wrong Goodreads book and is much
harder to notice.

### Secrets delivery

Besides plain environment variables, config loading also accepts
`LEAFMARK_ENV_FILE`, a path to a mounted dotenv-format file — useful in
production so credentials don't show up in `docker inspect`/
`docker compose config`. It only fills in variables not already set in the
environment, so an explicit `environment:` entry always wins for that key.
The Docker image runs as a fixed non-root UID:GID (`52416:52416`,
deliberately not `1000:1000` to avoid colliding with a host user's own
UID) — `chown` both your `DB_PATH` volume directory and any
`LEAFMARK_ENV_FILE` secrets file to that before mounting them.

## Non-goals

- No automated Goodreads *catalog* search — fuzzy matching is scoped to
  your want-to-read/currently-reading shelves only, never the full
  Goodreads catalog.
- No shelf-status transitions (e.g. auto-move to "read" at 100%) — pure
  progress-percentage updates only.
- No auth layer on the WebUI/confirm endpoint — Tailscale is the trust
  boundary; do not expose this port publicly.
- No multi-user support — single Goodreads account, single koreader-sync
  account, assumed throughout.
- Automated Goodreads login is deliberately *not* hardened against
  CAPTCHA/2FA — an unhandled challenge fails loudly via ntfy rather than
  being worked around (see [Automated Goodreads login](#automated-goodreads-login)).

## Repo structure

```
cmd/leafmark/main.go       entrypoint: wires config/db/clients, runs the HTTP server + poll loop
internal/
  config/                  env var (+ optional dotenv file) loading and validation
  db/                      schema (embedded), migrations, CRUD helpers
  koreader/                koreader-sync client
  goodreads/                progress push, CSRF handling, shelf fetch, chromedp login
  match/                   title/author normalization + token-set scoring
  ntfy/                    notification publishing
  poll/                    the core poll → diff → resolve/match → push cycle
  web/                     confirm endpoint + server-rendered WebUI (html/template, no JS)
Dockerfile
docker-compose.yml
.env.example
```

## Security

- No application-level authentication anywhere — this must only ever be
  reachable over Tailscale, never a public interface.
- `GOODREADS_PASSWORD` is your real Goodreads account password, used for
  automated re-login. Treat it, and any `LEAFMARK_ENV_FILE`/`.env` holding
  it, exactly like any other credential — restrictive file permissions,
  never committed, never logged.
