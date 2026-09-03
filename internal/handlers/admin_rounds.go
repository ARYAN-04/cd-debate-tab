package handlers

import (
	"bytes"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"cd-debate-tab/internal/auth"
	"cd-debate-tab/internal/draw"
	"cd-debate-tab/internal/store"
	"cd-debate-tab/internal/stream"
)

// AdminRounds routes round draft/publish flows.
type AdminRounds struct {
	Store *store.Store
	Draw  *draw.DrawService
	Tmpl  *template.Template
	Hub   *stream.Hub
}

// NewAdminRounds wires deps for the admin round routes.
func NewAdminRounds(s *store.Store, d *draw.DrawService, t *template.Template, h *stream.Hub) AdminRounds {
	return AdminRounds{Store: s, Draw: d, Tmpl: t, Hub: h}
}

// RegisterAdminRounds wires admin round routes onto mux.
func RegisterAdminRounds(mux *http.ServeMux, a AdminRounds) {
	g := func(h http.HandlerFunc) http.Handler { return auth.RequireAuth(a.Store, h) }
	mux.Handle("GET /admin/rounds", g(a.Index))
	mux.Handle("POST /admin/rounds", g(a.Create))
	mux.Handle("POST /admin/rounds/generate", g(a.Generate))
	mux.Handle("GET /admin/rounds/{roundID}/draft", g(a.Draft))
	mux.Handle("POST /admin/rounds/{roundID}/move-team", g(a.MoveTeam))
	mux.Handle("POST /admin/rounds/{roundID}/teams/{teamID}/flip-sides", g(a.FlipSides))
	mux.Handle("POST /admin/rounds/{roundID}/publish", g(a.Publish))
	mux.Handle("POST /admin/rounds/{roundID}/conclude", g(a.Conclude))
	mux.Handle("POST /admin/rounds/{roundID}/visibility", g(a.Visibility))
}

// RoundRow pairs a round with whether it has a generated draft to open.
type RoundRow struct {
	Round    store.Round
	HasDraft bool
}

// Index lists rounds with a creator form. ?hide=done filters finished rounds.
func (a AdminRounds) Index(w http.ResponseWriter, r *http.Request) {
	rounds, err := a.Store.ListRounds(r.Context())
	if err != nil {
		httpErr(w, 500, "list failed")
		return
	}
	hideDone := r.URL.Query().Get("hide") == "done"
	rows := make([]RoundRow, 0, len(rounds))
	done := 0
	for _, rd := range rounds {
		if rd.Status != "draft" {
			done++
			if hideDone {
				continue
			}
		}
		has, err := a.Store.HasDraft(r.Context(), rd.ID)
		if err != nil {
			httpErr(w, 500, "list failed")
			return
		}
		rows = append(rows, RoundRow{Round: rd, HasDraft: has})
	}
	render(w, a.Tmpl, "rounds", map[string]any{"Rounds": rows, "HideDone": hideDone, "DoneCount": done})
}

// Create stores a new draft round. A taken order re-renders the list with
// a choice: shift later rounds forward, or pick another order.
func (a AdminRounds) Create(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	order, _ := strconv.Atoi(r.FormValue("round_order"))
	rooms, _ := strconv.Atoi(r.FormValue("num_rooms"))
	if r.FormValue("mode") == "shift" {
		if err := a.Store.ShiftRoundsFrom(r.Context(), order); err != nil {
			httpErr(w, 500, "shift failed")
			return
		}
	}
	if _, err := a.Store.CreateRound(r.Context(), name, order, rooms); err != nil {
		if r.FormValue("mode") != "shift" && isConflict(err) {
			a.renderIndexWithConflict(w, r, name, order, rooms)
			return
		}
		httpErr(w, 500, "create failed")
		return
	}
	http.Redirect(w, r, "/admin/rounds", http.StatusSeeOther)
}

// conflictForm carries a taken order back to the list for resolution.
type conflictForm struct {
	Name     string
	Order    int
	NumRooms int
	Existing string
}

// isConflict reports UNIQUE violations (taken round order or name).
func isConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE")
}

// renderIndexWithConflict re-renders the rounds list with a shift-or-repick
// choice for the taken order.
func (a AdminRounds) renderIndexWithConflict(w http.ResponseWriter, r *http.Request, name string, order, rooms int) {
	rounds, err := a.Store.ListRounds(r.Context())
	if err != nil {
		httpErr(w, 500, "list failed")
		return
	}
	rows := make([]RoundRow, 0, len(rounds))
	existing := ""
	done := 0
	for _, rd := range rounds {
		has, err := a.Store.HasDraft(r.Context(), rd.ID)
		if err != nil {
			httpErr(w, 500, "list failed")
			return
		}
		if rd.RoundOrder == order {
			existing = rd.Name
		}
		if rd.Status != "draft" {
			done++
		}
		rows = append(rows, RoundRow{Round: rd, HasDraft: has})
	}
	render(w, a.Tmpl, "rounds", map[string]any{"Rounds": rows, "HideDone": false, "DoneCount": done,
		"Conflict": conflictForm{Name: name, Order: order, NumRooms: rooms, Existing: existing}})
}

