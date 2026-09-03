# cd-debate-tab

Debate tournament tab engine: CSV team ingestion, side-balanced draw
generation, drag-and-drop draft editing, and live SSE publishing to a
public draw screen.

## Stack

- Go 1.27, stdlib `net/http` mux, `html/template` + `embed.FS`
- `modernc.org/sqlite` (pure Go, WAL, single-writer)
- HTMX 2.x + SSE extension, SortableJS, Tailwind CSS

## Quickstart

```sh
go run ./cmd/server        # serves :8080, stores debate.db locally
```

1. Open `/login` and sign in as admin.
2. Import teams via CSV (`Team Name,Speaker 1,Speaker 2`) at `/admin/teams`.
3. Create a round and generate the draft at `/admin/rounds`.
4. Rearrange rooms / flip sides on the draft board, then publish.
5. Public screen at `/` updates live over SSE (`/events`).

## Develop

```sh
go test ./...              # unit tests (store, draw, auth, stream, httpx)
go build ./...             # full build
make tailwind              # rebuild static/css/dist.css (needs tailwindcss CLI)
```

See `PLAN.md` for the schema, route table, and algorithm specs.

## License

MIT — see [LICENSE](LICENSE).
