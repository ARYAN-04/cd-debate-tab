# Yellow-CD

Debate tournament tab engine: team ingestion (CSV or manual form),
side-balanced draw generation, drag-and-drop draft editing, and live
SSE publishing to a public draw screen.

## Stack

- Go 1.27, stdlib `net/http` mux, `html/template` + `embed.FS`
- `modernc.org/sqlite` (pure Go, WAL, single-writer)
- HTMX 2.x + SSE extension, SortableJS, hand-written CSS

## Quickstart

```sh
make seed                 # load 6 mock teams + 2 rounds into debate.db
go run ./cmd/server       # serves :8080
```

Sign in at `/login` (`admin`/`admin` unless `ADMIN_USER`/`ADMIN_PASS`
are set). Flow: teams → rounds → generate → drag/flip on the draft
board → publish. The public screen at `/` follows the newest visible
round live over SSE.

Notable behavior: taken round orders offer shift-forward vs repick;
published rounds are immutable (moves/flips refuse); rounds can be
hidden from public view without unpublishing; the projector never
sees drafts.

Security & robustness:
- CSRF protection across mutating actions via cookie and header/form tokens
- Constant-time password validation and IP rate limiting against brute force
- Periodic session cleanup and HTTP server timeout configurations
- Single-transaction atomic CSV team imports with collision repairs

## Develop

```sh
go test ./...              # unit tests (store, draw, auth, stream, httpx, handlers)
go build ./...             # full build
make tailwind              # rebuild static/css/dist.css (needs tailwindcss CLI)
```

Layout: `cmd/server` (wiring) + `cmd/seed` (mock data),
`internal/{store,draw,auth,stream,httpx,handlers}`, `templates/`,
`static/`. All SQL lives in `internal/store`; the draw service stays
HTTP/SQL-free; handlers stay thin (parse → service → fragment).

## License

MIT — see [LICENSE](LICENSE).
