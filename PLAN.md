# Debate Tournament Engine: Architecture & Implementation Plan

### 1. Technology Stack & Dependencies

* **Language/Runtime:** Go 1.27.1 verified on this system (`go version go1.27.1 darwin/arm64`).
  Pin in `go.mod`; uses `net/http` enhanced routing + stdlib concurrency.
* **Database Driver:** `modernc.org/sqlite` (Pure Go, zero CGO, WAL mode enabled).
* **Templates & Assets:** `html/template` with `embed.FS` for a zero-dependency standalone binary.
* **Frontend:** HTMX 2.x, HTMX SSE Extension (`htmx-ext-sse`), SortableJS, Tailwind CSS (compiled via standalone Tailwind CLI).

---

### 2. Database Schema (SQLite)

> Corrections applied: `rounds.num_rooms` added (chunking input K was previously
> unstated/stored nowhere); `UNIQUE` guards against double allocation and
> duplicate names; `ON DELETE RESTRICT` preserves history; PRAGMAs are
> per-connection config (see notes), not persisted DDL.

```sql
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA synchronous = NORMAL;

CREATE TABLE IF NOT EXISTS admins (
    id TEXT PRIMARY KEY, -- app-generated UUIDv7
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL, -- bcrypt cost 12
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS sessions (
    token TEXT PRIMARY KEY, -- crypto/rand 32 bytes, hex-encoded
    admin_id TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    FOREIGN KEY(admin_id) REFERENCES admins(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_sessions_admin ON sessions(admin_id);
-- Server-side cleanup job deletes expired sessions periodically.

CREATE TABLE IF NOT EXISTS teams (
    id TEXT PRIMARY KEY, -- app-generated UUIDv7
    name TEXT NOT NULL UNIQUE, -- prevents ambiguous search/draw display
    is_active INTEGER NOT NULL DEFAULT 1 CHECK(is_active IN (0,1)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS speakers (
    id TEXT PRIMARY KEY, -- app-generated UUIDv7
    team_id TEXT NOT NULL,
    name TEXT NOT NULL CHECK(length(trim(name)) > 0),
    is_active INTEGER NOT NULL DEFAULT 1 CHECK(is_active IN (0,1)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    FOREIGN KEY(team_id) REFERENCES teams(id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_speakers_team_active ON speakers(team_id, is_active);
-- Invariant (enforced in app, checked in tests): exactly 2 rows with
-- is_active=1 per team. No pure-DB way to enforce "exactly 2" in SQLite.

CREATE TABLE IF NOT EXISTS rounds (
    id TEXT PRIMARY KEY, -- app-generated UUIDv7
    name TEXT NOT NULL,                   -- e.g., "Round 1", "Semifinals"
    round_order INTEGER NOT NULL UNIQUE,  -- 1, 2, 3...
    num_rooms INTEGER NOT NULL CHECK(num_rooms > 0),
    status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft', 'published', 'concluded')),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS rooms (
    id TEXT PRIMARY KEY, -- app-generated UUIDv7
    round_id TEXT NOT NULL,
    name TEXT NOT NULL,
    FOREIGN KEY(round_id) REFERENCES rounds(id) ON DELETE CASCADE,
    UNIQUE(round_id, name)
);
CREATE INDEX IF NOT EXISTS idx_rooms_round ON rooms(round_id);

CREATE TABLE IF NOT EXISTS allocations (
    id TEXT PRIMARY KEY, -- app-generated UUIDv7
    round_id TEXT NOT NULL,
    room_id TEXT NOT NULL,
    team_id TEXT NOT NULL,
    speaker_id TEXT NOT NULL,
    side TEXT NOT NULL CHECK(side IN ('for', 'against')),
    FOREIGN KEY(round_id) REFERENCES rounds(id) ON DELETE CASCADE,
    FOREIGN KEY(room_id) REFERENCES rooms(id) ON DELETE CASCADE,
    FOREIGN KEY(team_id) REFERENCES teams(id) ON DELETE RESTRICT,
    FOREIGN KEY(speaker_id) REFERENCES speakers(id) ON DELETE RESTRICT,
    UNIQUE(round_id, speaker_id) -- each speaker debates at most once per round
);

CREATE INDEX IF NOT EXISTS idx_allocations_round ON allocations(round_id);
CREATE INDEX IF NOT EXISTS idx_allocations_round_room ON allocations(round_id, room_id);
CREATE INDEX IF NOT EXISTS idx_allocations_speaker ON allocations(speaker_id);
CREATE INDEX IF NOT EXISTS idx_allocations_team ON allocations(team_id);
```

