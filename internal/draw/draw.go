// Package draw implements the DrawService over store.Store.
package draw

import (
	"context"
	"errors"
	"fmt"

	"cd-debate-tab/internal/store"
)

// ErrTeamSize guards the exactly-2-active-speakers invariant.
var ErrTeamSize = errors.New("team must have exactly 2 active speakers")

// Draft is a generated but not yet persisted draw.
type Draft struct {
	RoundID string
	Allocs  []store.Allocation
}

// DrawService orchestrates chunking, side assignment, and persistence.
type DrawService struct {
	Store    *store.Store
	Shuffle  func(n int) int
	CoinFlip func() bool
}

// New wires a Store with injected shuffle/coin-flip for deterministic tests.
func New(st *store.Store, shuffle func(n int) int, coinFlip func() bool) *DrawService {
	return &DrawService{Store: st, Shuffle: shuffle, CoinFlip: coinFlip}
}

// CryptoDefaults wires crypto/rand-backed shuffle and coin flip.
func CryptoDefaults(st *store.Store) *DrawService {
	return New(st, DefaultShuffle, func() bool { return DefaultShuffle(2) == 1 })
}

func (d *DrawService) shuffleOrDefault() func(n int) int {
	if d.Shuffle != nil {
		return d.Shuffle
	}
	return DefaultShuffle
}

func (d *DrawService) coinFlipOrDefault() func() bool {
	if d.CoinFlip != nil {
		return d.CoinFlip
	}
	return func() bool { return DefaultShuffle(2) == 1 }
}

// Generate loads the round, active teams+speakers, and batched bias,
// chunks teams into rooms, assigns sides, and persists via SaveDraft.
func (d *DrawService) Generate(ctx context.Context, roundID string) (Draft, error) {
	round, err := d.Store.GetRound(ctx, roundID)
	if err != nil {
		return Draft{}, err
	}
	tws, err := d.Store.ListTeamsWithSpeakers(ctx)
	if err != nil {
		return Draft{}, err
	}
	bias, err := d.Store.BiasMap(ctx)
	if err != nil {
		return Draft{}, err
	}
	var order []string
	speakers := make(map[string][2]store.Speaker)
	for _, tw := range tws {
		if !tw.Team.IsActive {
			continue
		}
		var active []store.Speaker
		for _, sp := range tw.Speakers {
			if sp.IsActive {
				active = append(active, sp)
			}
		}
		if len(active) != 2 {
			return Draft{}, fmt.Errorf("%w: team %q has %d active speakers", ErrTeamSize, tw.Team.Name, len(active))
		}
		order = append(order, tw.Team.ID)
		speakers[tw.Team.ID] = [2]store.Speaker{active[0], active[1]}
	}
	rooms, err := ChunkTeams(order, round.NumRooms, d.shuffleOrDefault())
	if err != nil {
		return Draft{}, err
	}
	flip := d.coinFlipOrDefault()
	var roomsOut []store.Room
	var allocs []store.Allocation
	for i, chunk := range rooms {
		roomID, err := store.NewID()
		if err != nil {
			return Draft{}, err
		}
		roomsOut = append(roomsOut, store.Room{
			ID:      roomID,
			RoundID: roundID,
			Name:    fmt.Sprintf("Room %d", i+1),
		})
		for _, teamID := range chunk {
			sp := speakers[teamID]
			s1, s2 := AssignSides(bias[sp[0].ID], bias[sp[1].ID], flip)
			allocs = append(allocs,
				store.Allocation{RoundID: roundID, RoomID: roomID, TeamID: teamID, SpeakerID: sp[0].ID, Side: s1},
				store.Allocation{RoundID: roundID, RoomID: roomID, TeamID: teamID, SpeakerID: sp[1].ID, Side: s2},
			)
		}
	}
	if err := d.Store.SaveDraft(ctx, roundID, roomsOut, allocs); err != nil {
		return Draft{}, err
	}
	return Draft{RoundID: roundID, Allocs: allocs}, nil
}

// ensureTeamSize enforces exactly 2 active speakers before mutating a draft.
func (d *DrawService) ensureTeamSize(ctx context.Context, teamID string) error {
	n, err := d.Store.CountActiveSpeakers(ctx, teamID)
	if err != nil {
		return err
	}
	if n != 2 {
		return fmt.Errorf("%w: team %q has %d active speakers", ErrTeamSize, teamID, n)
	}
	return nil
}

func (d *DrawService) MoveTeam(ctx context.Context, roundID, teamID, targetRoomID string) error {
	if err := d.ensureTeamSize(ctx, teamID); err != nil {
		return err
	}
	return d.Store.MoveTeam(ctx, roundID, teamID, targetRoomID)
}

func (d *DrawService) FlipSides(ctx context.Context, roundID, teamID string) error {
	if err := d.ensureTeamSize(ctx, teamID); err != nil {
		return err
	}
	return d.Store.FlipSides(ctx, roundID, teamID)
}

func (d *DrawService) Publish(ctx context.Context, roundID string) error {
	_, err := d.Store.Publish(ctx, roundID)
	return err
}
