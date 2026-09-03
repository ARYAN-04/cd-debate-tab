package draw

import (
	"reflect"
	"testing"
)

// identity preserves input order: Fisher-Yates calls sh(i+1), returning i.
func identity(n int) int { return n - 1 }

func TestChunkDistribution(t *testing.T) {
	ids := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7"}
	rooms, err := ChunkTeams(ids, 3, identity)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"t1", "t2", "t3"}, {"t4", "t5"}, {"t6", "t7"}}
	if !reflect.DeepEqual(rooms, want) {
		t.Fatalf("got %v want %v", rooms, want)
	}
}

func TestChunkClamp(t *testing.T) {
	rooms, err := ChunkTeams([]string{"a", "b"}, 5, identity)
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 2 || len(rooms[0]) != 1 || len(rooms[1]) != 1 {
		t.Fatalf("clamp failed: %v", rooms)
	}
}

func TestChunkReject(t *testing.T) {
	for _, k := range []int{0, -1} {
		if _, err := ChunkTeams([]string{"a"}, k, identity); err == nil {
			t.Fatalf("k=%d: want error", k)
		}
	}
}

func TestAssignSidesBias(t *testing.T) {
	if s1, s2 := AssignSides(3, 1, nil); s1 != "against" || s2 != "for" {
		t.Fatalf("got %s/%s", s1, s2)
	}
	if s1, s2 := AssignSides(1, 3, nil); s1 != "for" || s2 != "against" {
		t.Fatalf("got %s/%s", s1, s2)
	}
}

func TestAssignSidesTieDeterministic(t *testing.T) {
	if s1, s2 := AssignSides(2, 2, func() bool { return true }); s1 != "for" || s2 != "against" {
		t.Fatalf("true: got %s/%s", s1, s2)
	}
	if s1, s2 := AssignSides(2, 2, func() bool { return false }); s1 != "against" || s2 != "for" {
		t.Fatalf("false: got %s/%s", s1, s2)
	}
}