**Connection & transition notes:**
* `journal_mode` / `foreign_keys` do not persist from `schema.sql`. Set them on
  every pool connection (`_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)`)
  or in `db.go` right after open. Single-writer discipline:
  `SetMaxOpenConns(1)` for writes (WAL allows concurrent readers).
* Status machine `draft -> published -> concluded` is enforced in app transactions,
  not by `CHECK` alone:
  `UPDATE rounds SET status='published' WHERE id=? AND status='draft'`
  and verify one row affected (makes double-click Publish idempotent).
* UUIDs are generated app-side. Timestamps use RFC3339 UTC text for clean
  Go `time.Time` scanning (avoids `DATETIME` quirks).

---

### 3. Core Algorithms & Logic Specifications

#### A. Side Balancing

Each team has exactly two active speakers ($S_1, S_2$). Exactly one must debate `for` and one `against`.

1. For both active speakers, calculate historical side bias across all `published` and `concluded` rounds:

$$\text{Bias}(S) = \text{Count}(\text{side} = \text{'for'}) - \text{Count}(\text{side} = \text{'against'})$$


2. Compare the biases:
* If $\text{Bias}(S_1) > \text{Bias}(S_2)$: $S_1 \to \text{'against'}$, $S_2 \to \text{'for'}$.
* If $\text{Bias}(S_1) < \text{Bias}(S_2)$: $S_1 \to \text{'for'}$, $S_2 \to \text{'against'}$.
* If $\text{Bias}(S_1) == \text{Bias}(S_2)$: Random 50/50 assignment using `crypto/rand`.

**Implementation notes (corrected):** compute bias in one batched query
(`SELECT speaker_id, SUM(CASE WHEN side='for' THEN 1 ELSE -1 END)` joining
`allocations -> rounds WHERE rounds.status IN ('published','concluded')`),
not N queries. Guard `len(active speakers) != 2` with typed `ErrTeamSize`.
Inject shuffle/coin-flip (`Shuffle func(n int)`, `CoinFlip func() bool`) so
unit tests are deterministic; production wires `crypto/rand`.



#### B. Room Chunking

Given $N$ active teams and $K$ rooms (`rounds.num_rooms`):

1. Validate: reject $K \le 0$; clamp $K = \min(K, N)$ so no empty rooms when $K > N$.
2. Shuffle active teams using Fisher-Yates with `crypto/rand` (injectable for tests).
3. Compute $\text{base} = \lfloor N / K \rfloor$ and $\text{rem} = N \pmod K$.
4. Allocate $\text{base} + 1$ teams to the first $\text{rem}$ rooms; allocate $\text{base}$ teams to the remaining rooms.

#### C. Room Imbalance Detection

* On load or after any drag-and-drop action, compute:

$$\Delta = \max(\text{teams\_in\_room}) - \min(\text{teams\_in\_room})$$


* If $\Delta > 1$: Render warning badge: `Imbalanced Rooms: Room sizes differ by more than 1 team.` (Does not block publishing).

---

### 4. Critical Workflows

#### A. CSV Ingestion & Inline Recovery

* Format: `Team Name,Speaker 1,Speaker 2`. Limit: 1 MB / 2000 rows; trim
  whitespace; handle quoted commas via `encoding/csv`.
* **Validation Rules:** Row must contain exactly 3 non-empty values after trim.
  Speaker 1 and Speaker 2 names must not be identical (case-insensitive).
  Duplicate `Team Name` surfaces as a row error, not a 500.
* **Partial Import Execution:**
1. Parse all lines, validate each.
2. Insert only valid rows inside a single transaction.
3. Invalid rows are gathered into an error list containing `(LineNumber, RawText, ErrorReason)`.
4. If errors exist, the response renders `import_errors.html` containing an editable form of the failed rows with pre-filled inputs. Admins correct mistakes inline and submit via `POST /admin/teams/manual-batch` (see route table).



#### B. Drag-and-Drop Draft Editing

* Built with SortableJS + HTMX.
* Moving a team card to another room fires:
`POST /admin/rounds/{roundID}/move-team` with form data: `team_id`, `target_room_id`.
* The handler validates `target_room_id` belongs to the same round, updates `allocations.room_id` for both team members in a transaction, recalculates room counts, and returns the updated `#draft-canvas` HTML fragment.
* Flipping sides fires:
`POST /admin/rounds/{roundID}/teams/{teamID}/flip-sides`.
The handler swaps the `for` and `against` values between the two speakers in `allocations`.

#### C. Speaker Substitution & Name Redaction

