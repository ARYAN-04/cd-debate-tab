package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	st := New(db)
	if err := st.InitSchema(ctx); err != nil {
		t.Fatal(err)
	}
	return st, ctx
}

func mustTeam(t *testing.T, st *Store, ctx context.Context, name, s1, s2 string) TeamWithSpeakers {
	t.Helper()
	tw, err := st.CreateTeam(ctx, name, s1, s2)
	if err != nil {
		t.Fatal(err)
	}
	if len(tw.Speakers) != 2 {
		t.Fatalf("want 2 speakers, got %d", len(tw.Speakers))
	}
	return tw
}

func mustRound(t *testing.T, st *Store, ctx context.Context, name string, order, rooms int) Round {
	t.Helper()
	r, err := st.CreateRound(ctx, name, order, rooms)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDoubleAllocRejected(t *testing.T) {
	st, ctx := testStore(t)
	tw := mustTeam(t, st, ctx, "Alpha", "Amy", "Abe")
	r := mustRound(t, st, ctx, "Round 1", 1, 1)
	room, err := st.CreateRoom(ctx, r.ID, "Room A")
	if err != nil {
		t.Fatal(err)
	}
	dup := []Allocation{
		{RoundID: r.ID, RoomID: room.ID, TeamID: tw.Team.ID, SpeakerID: tw.Speakers[0].ID, Side: "for"},
		{RoundID: r.ID, RoomID: room.ID, TeamID: tw.Team.ID, SpeakerID: tw.Speakers[0].ID, Side: "against"},
	}
	if err := st.SaveDraft(ctx, r.ID, []Room{{ID: room.ID, RoundID: r.ID, Name: room.Name}}, dup); err == nil {
		t.Fatal("want UNIQUE(round_id, speaker_id) violation, got nil")
	}
	got, err := st.GetDraftAllocations(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("failed SaveDraft must leave no rows, got %d", len(got))
	}
}

func TestPublishIdempotent(t *testing.T) {
	st, ctx := testStore(t)
	r := mustRound(t, st, ctx, "Round 1", 1, 1)
	ok, err := st.Publish(ctx, r.ID)
	if err != nil || !ok {
		t.Fatalf("first publish = %v, %v; want true, nil", ok, err)
	}
	ok, err = st.Publish(ctx, r.ID)
	if err != nil || ok {
		t.Fatalf("second publish = %v, %v; want false, nil", ok, err)
	}
	ok, err = st.Conclude(ctx, r.ID)
	if err != nil || !ok {
		t.Fatalf("conclude after publish = %v, %v; want true, nil", ok, err)
	}
	ok, err = st.Conclude(ctx, r.ID)
	if err != nil || ok {
		t.Fatalf("second conclude = %v, %v; want false, nil", ok, err)
	}
}

func TestSubstituteRepointsDraftKeepsHistory(t *testing.T) {
	st, ctx := testStore(t)
	tw := mustTeam(t, st, ctx, "Beta", "Bea", "Ben")
	oldID := tw.Speakers[0].ID

	r1 := mustRound(t, st, ctx, "Round 1", 1, 1)
	room1, _ := st.CreateRoom(ctx, r1.ID, "Room A")
	err := st.SaveDraft(ctx, r1.ID,
		[]Room{{ID: room1.ID, RoundID: r1.ID, Name: room1.Name}},
		[]Allocation{
			{RoundID: r1.ID, RoomID: room1.ID, TeamID: tw.Team.ID, SpeakerID: oldID, Side: "for"},
			{RoundID: r1.ID, RoomID: room1.ID, TeamID: tw.Team.ID, SpeakerID: tw.Speakers[1].ID, Side: "against"},
		})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := st.Publish(ctx, r1.ID); err != nil || !ok {
		t.Fatalf("publish r1 = %v, %v", ok, err)
	}

	r2 := mustRound(t, st, ctx, "Round 2", 2, 1)
	room2, _ := st.CreateRoom(ctx, r2.ID, "Room A")
	err = st.SaveDraft(ctx, r2.ID,
		[]Room{{ID: room2.ID, RoundID: r2.ID, Name: room2.Name}},
		[]Allocation{
			{RoundID: r2.ID, RoomID: room2.ID, TeamID: tw.Team.ID, SpeakerID: oldID, Side: "against"},
			{RoundID: r2.ID, RoomID: room2.ID, TeamID: tw.Team.ID, SpeakerID: tw.Speakers[1].ID, Side: "for"},
		})
	if err != nil {
		t.Fatal(err)
	}

	if err := st.SubstituteSpeaker(ctx, tw.Team.ID, oldID, "", "Bex"); err != nil {
		t.Fatal(err)
	}
	teams, err := st.ListTeamsWithSpeakers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var newID string
	for _, tws := range teams {
		if tws.Team.ID != tw.Team.ID {
			continue
		}
		for _, sp := range tws.Speakers {
			switch sp.ID {
			case oldID:
				if sp.IsActive {
					t.Fatal("old speaker still active")
				}
			default:
				if sp.IsActive && sp.Name == "Bex" {
					newID = sp.ID
				}
			}
		}
	}
	if newID == "" {
		t.Fatal("new active speaker not found")
	}
	draft, err := st.GetDraftAllocations(ctx, r2.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range draft {
		if d.Allocation.SpeakerID == oldID {
			t.Fatalf("draft still points at departed speaker %s", oldID)
		}
	}
	found := false
	for _, d := range draft {
		if d.Allocation.SpeakerID == newID && d.Allocation.Side == "against" {
			found = true
		}
	}
	if !found {
		t.Fatal("draft allocation not re-pointed to new speaker with side preserved")
	}
	published, err := st.GetDraftAllocations(ctx, r1.ID)
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, d := range published {
		if d.Allocation.SpeakerID == oldID {
			kept = true
		}
	}
	if !kept {
		t.Fatal("published history must stay on old speaker id")
	}
}

func TestToggleActive(t *testing.T) {
	st, ctx := testStore(t)
	tw := mustTeam(t, st, ctx, "Gamma", "Gus", "Gail")
	if err := st.SetTeamActive(ctx, tw.Team.ID, false); err != nil {
		t.Fatal(err)
	}
	teams, err := st.ListTeamsWithSpeakers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if teams[0].Team.IsActive {
		t.Fatal("team should be inactive")
	}
	if err := st.SetTeamActive(ctx, tw.Team.ID, true); err != nil {
		t.Fatal(err)
	}
	teams, err = st.ListTeamsWithSpeakers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !teams[0].Team.IsActive {
		t.Fatal("team should be active again")
	}
	if err := st.SetTeamActive(ctx, "nope", false); err == nil {
		t.Fatal("want error for unknown team")
	}
}

func TestBiasMap(t *testing.T) {
	st, ctx := testStore(t)
	tw := mustTeam(t, st, ctx, "Delta", "Dee", "Dan")
	s1, s2 := tw.Speakers[0].ID, tw.Speakers[1].ID

	r1 := mustRound(t, st, ctx, "Round 1", 1, 1)
	room1, _ := st.CreateRoom(ctx, r1.ID, "Room A")
	err := st.SaveDraft(ctx, r1.ID,
		[]Room{{ID: room1.ID, RoundID: r1.ID, Name: room1.Name}},
		[]Allocation{
			{RoundID: r1.ID, RoomID: room1.ID, TeamID: tw.Team.ID, SpeakerID: s1, Side: "for"},
			{RoundID: r1.ID, RoomID: room1.ID, TeamID: tw.Team.ID, SpeakerID: s2, Side: "against"},
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Publish(ctx, r1.ID); err != nil {
		t.Fatal(err)
	}

	// Draft rounds must not count toward bias.
	r2 := mustRound(t, st, ctx, "Round 2", 2, 1)
	room2, _ := st.CreateRoom(ctx, r2.ID, "Room A")
	err = st.SaveDraft(ctx, r2.ID,
		[]Room{{ID: room2.ID, RoundID: r2.ID, Name: room2.Name}},
		[]Allocation{
			{RoundID: r2.ID, RoomID: room2.ID, TeamID: tw.Team.ID, SpeakerID: s1, Side: "against"},
			{RoundID: r2.ID, RoomID: room2.ID, TeamID: tw.Team.ID, SpeakerID: s2, Side: "for"},
		})
	if err != nil {
		t.Fatal(err)
	}

	bias, err := st.BiasMap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bias[s1] != 1 || bias[s2] != -1 {
		t.Fatalf("bias = %v; want s1=1 s2=-1 (draft excluded)", bias)
	}
}

func TestSessionsAndSearch(t *testing.T) {
	st, ctx := testStore(t)
	if err := st.EnsureAdmin(ctx, "admin", "hash"); err != nil {
		t.Fatal(err)
	}
	a, err := st.GetAdminByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 64 {
		t.Fatalf("token len = %d, want 64", len(tok))
	}
	if err := st.CreateSession(ctx, tok, a.ID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetSessionByToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	expired, _ := NewSessionToken()
	if err := st.CreateSession(ctx, expired, a.ID, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetSessionByToken(ctx, expired); err == nil {
		t.Fatal("want expiry error, got nil")
	}
	if err := st.DeleteSession(ctx, tok); err != nil {
		t.Fatal(err)
	}

	tw := mustTeam(t, st, ctx, "Epsilon", "Eve_%", "Eli")
	r := mustRound(t, st, ctx, "Round 1", 1, 1)
	room, _ := st.CreateRoom(ctx, r.ID, "Room A")
	err = st.SaveDraft(ctx, r.ID,
		[]Room{{ID: room.ID, RoundID: r.ID, Name: room.Name}},
		[]Allocation{
			{RoundID: r.ID, RoomID: room.ID, TeamID: tw.Team.ID, SpeakerID: tw.Speakers[0].ID, Side: "for"},
			{RoundID: r.ID, RoomID: room.ID, TeamID: tw.Team.ID, SpeakerID: tw.Speakers[1].ID, Side: "against"},
		})
	if err != nil {
		t.Fatal(err)
	}
	// Literal % and _ must not act as wildcards.
	res, err := st.SearchAllocations(ctx, "%_")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Fatalf("escaped search matched %d rows, want 0", len(res))
	}
	res, err = st.SearchAllocations(ctx, "Eve_")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("search matched %d rows, want 1", len(res))
	}
}

func TestPublicSearchHidesDraft(t *testing.T) {
	st, ctx := testStore(t)
	tw := mustTeam(t, st, ctx, "Zeta", "Zed", "Zoe")
	r := mustRound(t, st, ctx, "Round 1", 1, 1)
	room, _ := st.CreateRoom(ctx, r.ID, "Room A")
	err := st.SaveDraft(ctx, r.ID,
		[]Room{{ID: room.ID, RoundID: r.ID, Name: room.Name}},
		[]Allocation{
			{RoundID: r.ID, RoomID: room.ID, TeamID: tw.Team.ID, SpeakerID: tw.Speakers[0].ID, Side: "for"},
			{RoundID: r.ID, RoomID: room.ID, TeamID: tw.Team.ID, SpeakerID: tw.Speakers[1].ID, Side: "against"},
		})
	if err != nil {
		t.Fatal(err)
	}
	pub, err := st.SearchPublicAllocations(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pub) != 0 {
		t.Fatalf("draft leaked to public search: %d rows", len(pub))
	}
	if ok, err := st.Publish(ctx, r.ID); err != nil || !ok {
		t.Fatalf("publish = %v, %v", ok, err)
	}
	pub, err = st.SearchPublicAllocations(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pub) != 2 {
		t.Fatalf("public search matched %d rows, want 2", len(pub))
	}
}

func TestLockedRoundImmutable(t *testing.T) {
	st, ctx := testStore(t)
	tw := mustTeam(t, st, ctx, "Eta", "Ed", "Ella")
	r := mustRound(t, st, ctx, "Round 1", 1, 2)
	room, _ := st.CreateRoom(ctx, r.ID, "Room A")
	room2, _ := st.CreateRoom(ctx, r.ID, "Room B")
	if err := st.SaveDraft(ctx, r.ID,
		[]Room{{ID: room.ID, RoundID: r.ID, Name: room.Name}, {ID: room2.ID, RoundID: r.ID, Name: room2.Name}},
		[]Allocation{
			{RoundID: r.ID, RoomID: room.ID, TeamID: tw.Team.ID, SpeakerID: tw.Speakers[0].ID, Side: "for"},
			{RoundID: r.ID, RoomID: room.ID, TeamID: tw.Team.ID, SpeakerID: tw.Speakers[1].ID, Side: "against"},
		}); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.Publish(ctx, r.ID); err != nil || !ok {
		t.Fatalf("publish = %v, %v", ok, err)
	}
	if err := st.MoveTeam(ctx, r.ID, tw.Team.ID, room2.ID); err != ErrRoundNotDraft {
		t.Fatalf("move on published round = %v, want ErrRoundNotDraft", err)
	}
	if err := st.FlipSides(ctx, r.ID, tw.Team.ID); err != ErrRoundNotDraft {
		t.Fatalf("flip on published round = %v, want ErrRoundNotDraft", err)
	}
}

func TestShiftRoundsFrom(t *testing.T) {
	st, ctx := testStore(t)
	mustRound(t, st, ctx, "R1", 1, 1)
	mustRound(t, st, ctx, "R2", 2, 1)
	if err := st.ShiftRoundsFrom(ctx, 1); err != nil {
		t.Fatal(err)
	}
	rounds, err := st.ListRounds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, r := range rounds {
		got[r.Name] = r.RoundOrder
	}
	if got["R1"] != 2 || got["R2"] != 3 {
		t.Fatalf("orders after shift = %v, want R1:2 R2:3", got)
	}
	if _, err := st.CreateRound(ctx, "R0", 1, 1); err != nil {
		t.Fatalf("create at freed order failed: %v", err)
	}
}

func TestPublicSearchScopesRound(t *testing.T) {
	st, ctx := testStore(t)
	tw := mustTeam(t, st, ctx, "Theta", "Ted", "Tess")
	mk := func(name string, order int) Round {
		r := mustRound(t, st, ctx, name, order, 1)
		room, _ := st.CreateRoom(ctx, r.ID, "Room A")
		if err := st.SaveDraft(ctx, r.ID,
			[]Room{{ID: room.ID, RoundID: r.ID, Name: room.Name}},
			[]Allocation{
				{RoundID: r.ID, RoomID: room.ID, TeamID: tw.Team.ID, SpeakerID: tw.Speakers[0].ID, Side: "for"},
				{RoundID: r.ID, RoomID: room.ID, TeamID: tw.Team.ID, SpeakerID: tw.Speakers[1].ID, Side: "against"},
			}); err != nil {
			t.Fatal(err)
		}
		if ok, err := st.Publish(ctx, r.ID); err != nil || !ok {
			t.Fatalf("publish %s = %v, %v", name, ok, err)
		}
		return r
	}
	r1 := mk("Round 1", 1)
	r2 := mk("Round 2", 2)
	pub, err := st.ListPublicRounds(ctx)
	if err != nil || len(pub) != 2 || pub[0].ID != r2.ID {
		t.Fatalf("public rounds = %+v, %v; want newest first", pub, err)
	}
	one, err := st.SearchPublicAllocations(ctx, "", r1.ID)
	if err != nil || len(one) != 2 {
		t.Fatalf("scoped search = %d rows, %v; want 2", len(one), err)
	}
	all, err := st.SearchPublicAllocations(ctx, "", "")
	if err != nil || len(all) != 4 {
		t.Fatalf("unscoped search = %d rows, %v; want 4", len(all), err)
	}
}
