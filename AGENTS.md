# AGENTS.md — cd-debate-tab

Inherits `~/.agents/AGENTS.md` (simplicity-first, surgical changes, gofumpt,
explicit errors, SSH remotes, Mitchell Hashimoto commits). Project specifics below.

## Stack
- Go 1.27 (`go.mod` pinned), stdlib `net/http` mux (`METHOD /path/{roundID}`), `html/template` + `embed.FS`.
- `modernc.org/sqlite` (pure Go, WAL). Pragmas are per-connection (`foreign_keys=ON`, `busy_timeout=5000`), not persisted DDL. Single-writer: `SetMaxOpenConns(1)`.
- HTMX 2.x + `htmx-ext-sse`, SortableJS, Tailwind standalone CLI (`static/css/dist.css` compiled).

## Source of truth
- `PLAN.md` §2 (DDL), §5 (routes), §4 (workflows). If code and PLAN disagree, surface it; don't silently drift.

## Layout (§6)
- `cmd/server/main.go` — wires Store, DrawService, Hub, handlers.
- `internal/store/` — all SQL + tx helpers. Only package that imports the sqlite driver.
- `internal/draw/` — `DrawService` (Generate/MoveTeam/FlipSides/Publish) + pure `engine.go` (chunking, bias) + `csv.go`. No HTTP, no SQL wire types on public surface.
- `internal/stream/` — SSE Hub (owned by main, injected). `internal/handlers/sse.go` is streaming handler only.
- `internal/auth/`, `internal/httpx/` (middleware, templates), `internal/handlers/` (thin: parse → service → fragment).
- No shared `models/` dump. Domain types live in `draw`, rows in `store`.
- Templates in `templates/`, HTMX fragments under `*/partials/`.

## Load-bearing invariants
- Round status `draft -> published -> concluded`, guarded in tx: `UPDATE ... WHERE id=? AND status=?`, check rows-affected (idempotent publish).
- Exactly 2 active speakers per team (app-enforced, `ErrTeamSize` otherwise).
- `UNIQUE(round_id, speaker_id)`; `teams.name UNIQUE`; `ON DELETE RESTRICT` on history tables.
- Bias = `SUM(for=1, against=-1)` over `published`/`concluded` only, one batched query. Shuffle/coin-flip injected (`crypto/rand` in prod) for deterministic tests.
- Chunking: `K = min(K, N)`, reject `K <= 0`.
- SSE: never emit raw newlines in `data:`; strip to one line per frame + `event: draw-published` + `retry: 3000`. Hub: buffer 4, drop slow clients, `r.Context().Done()` reap.
- CSV: 1MB/2000-row cap, `encoding/csv`, trim, case-insensitive speaker-dup check; valid rows in one tx, bad rows → `import_errors.html` → `POST /admin/teams/manual-batch`.
- Substitution re-points open `draft` allocations in the same tx; history stays on old ID. Redaction rewrites displayed history (accepted).
- Param names: `{roundID}`, `{teamID}`, `{speakerID}`. Flip: `POST /admin/rounds/{roundID}/teams/{teamID}/flip-sides`.

## Commands
- `go test ./...`, `gofumpt -l .` (or `gofmt`), `go build ./...`.
- Tailwind: standalone CLI build to `static/css/dist.css` (see Makefile when added).

## Commits
- SSH remotes only. Small atomic units, lowercase scope prefix, imperative, <50 chars, blank line + why-body wrapped at 72.
