package draw

import (
	"crypto/rand"
	"errors"
	"math/big"
)

// ErrInvalidRooms rejects K <= 0 chunking input.
var ErrInvalidRooms = errors.New("num_rooms must be > 0")

// DefaultShuffle returns a uniform int in [0, n) via crypto/rand.
// It is the production Shuffle; tests inject a stub.
func DefaultShuffle(n int) int {
	if n <= 1 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}

// ChunkTeams splits teamIDs into k rooms: K=min(K,N), rejects K<=0.
// It Fisher-Yates shuffles a copy using shuffle (DefaultShuffle when nil),
// then deals base=N/K teams per room plus one extra to the first rem=N%K.
func ChunkTeams(teamIDs []string, k int, shuffle ...func(n int) int) ([][]string, error) {
	if k <= 0 {
		return nil, ErrInvalidRooms
	}
	n := len(teamIDs)
	if n == 0 {
		return nil, nil
	}
	if k > n {
		k = n
	}
	sh := DefaultShuffle
	if len(shuffle) > 0 && shuffle[0] != nil {
		sh = shuffle[0]
	}
	ids := append([]string(nil), teamIDs...)
	for i := n - 1; i > 0; i-- {
		j := sh(i + 1)
		j = ((j % (i + 1)) + (i + 1)) % (i + 1)
		ids[i], ids[j] = ids[j], ids[i]
	}
	base := n / k
	rem := n % k
	rooms := make([][]string, 0, k)
	idx := 0
	for r := 0; r < k; r++ {
		size := base
		if r < rem {
			size++
		}
		rooms = append(rooms, append([]string(nil), ids[idx:idx+size]...))
		idx += size
	}
	return rooms, nil
}

// AssignSides assigns for/against by bias: higher bias gets 'against'
// (balancing future sides); ties use coinFlip (true => side1 'for').
// A nil coinFlip falls back to crypto/rand.
func AssignSides(bias1, bias2 int, coinFlip func() bool) (side1, side2 string) {
	switch {
	case bias1 > bias2:
		return "against", "for"
	case bias1 < bias2:
		return "for", "against"
	default:
		flip := false
		if coinFlip != nil {
			flip = coinFlip()
		} else {
			flip = DefaultShuffle(2) == 1
		}
		if flip {
			return "for", "against"
		}
		return "against", "for"
	}
}
