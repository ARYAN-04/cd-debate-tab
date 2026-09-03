// Package store holds domain row types and all SQL for the app.
// Only this package imports the sqlite driver (blank) via tests and
// callers wire *sql.DB in; every statement is parameterized with '?'.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type Team struct {
	ID        string
	Name      string
	IsActive  bool
	CreatedAt time.Time
}

type Speaker struct {
	ID        string
	TeamID    string
	Name      string
	IsActive  bool
	CreatedAt time.Time
}

type Round struct {
	ID         string
	Name       string
	RoundOrder int
	NumRooms   int
	Status     string
	CreatedAt  time.Time
}

type Room struct {
	ID      string
	RoundID string
	Name    string
}

type Allocation struct {
	ID        string
	RoundID   string
	RoomID    string
	TeamID    string
	SpeakerID string
	Side      string
}

type Admin struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

type Session struct {
	Token     string
	AdminID   string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type TeamWithSpeakers struct {
	Team     Team
	Speakers []Speaker
}

type DraftAllocation struct {
	Allocation Allocation
	TeamName   string
	Speaker    string
	RoomName   string
}

// Store wraps *sql.DB. All SQL lives in this package.
type Store struct {
	DB *sql.DB
}

// New returns a Store over db.
func New(db *sql.DB) *Store {
	return &Store{DB: db}
}

var (
	ErrNotFound       = errors.New("not found")
	ErrSessionExpired = errors.New("session expired")
	ErrRoomMismatch   = errors.New("target room belongs to a different round")
	ErrRoundNotDraft  = errors.New("round is not a draft")
	ErrBadSide        = errors.New("side must be 'for' or 'against'")
)

// schema mirrors internal/database/schema.sql (store must work on any
// *sql.DB handed to it, including in-memory DBs in tests).
const schema = `
CREATE TABLE IF NOT EXISTS admins (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE IF NOT EXISTS sessions (
    token TEXT PRIMARY KEY,
    admin_id TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    FOREIGN KEY(admin_id) REFERENCES admins(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_sessions_admin ON sessions(admin_id);
CREATE TABLE IF NOT EXISTS teams (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    is_active INTEGER NOT NULL DEFAULT 1 CHECK(is_active IN (0,1)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE IF NOT EXISTS speakers (
    id TEXT PRIMARY KEY,
    team_id TEXT NOT NULL,
    name TEXT NOT NULL CHECK(length(trim(name)) > 0),
    is_active INTEGER NOT NULL DEFAULT 1 CHECK(is_active IN (0,1)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    FOREIGN KEY(team_id) REFERENCES teams(id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_speakers_team_active ON speakers(team_id, is_active);
CREATE TABLE IF NOT EXISTS rounds (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    round_order INTEGER NOT NULL UNIQUE,
    num_rooms INTEGER NOT NULL CHECK(num_rooms > 0),
    status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft', 'published', 'concluded')),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE IF NOT EXISTS rooms (
    id TEXT PRIMARY KEY,
    round_id TEXT NOT NULL,
    name TEXT NOT NULL,
    FOREIGN KEY(round_id) REFERENCES rounds(id) ON DELETE CASCADE,
    UNIQUE(round_id, name)
);
CREATE INDEX IF NOT EXISTS idx_rooms_round ON rooms(round_id);
CREATE TABLE IF NOT EXISTS allocations (
    id TEXT PRIMARY KEY,
    round_id TEXT NOT NULL,
    room_id TEXT NOT NULL,
    team_id TEXT NOT NULL,
    speaker_id TEXT NOT NULL,
    side TEXT NOT NULL CHECK(side IN ('for', 'against')),
    FOREIGN KEY(round_id) REFERENCES rounds(id) ON DELETE CASCADE,
    FOREIGN KEY(room_id) REFERENCES rooms(id) ON DELETE CASCADE,
    FOREIGN KEY(team_id) REFERENCES teams(id) ON DELETE RESTRICT,
    FOREIGN KEY(speaker_id) REFERENCES speakers(id) ON DELETE RESTRICT,
    UNIQUE(round_id, speaker_id)
);
CREATE INDEX IF NOT EXISTS idx_allocations_round ON allocations(round_id);
CREATE INDEX IF NOT EXISTS idx_allocations_round_room ON allocations(round_id, room_id);
CREATE INDEX IF NOT EXISTS idx_allocations_speaker ON allocations(speaker_id);
CREATE INDEX IF NOT EXISTS idx_allocations_team ON allocations(team_id);
`

// NewID returns an app-side UUIDv7-style hex ID (16 crypto/rand bytes,
// version nibble 7, RFC 4122 variant bits).
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:]), nil
}

