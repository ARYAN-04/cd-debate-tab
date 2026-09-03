// Package handlers holds thin HTTP handlers: parse -> service -> fragment.
package handlers

import (
	"html/template"
	"net/http"

	"cd-debate-tab/internal/store"
	tmplfs "cd-debate-tab/templates"
)

// LoadTemplates parses the embedded template FS (html/template escapes).
func LoadTemplates() (*template.Template, error) {
	return template.ParseFS(tmplfs.FS,
		"layout.html", "admin/*.html",
		"admin/partials/*.html", "public/*.html",
		"public/partials/*.html")
}

func render(w http.ResponseWriter, t *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.ExecuteTemplate(w, name, data)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	http.Error(w, msg, code)
}

// TeamCard groups a team's draft speakers inside one room.
type TeamCard struct {
	TeamID   string
	TeamName string
	Speakers []store.DraftAllocation
}

// RoomGroup is one dropzone column on the draft board.
type RoomGroup struct {
	ID    string
	Name  string
	Cards []TeamCard
}

// DraftData feeds draft.html / draft_canvas.html (+imbalance badge).
type DraftData struct {
	Round      store.Round
	Rooms      []RoomGroup
	Imbalanced bool
	Delta      int
}

// GroupDraft buckets flat allocations into room -> team cards.
func GroupDraft(allocs []store.DraftAllocation) []RoomGroup {
	ri, ti := map[string]int{}, map[string]map[string]int{}
	var rooms []RoomGroup
	for _, a := range allocs {
		i, ok := ri[a.Allocation.RoomID]
		if !ok {
			i = len(rooms)
			ri[a.Allocation.RoomID] = i
			ti[a.Allocation.RoomID] = map[string]int{}
			rooms = append(rooms, RoomGroup{ID: a.Allocation.RoomID, Name: a.RoomName})
		}
		j, ok := ti[a.Allocation.RoomID][a.Allocation.TeamID]
		if !ok {
			j = len(rooms[i].Cards)
			ti[a.Allocation.RoomID][a.Allocation.TeamID] = j
			rooms[i].Cards = append(rooms[i].Cards, TeamCard{TeamID: a.Allocation.TeamID, TeamName: a.TeamName})
		}
		rooms[i].Cards[j].Speakers = append(rooms[i].Cards[j].Speakers, a)
	}
	return rooms
}

// Imbalance returns max-min team counts and whether Δ>1.
func Imbalance(rooms []RoomGroup) (delta int, bad bool) {
	if len(rooms) == 0 {
		return 0, false
	}
	mx, mn := len(rooms[0].Cards), len(rooms[0].Cards)
	for _, r := range rooms[1:] {
		mx, mn = max(mx, len(r.Cards)), min(mn, len(r.Cards))
	}
	return mx - mn, mx-mn > 1
}

// DrawRow is one team on the public table: speaker names per side.
type DrawRow struct {
	Team    string
	For     string
	Against string
}

// DrawRoom groups draw rows under one room section.
type DrawRoom struct {
	Name string
	Rows []DrawRow
}

// GroupDraw buckets flat allocations into room sections with one row per
// team (for/against speaker names side by side).
func GroupDraw(allocs []store.DraftAllocation) []DrawRoom {
	ri := map[string]int{}
	var rooms []DrawRoom
	ti := map[string]map[string]int{}
	for _, a := range allocs {
		i, ok := ri[a.Allocation.RoomID]
		if !ok {
			i = len(rooms)
			ri[a.Allocation.RoomID] = i
			ti[a.Allocation.RoomID] = map[string]int{}
			rooms = append(rooms, DrawRoom{Name: a.RoomName})
		}
		j, ok := ti[a.Allocation.RoomID][a.Allocation.TeamID]
		if !ok {
			j = len(rooms[i].Rows)
			ti[a.Allocation.RoomID][a.Allocation.TeamID] = j
			rooms[i].Rows = append(rooms[i].Rows, DrawRow{Team: a.TeamName})
		}
		if a.Allocation.Side == "for" {
			rooms[i].Rows[j].For = a.Speaker
		} else {
			rooms[i].Rows[j].Against = a.Speaker
		}
	}
	return rooms
}