// Visibility hides or unhides a round from the public draw.
// Status is untouched: hiding never unpublishes.
func (a AdminRounds) Visibility(w http.ResponseWriter, r *http.Request) {
	hide := r.FormValue("hidden") == "1" || r.FormValue("hidden") == "true"
	if err := a.Store.SetRoundHidden(r.Context(), r.PathValue("roundID"), hide); err != nil {
		httpErr(w, 500, "visibility failed")
		return
	}
	http.Redirect(w, r, "/admin/rounds", http.StatusSeeOther)
}

// Generate runs chunking+balancing and persists the draft via the service.
func (a AdminRounds) Generate(w http.ResponseWriter, r *http.Request) {
	roundID := r.FormValue("round_id")
	if roundID == "" {
		httpErr(w, 400, "round_id required")
		return
	}
	if _, err := a.Draw.Generate(r.Context(), roundID); err != nil {
		httpErr(w, 500, "generate failed")
		return
	}
	http.Redirect(w, r, "/admin/rounds/"+roundID+"/draft", http.StatusSeeOther)
}

// Draft renders the SortableJS board with imbalance badge when Δ>1.
func (a AdminRounds) Draft(w http.ResponseWriter, r *http.Request) {
	roundID := r.PathValue("roundID")
	round, err := a.Store.GetRound(r.Context(), roundID)
	if err != nil {
		httpErr(w, 404, "round not found")
		return
	}
	allocs, err := a.Store.GetDraftAllocations(r.Context(), roundID)
	if err != nil {
		httpErr(w, 500, "draft failed")
		return
	}
	rooms := GroupDraft(allocs)
	d, bad := Imbalance(rooms)
	render(w, a.Tmpl, "draft", DraftData{Round: round, Rooms: rooms, Imbalanced: bad, Delta: d})
}

// draftCanvas re-renders the #draft-canvas fragment after mutations.
func (a AdminRounds) draftCanvas(w http.ResponseWriter, r *http.Request, roundID string) {
	round, err := a.Store.GetRound(r.Context(), roundID)
	if err != nil {
		httpErr(w, 404, "round not found")
		return
	}
	allocs, err := a.Store.GetDraftAllocations(r.Context(), roundID)
	if err != nil {
		httpErr(w, 500, "draft failed")
		return
	}
	rooms := GroupDraft(allocs)
	d, bad := Imbalance(rooms)
	render(w, a.Tmpl, "draft_canvas", DraftData{Round: round, Rooms: rooms, Imbalanced: bad, Delta: d})
}

// MoveTeam relocates a team and returns the #draft-canvas fragment.
func (a AdminRounds) MoveTeam(w http.ResponseWriter, r *http.Request) {
	roundID := r.PathValue("roundID")
	err := a.Draw.MoveTeam(r.Context(), roundID, r.FormValue("team_id"), r.FormValue("target_room_id"))
	if err != nil {
		httpErr(w, 400, "move failed")
		return
	}
	a.draftCanvas(w, r, roundID)
}

// FlipSides inverts a team's sides and returns the #draft-canvas fragment.
func (a AdminRounds) FlipSides(w http.ResponseWriter, r *http.Request) {
	roundID := r.PathValue("roundID")
	err := a.Draw.FlipSides(r.Context(), roundID, r.PathValue("teamID"))
	if err != nil {
		httpErr(w, 400, "flip failed")
		return
	}
	a.draftCanvas(w, r, roundID)
}

// Publish locks the draft (guarded) and broadcasts the SSE frame.
func (a AdminRounds) Publish(w http.ResponseWriter, r *http.Request) {
	roundID := r.PathValue("roundID")
	if err := a.Draw.Publish(r.Context(), roundID); err != nil {
		httpErr(w, 409, "publish failed")
		return
	}
	allocs, _ := a.Store.GetDraftAllocations(r.Context(), roundID)
	var buf bytes.Buffer
	_ = a.Tmpl.ExecuteTemplate(&buf, "draw_grid", map[string]any{"Rooms": GroupDraw(allocs)})
	a.Hub.Broadcast(stream.EncodeSSEFrame("draw-published", buf.String()))
	http.Redirect(w, r, "/admin/rounds/"+roundID+"/draft", http.StatusSeeOther)
}

// Conclude freezes a published round permanently.
func (a AdminRounds) Conclude(w http.ResponseWriter, r *http.Request) {
	if _, err := a.Store.Conclude(r.Context(), r.PathValue("roundID")); err != nil {
		httpErr(w, 409, "conclude failed")
		return
	}
	http.Redirect(w, r, "/admin/rounds", http.StatusSeeOther)
}