// NewSessionToken returns a crypto/rand 32-byte hex token.
func NewSessionToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// nowStr returns the current UTC time formatted as RFC3339.
func nowStr() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// parseTime scans RFC3339 UTC text (or a time.Time / []byte holding one).
func parseTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t.UTC(), nil
	case string:
		return time.Parse(time.RFC3339Nano, t)
	case []byte:
		return time.Parse(time.RFC3339Nano, string(t))
	default:
		return time.Time{}, errors.New("store: unsupported timestamp type")
	}
}

func scanTime(dest *time.Time, v any) error {
	t, err := parseTime(v)
	if err != nil {
		return err
	}
	*dest = t
	return nil
}

func (s *Store) InitSchema(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, schema); err != nil {
		return err
	}
	return nil
}

func (s *Store) EnsureAdmin(ctx context.Context, username, passwordHash string) error {
	if strings.TrimSpace(username) == "" || passwordHash == "" {
		return errors.New("store: username and password hash required")
	}
	id, err := NewID()
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO admins (id, username, password_hash, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(username) DO UPDATE SET password_hash=excluded.password_hash`,
		id, strings.TrimSpace(username), passwordHash, nowStr())
	return err
}

func (s *Store) GetAdminByUsername(ctx context.Context, username string) (Admin, error) {
	var a Admin
	var created any
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, username, password_hash, created_at FROM admins WHERE username = ?`,
		username).Scan(&a.ID, &a.Username, &a.PasswordHash, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Admin{}, ErrNotFound
		}
		return Admin{}, err
	}
	if err := scanTime(&a.CreatedAt, created); err != nil {
		return Admin{}, err
	}
	return a, nil
}