* **Name Redaction:** `PATCH /admin/speakers/{speakerID}/redact` updates `speakers.name` directly. Because `allocations` references `speaker_id`, all past and future records reflect the updated name immediately (accepted tradeoff: rewrites displayed history).
* **Teammate Substitution:** `POST /admin/teams/{teamID}/substitute-speaker`:
1. Sets `speakers.is_active = 0` on the departing member.
2. Inserts the new speaker with `is_active = 1` linked to the same `team_id` (validate non-empty, distinct from teammate).
3. In the same transaction, if an open `draft` round allocates the departing speaker, re-point those draft `allocations` rows to the new speaker (preserving side). Future draws automatically use the new speaker (starting with neutral side bias 0). Historical (`published`/`concluded`) allocations remain anchored to the previous speaker ID.



#### D. Non-Blocking SSE Distribution

* The SSE hub monitors `r.Context().Done()` to instantly reap dead connections and drops lagging clients if channel buffers (capacity 4) fill up.
* When the admin clicks **Publish**, the server runs the guarded
  `draft -> published` transition, renders the full public draw grid template
  into an HTML buffer, strips newlines (SSE `data:` lines must not contain raw
  `\n`), emits one `data:` line per logical line plus `event: draw-published`,
  `retry: 3000`, and broadcasts to the hub. Alternative: broadcast a version
  URL and let clients `hx-get` the grid.
* Public clients listen via `hx-ext="sse"` and perform an atomic `innerHTML` swap on `#draw-grid`.

---

### 5. API & Route Structure (Go 1.27 Mux)

Param naming is consistent: `{roundID}`, `{teamID}`, `{speakerID}`.

| Method | Path | Auth Required | Description |
| --- | --- | --- | --- |
| `GET` | `/` | No | Public draw page (with search input and SSE listener) |
| `GET` | `/draw/search` | No | Dynamic draw filtering by speaker/team name (HTMX partial, parameterized `LIKE ... ESCAPE`, capped results) |
| `GET` | `/events` | No | Public SSE stream endpoint (`retry: 3000`, newline-stripped `data:` frames) |
| `GET` | `/login` | No | Admin login page |
| `POST` | `/login` | No | Authenticate and issue secure session cookie (rate-limited) |
| `POST` | `/logout` | Yes | Invalidate session (server-side delete + clear cookie) |
| `GET` | `/admin/teams` | Yes | Team management, active/eliminated toggles |
| `POST` | `/admin/teams/import` | Yes | Process CSV upload; returns success or error repair form |
| `POST` | `/admin/teams/manual-batch` | Yes | Submit inline-corrected CSV rows from `import_errors.html` |
| `POST` | `/admin/teams/toggle-active` | Yes | Toggle `is_active` status for elimination/resurrection (body: `team_id`) |
| `POST` | `/admin/teams/{teamID}/substitute-speaker` | Yes | Deactivate old speaker, add new active speaker (+ re-point open draft) |
| `PATCH` | `/admin/speakers/{speakerID}/redact` | Yes | In-place name correction/redaction (via `hx-patch` + CSRF header) |
| `GET` | `/admin/rounds` | Yes | Round list and round creator |
| `POST` | `/admin/rounds` | Yes | Create round (`name`, `round_order`, `num_rooms`) in `draft` status |
| `POST` | `/admin/rounds/generate` | Yes | Run chunking and balancing; persist draft |
| `GET` | `/admin/rounds/{roundID}/draft` | Yes | Interactive SortableJS draft dashboard |
| `POST` | `/admin/rounds/{roundID}/move-team` | Yes | Atomic team relocation between rooms (body: `team_id`, `target_room_id`) |
| `POST` | `/admin/rounds/{roundID}/teams/{teamID}/flip-sides` | Yes | Atomic team side inversion |
| `POST` | `/admin/rounds/{roundID}/publish` | Yes | Lock draft, set `published` (guarded `WHERE status='draft'`), broadcast SSE |
| `POST` | `/admin/rounds/{roundID}/conclude` | Yes | Mark round `concluded` (only from `published`, frozen permanently) |

Auth: bcrypt cost 12, HttpOnly + SameSite=Lax (+ Secure in prod) cookies,
per-session CSRF token checked on mutating routes including HTMX `HX-Request`.

---

### 6. Directory Layout

