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
    is_hidden INTEGER NOT NULL DEFAULT 0 CHECK(is_hidden IN (0,1)), -- 1 = hidden from public draw
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