func (s *Store) CreateSession(ctx context.Context, token, adminID string, expiresAt time.Time) error {
	if token == "" || adminID == "" {
		return errors.New("store: token and admin id required")
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO sessions (token, admin_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		token, adminID, expiresAt.UTC().Format(time.RFC3339Nano), nowStr())
	return err
}

func (s *Store) GetSessionByToken(ctx context.Context, token string) (Session, error) {
	var sess Session
	var expires, created any
	err := s.DB.QueryRowContext(ctx,
		`SELECT token, admin_id, expires_at, created_at FROM sessions WHERE token = ?`,
		token).Scan(&sess.Token, &sess.AdminID, &expires, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	if err := scanTime(&sess.ExpiresAt, expires); err != nil {
		return Session{}, err
	}
	if err := scanTime(&sess.CreatedAt, created); err != nil {
		return Session{}, err
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		return Session{}, ErrSessionExpired
	}
	return sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// CreateTeam inserts a team plus exactly 2 active speakers in one tx.
// IDs are generated app-side; names are trimmed.
func (s *Store) CreateTeam(ctx context.Context, name, speaker1, speaker2 string) (TeamWithSpeakers, error) {
	name = strings.TrimSpace(name)
	speaker1 = strings.TrimSpace(speaker1)
	speaker2 = strings.TrimSpace(speaker2)
	if name == "" || speaker1 == "" || speaker2 == "" {
		return TeamWithSpeakers{}, errors.New("store: team and both speaker names required")
	}
	if strings.EqualFold(speaker1, speaker2) {
		return TeamWithSpeakers{}, errors.New("store: speaker names must differ")
	}
	teamID, err := NewID()
	if err != nil {
		return TeamWithSpeakers{}, err
	}
	sp1ID, err := NewID()
	if err != nil {
		return TeamWithSpeakers{}, err
	}
	sp2ID, err := NewID()
	if err != nil {
		return TeamWithSpeakers{}, err
	}
	now := nowStr()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return TeamWithSpeakers{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO teams (id, name, is_active, created_at) VALUES (?, ?, 1, ?)`,
		teamID, name, now); err != nil {
		return TeamWithSpeakers{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO speakers (id, team_id, name, is_active, created_at) VALUES (?, ?, ?, 1, ?)`,
		sp1ID, teamID, speaker1, now); err != nil {
		return TeamWithSpeakers{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO speakers (id, team_id, name, is_active, created_at) VALUES (?, ?, ?, 1, ?)`,
		sp2ID, teamID, speaker2, now); err != nil {
		return TeamWithSpeakers{}, err
	}
	if err := tx.Commit(); err != nil {
		return TeamWithSpeakers{}, err
	}
	committed = true
	createdAt, _ := time.Parse(time.RFC3339Nano, now)
	out := TeamWithSpeakers{
		Team: Team{ID: teamID, Name: name, IsActive: true, CreatedAt: createdAt},
		Speakers: []Speaker{
			{ID: sp1ID, TeamID: teamID, Name: speaker1, IsActive: true, CreatedAt: createdAt},
			{ID: sp2ID, TeamID: teamID, Name: speaker2, IsActive: true, CreatedAt: createdAt},
		},
	}
	return out, nil
}

func (s *Store) ListTeamsWithSpeakers(ctx context.Context) ([]TeamWithSpeakers, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, is_active, created_at FROM teams ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var teams []TeamWithSpeakers
	for rows.Next() {
		var t Team
		var active int
		var created any
		if err := rows.Scan(&t.ID, &t.Name, &active, &created); err != nil {
			return nil, err
		}
		t.IsActive = active == 1
		if err := scanTime(&t.CreatedAt, created); err != nil {
			return nil, err
		}
		teams = append(teams, TeamWithSpeakers{Team: t})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range teams {
		srows, err := s.DB.QueryContext(ctx,
			`SELECT id, team_id, name, is_active, created_at FROM speakers
			 WHERE team_id = ? ORDER BY name`, teams[i].Team.ID)
		if err != nil {
			return nil, err
		}
		var sps []Speaker
		for srows.Next() {
			var sp Speaker
			var active int
			var created any
			if err := srows.Scan(&sp.ID, &sp.TeamID, &sp.Name, &active, &created); err != nil {
				srows.Close()
				return nil, err
			}
			sp.IsActive = active == 1
			if err := scanTime(&sp.CreatedAt, created); err != nil {
				srows.Close()
				return nil, err
			}
			sps = append(sps, sp)
		}
		srows.Close()
		if err := srows.Err(); err != nil {
			return nil, err
		}
		teams[i].Speakers = sps
	}
	return teams, nil
}

func (s *Store) SetTeamActive(ctx context.Context, teamID string, active bool) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE teams SET is_active = ? WHERE id = ?`, boolToInt(active), teamID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SubstituteSpeaker deactivates oldSpeakerID, inserts the new speaker, and
// re-points open draft allocations in one tx; history stays on the old ID.
// An empty newSpeakerID is generated app-side.
func (s *Store) SubstituteSpeaker(ctx context.Context, teamID, oldSpeakerID, newSpeakerID, newName string) error {
	newName = strings.TrimSpace(newName)
	if teamID == "" || oldSpeakerID == "" {
		return errors.New("store: team and old speaker id required")
	}
	if newName == "" {
		return errors.New("store: new speaker name required")
	}
	if newSpeakerID == "" {
		id, err := NewID()
		if err != nil {
			return err
		}
		newSpeakerID = id
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var oldTeam string
	var oldActive int
	err = tx.QueryRowContext(ctx,
		`SELECT team_id, is_active FROM speakers WHERE id = ?`, oldSpeakerID).Scan(&oldTeam, &oldActive)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if oldTeam != teamID {
		return errors.New("store: speaker does not belong to team")
	}
	var teammate string
	err = tx.QueryRowContext(ctx,
		`SELECT name FROM speakers WHERE team_id = ? AND id != ? AND is_active = 1 LIMIT 1`,
		teamID, oldSpeakerID).Scan(&teammate)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && strings.EqualFold(teammate, newName) {
		return errors.New("store: new name matches teammate")
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE speakers SET is_active = 0 WHERE id = ?`, oldSpeakerID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO speakers (id, team_id, name, is_active, created_at) VALUES (?, ?, ?, 1, ?)`,
		newSpeakerID, teamID, newName, nowStr()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE allocations SET speaker_id = ?
		 WHERE speaker_id = ? AND round_id IN (SELECT id FROM rounds WHERE status = 'draft')`,
		newSpeakerID, oldSpeakerID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) RedactSpeaker(ctx context.Context, speakerID, newName string) error {
	newName = strings.TrimSpace(newName)
	if speakerID == "" || newName == "" {
		return errors.New("store: speaker id and new name required")
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE speakers SET name = ? WHERE id = ?`, newName, speakerID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateRound inserts a draft round with an app-side ID.
func (s *Store) CreateRound(ctx context.Context, name string, roundOrder, numRooms int) (Round, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Round{}, errors.New("store: round name required")
	}
	if numRooms <= 0 {
		return Round{}, errors.New("store: num_rooms must be > 0")
	}
	id, err := NewID()
	if err != nil {
		return Round{}, err
	}
	now := nowStr()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO rounds (id, name, round_order, num_rooms, status, created_at)
		 VALUES (?, ?, ?, ?, 'draft', ?)`,
		id, name, roundOrder, numRooms, now); err != nil {
		return Round{}, err
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, now)
	return Round{ID: id, Name: name, RoundOrder: roundOrder, NumRooms: numRooms, Status: "draft", CreatedAt: createdAt}, nil
}

// CreateRoom inserts a room for a round with an app-side ID.
func (s *Store) CreateRoom(ctx context.Context, roundID, name string) (Room, error) {
	name = strings.TrimSpace(name)
	if roundID == "" || name == "" {
		return Room{}, errors.New("store: round id and room name required")
	}
	id, err := NewID()
	if err != nil {
		return Room{}, err
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO rooms (id, round_id, name) VALUES (?, ?, ?)`, id, roundID, name); err != nil {
		return Room{}, err
	}
	return Room{ID: id, RoundID: roundID, Name: name}, nil
}

func (s *Store) ListRounds(ctx context.Context) ([]Round, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, round_order, num_rooms, status, created_at FROM rounds ORDER BY round_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Round
	for rows.Next() {
		var r Round
		var created any
		if err := rows.Scan(&r.ID, &r.Name, &r.RoundOrder, &r.NumRooms, &r.Status, &created); err != nil {
			return nil, err
		}
		if err := scanTime(&r.CreatedAt, created); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetRound(ctx context.Context, roundID string) (Round, error) {
	var r Round
	var created any
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, name, round_order, num_rooms, status, created_at FROM rounds WHERE id = ?`,
		roundID).Scan(&r.ID, &r.Name, &r.RoundOrder, &r.NumRooms, &r.Status, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Round{}, ErrNotFound
		}
		return Round{}, err
	}
	if err := scanTime(&r.CreatedAt, created); err != nil {
		return Round{}, err
	}
	return r, nil
}

// SaveDraft replaces the draft draw for a round in one tx: it deletes the
// existing draft rooms and allocations for the round, then inserts rooms
// and allocations. Empty allocation IDs are generated app-side.
func (s *Store) SaveDraft(ctx context.Context, roundID string, rooms []Room, allocs []Allocation) error {
	if roundID == "" {
		return errors.New("store: round id required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM rounds WHERE id = ?`, roundID).Scan(&status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if status != "draft" {
		return ErrRoundNotDraft
	}
	for _, a := range allocs {
		if a.RoundID != roundID {
			return errors.New("store: allocation round mismatch")
		}
		if a.Side != "for" && a.Side != "against" {
			return ErrBadSide
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM allocations WHERE round_id = ?`, roundID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM rooms WHERE round_id = ?`, roundID); err != nil {
		return err
	}
	for _, rm := range rooms {
		roomID := rm.ID
		if roomID == "" {
			roomID, err = NewID()
			if err != nil {
				return err
			}
		}
		if strings.TrimSpace(rm.Name) == "" {
			return errors.New("store: room name required")
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO rooms (id, round_id, name) VALUES (?, ?, ?)`,
			roomID, roundID, strings.TrimSpace(rm.Name)); err != nil {
			return err
		}
	}
	for _, a := range allocs {
		allocID := a.ID
		if allocID == "" {
			allocID, err = NewID()
			if err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO allocations (id, round_id, room_id, team_id, speaker_id, side)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			allocID, roundID, a.RoomID, a.TeamID, a.SpeakerID, a.Side); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

const draftAllocationCols = `a.id, a.round_id, a.room_id, a.team_id, a.speaker_id, a.side,
		t.name, s.name, r.name`

func scanDraftAllocation(rows *sql.Rows) (DraftAllocation, error) {
	var d DraftAllocation
	err := rows.Scan(&d.Allocation.ID, &d.Allocation.RoundID, &d.Allocation.RoomID,
		&d.Allocation.TeamID, &d.Allocation.SpeakerID, &d.Allocation.Side,
		&d.TeamName, &d.Speaker, &d.RoomName)
	return d, err
}

// HasDraft reports whether a round has any allocations (i.e. it was generated).
func (s *Store) HasDraft(ctx context.Context, roundID string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM allocations WHERE round_id = ?`, roundID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ShiftRoundsFrom moves every round at order or later one step forward,
// highest first so the UNIQUE(order) guard never collides mid-shift.
func (s *Store) ShiftRoundsFrom(ctx context.Context, order int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM rounds WHERE round_order >= ? ORDER BY round_order DESC`, order)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE rounds SET round_order = round_order + 1 WHERE id = ?`, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// ListPublicRounds returns published/concluded rounds, newest order first.
func (s *Store) ListPublicRounds(ctx context.Context) ([]Round, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, round_order, num_rooms, status, created_at FROM rounds
		 WHERE status IN ('published','concluded') ORDER BY round_order DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Round
	for rows.Next() {
		var r Round
		var created any
		if err := rows.Scan(&r.ID, &r.Name, &r.RoundOrder, &r.NumRooms, &r.Status, &created); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetDraftAllocations(ctx context.Context, roundID string) ([]DraftAllocation, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+draftAllocationCols+`
		 FROM allocations a
		 JOIN teams t ON t.id = a.team_id
		 JOIN speakers s ON s.id = a.speaker_id
		 JOIN rooms r ON r.id = a.room_id
		 WHERE a.round_id = ?
		 ORDER BY r.name, t.name, s.name`, roundID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DraftAllocation
	for rows.Next() {
		d, err := scanDraftAllocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// draftOnlyTx fails unless roundID is still a draft (locked rounds are
// immutable). Call inside an open tx before mutating draft allocations.
func draftOnlyTx(ctx context.Context, tx *sql.Tx, roundID string) error {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM rounds WHERE id = ?`, roundID).Scan(&status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if status != "draft" {
		return ErrRoundNotDraft
	}
	return nil
}

// MoveTeam re-points both of a team's speaker allocations to the target
// room in one tx after verifying the room belongs to the same round.
func (s *Store) MoveTeam(ctx context.Context, roundID, teamID, targetRoomID string) error {
	if roundID == "" || teamID == "" || targetRoomID == "" {
		return errors.New("store: round, team and target room required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := draftOnlyTx(ctx, tx, roundID); err != nil {
		return err
	}
	var roomRound string
	err = tx.QueryRowContext(ctx, `SELECT round_id FROM rooms WHERE id = ?`, targetRoomID).Scan(&roomRound)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if roomRound != roundID {
		return ErrRoomMismatch
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE allocations SET room_id = ? WHERE round_id = ? AND team_id = ?`,
		targetRoomID, roundID, teamID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// FlipSides swaps for/against between a team's 2 speakers in one tx.
func (s *Store) FlipSides(ctx context.Context, roundID, teamID string) error {
	if roundID == "" || teamID == "" {
		return errors.New("store: round and team required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := draftOnlyTx(ctx, tx, roundID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT speaker_id, side FROM allocations WHERE round_id = ? AND team_id = ?`,
		roundID, teamID)
	if err != nil {
		return err
	}
	type pair struct{ speaker, side string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.speaker, &p.side); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(pairs) != 2 {
		return errors.New("store: team must have exactly 2 allocations to flip")
	}
	swapped := map[string]string{
		pairs[0].speaker: oppositeSide(pairs[0].side),
		pairs[1].speaker: oppositeSide(pairs[1].side),
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE allocations SET side = CASE speaker_id WHEN ? THEN ? WHEN ? THEN ? ELSE side END
		 WHERE round_id = ? AND team_id = ?`,
		pairs[0].speaker, swapped[pairs[0].speaker], pairs[1].speaker, swapped[pairs[1].speaker],
		roundID, teamID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 2 {
		return errors.New("store: flip affected unexpected row count")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func oppositeSide(side string) string {
	if side == "for" {
		return "against"
	}
	return "for"
}

// Publish guards the draft -> published transition with WHERE status.
// It reports whether the transition happened (idempotent: second call
// returns false, nil).
func (s *Store) Publish(ctx context.Context, roundID string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE rounds SET status = 'published' WHERE id = ? AND status = 'draft'`, roundID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Conclude guards the published -> concluded transition with WHERE status.
func (s *Store) Conclude(ctx context.Context, roundID string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE rounds SET status = 'concluded' WHERE id = ? AND status = 'published'`, roundID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// BiasMap batches bias (for=1, against=-1) over published/concluded only
// in a single query keyed by speaker id.
func (s *Store) BiasMap(ctx context.Context) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT a.speaker_id, SUM(CASE WHEN a.side = 'for' THEN 1 ELSE -1 END)
		 FROM allocations a
		 JOIN rounds r ON r.id = a.round_id
		 WHERE r.status IN ('published', 'concluded')
		 GROUP BY a.speaker_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var bias int
		if err := rows.Scan(&id, &bias); err != nil {
			return nil, err
		}
		out[id] = bias
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// escapeLike escapes \, % and _ for a LIKE ... ESCAPE '\' pattern.
func escapeLike(q string) string {
	q = strings.ReplaceAll(q, `\`, `\\`)
	q = strings.ReplaceAll(q, `%`, `\%`)
	q = strings.ReplaceAll(q, `_`, `\_`)
	return q
}

// SearchAllocations uses parameterized LIKE ESCAPE, capped at 100 rows.
func (s *Store) SearchAllocations(ctx context.Context, query string) ([]DraftAllocation, error) {
	return s.searchAllocationsIn(ctx, query, "", false)
}

// SearchPublicAllocations matches SearchAllocations but only over
// published/concluded rounds, so drafts never leak to the public screen.
// A non-empty roundID scopes the search to that round.
func (s *Store) SearchPublicAllocations(ctx context.Context, query, roundID string) ([]DraftAllocation, error) {
	return s.searchAllocationsIn(ctx, query, roundID, true)
}

func (s *Store) searchAllocationsIn(ctx context.Context, query, roundID string, publicOnly bool) ([]DraftAllocation, error) {
	pattern := "%" + escapeLike(query) + "%"
	statusFilter := ""
	if publicOnly {
		statusFilter = `JOIN rounds rd ON rd.id = a.round_id AND rd.status IN ('published','concluded')`
	}
	roundFilter := ""
	args := []any{pattern, pattern}
	if roundID != "" {
		roundFilter = `AND a.round_id = ?`
		args = append(args, roundID)
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+draftAllocationCols+`
		 FROM allocations a
		 JOIN teams t ON t.id = a.team_id
		 JOIN speakers s ON s.id = a.speaker_id
		 JOIN rooms r ON r.id = a.room_id
		 `+statusFilter+`
		 WHERE (t.name LIKE ? ESCAPE '\' OR s.name LIKE ? ESCAPE '\') `+roundFilter+`
		 ORDER BY r.name, t.name, s.name
		 LIMIT 100`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DraftAllocation
	for rows.Next() {
		d, err := scanDraftAllocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