```text
├── cmd/
│   └── server/
│       └── main.go               // wires Store, DrawService, Hub, handlers
├── internal/
│   ├── auth/
│   │   ├── auth.go             // Password hashing (bcrypt) & session checks
│   │   └── middleware.go       // Auth protection & CSRF verification
│   ├── database/               // purely SQLite open + pragmas + pool tuning
│   │   ├── db.go               // SQLite connection pool (WAL, busy_timeout, FK ON)
│   │   └── schema.sql          // Migration script (DDL only, no PRAGMA persistence)
│   ├── store/
│   │   ├── store.go            // Store interface + tx helpers (SQL lives here only)
│   │   └── sqlite.go           // implementation — not implemented (scaffold first)
│   ├── draw/
│   │   ├── draw.go             // DrawService: Generate/MoveTeam/FlipSides/Publish — not implemented
│   │   ├── engine.go           // pure room chunking & side-balancing (injectable rand)
│   │   └── csv.go              // robust CSV parser & error aggregator (pure)
│   ├── stream/
│   │   └── hub.go              // non-blocking SSE Hub (owned by main, injected)
│   ├── httpx/
│   │   ├── middleware.go       // request-id, recover, logging
│   │   └── templates.go        // template parsing (embed.FS) helpers
│   ├── handlers/
│   │   ├── admin_rounds.go     // thin: parse -> DrawService -> render fragment
│   │   ├── admin_teams.go      // thin: parse -> Store/DrawService -> render
│   │   ├── public.go           // Draw view and HTMX search
│   │   └── sse.go              // streaming handler only (Hub lives in stream/)
├── templates/
│   ├── layout.html             // Base HTML shell
│   ├── admin/
│   │   ├── teams.html          // Team roster & CSV upload
│   │   ├── import_errors.html  // Fragment for fixing bad CSV lines
│   │   ├── draft.html          // SortableJS interactive board
│   │   └── partials/
│   │       ├── draft_canvas.html // Updatable dropzones & imbalance badge
│   │       └── team_row.html
│   └── public/
│       ├── draw.html           // Main projector / public screen
│       └── partials/
│           └── draw_grid.html  // Pure grid partial swapped by SSE / search
├── static/
│   ├── css/dist.css            // Compiled Tailwind
│   └── js/sortable.min.js
├── Makefile
└── go.mod

```

---

### 7. Step-by-Step Prompt for Coding Agent

Copy and execute the prompt below to generate the system:

```text
Implement a complete debate competition dashboard in Go 1.27 using the standard library net/http, modernc.org/sqlite, html/template, and HTMX 2.x with the SSE extension.

Key Technical Rules:
1. Database: Embedded SQLite in WAL mode with per-connection pragmas (foreign_keys=ON, busy_timeout=5000). Execute the DDL in §2 (includes rounds.num_rooms, UNIQUE(round_id,speaker_id), RESTRICT history deletes). Enforce draft->published->concluded with guarded WHERE status= updates.
2. Debate Rules: Each team has 2 active members. In every round, one must debate 'for' and one 'against'. Track individual speaker side bias (Count(For) - Count(Against)) across published/concluded rounds in one batched query; inject shuffle/coin-flip for tests, wire crypto/rand in prod. Clamp K=min(K,N).
3. Ingestion: CSV upload (1MB/2000 rows, encoding/csv, trim, case-insensitive dup check). Insert valid rows in one tx; return import_errors.html for bad rows resubmitted to POST /admin/teams/manual-batch.
4. Draft Management: SortableJS drag-and-drop. Moving a card fires POST /admin/rounds/{roundID}/move-team (validate same-round target), reallocates both speakers, checks max-min > 1, returns updated draft canvas fragment with imbalance badge. Per-team 'Flip Sides' posts to POST /admin/rounds/{roundID}/teams/{teamID}/flip-sides.
5. Substitution & Redaction: POST /admin/teams/{teamID}/substitute-speaker deactivates old, inserts new, and re-points open draft allocations in the same tx (history stays on old ID). PATCH /admin/speakers/{speakerID}/redact renames in place.
6. SSE Hub: Non-blocking in-memory Hub (buffer 4, drop slow clients, r.Context().Done() reap). On publish, strip newlines into valid SSE data: lines + event: draw-published + retry: 3000.
7. Public View: Render a clean, high-contrast, responsive table/grid with an HTMX-driven live search filter (parameterized LIKE, capped) and auto-reconnecting SSE swap target.
8. Security: Session auth with bcrypt (cost 12), HttpOnly SameSite=Lax (+Secure prod) cookies, per-session CSRF on mutating + HTMX routes, rate-limited login, parameterized SQL.

Begin by setting up the project structure (§6 layout: store/draw/stream split, no shared models package), SQLite migrations, and the core debate balancing engine with unit tests.

```
