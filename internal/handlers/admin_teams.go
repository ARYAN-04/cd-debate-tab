package handlers

import (
	"encoding/csv"
	"html/template"
	"net/http"
	"strings"

	"cd-debate-tab/internal/auth"
	"cd-debate-tab/internal/draw"
	"cd-debate-tab/internal/store"
)

// AdminTeams routes team import/toggle/substitute flows.
type AdminTeams struct {
	Store *store.Store
	Tmpl  *template.Template
}

// NewAdminTeams wires deps for the admin team routes.
func NewAdminTeams(s *store.Store, t *template.Template) AdminTeams {
	return AdminTeams{Store: s, Tmpl: t}
}

// RegisterAdminTeams wires admin team routes onto mux.
func RegisterAdminTeams(mux *http.ServeMux, a AdminTeams) {
	g := func(h http.HandlerFunc) http.Handler { return auth.RequireAuth(a.Store, auth.CSRFProtect(h)) }
	mux.Handle("GET /admin/teams", g(a.Index))
	mux.Handle("POST /admin/teams/import", g(a.Import))
	mux.Handle("POST /admin/teams/manual-batch", g(a.ManualBatch))
	mux.Handle("POST /admin/teams/add", g(a.Add))
	mux.Handle("POST /admin/teams/toggle-active", g(a.ToggleActive))
	mux.Handle("POST /admin/teams/{teamID}/substitute-speaker", g(a.Substitute))
	mux.Handle("PATCH /admin/speakers/{speakerID}/redact", g(a.Redact))
}

// Index lists teams with speakers.
func (a AdminTeams) Index(w http.ResponseWriter, r *http.Request) {
	teams, err := a.Store.ListTeamsWithSpeakers(r.Context())
	if err != nil {
		httpErr(w, 500, "list failed")
		return
	}
	render(w, a.Tmpl, "teams", map[string]any{
		"Teams":     teams,
		"CSRFToken": auth.CSRFToken(r),
	})
}

// Add creates one team from the manual form (same rules as CSV rows).
func (a AdminTeams) Add(w http.ResponseWriter, r *http.Request) {
	fail := func(reason string) {
		teams, err := a.Store.ListTeamsWithSpeakers(r.Context())
		if err != nil {
			httpErr(w, 500, "list failed")
			return
		}
		render(w, a.Tmpl, "teams", map[string]any{
			"Teams":     teams,
			"Err":       reason,
			"CSRFToken": auth.CSRFToken(r),
		})
	}
	team, s1, s2 := strings.TrimSpace(r.FormValue("team_name")),
		strings.TrimSpace(r.FormValue("speaker1")), strings.TrimSpace(r.FormValue("speaker2"))
	if team == "" || s1 == "" || s2 == "" {
		fail("team and both speakers are required")
		return
	}
	if strings.EqualFold(s1, s2) {
		fail("speakers must not be identical")
		return
	}
	if _, err := a.Store.CreateTeam(r.Context(), team, s1, s2); err != nil {
		fail("could not add team (duplicate name?)")
		return
	}
	http.Redirect(w, r, "/admin/teams", http.StatusSeeOther)
}

// Import parses CSV, inserts valid rows in one transaction, renders import_errors on row errors.
func (a AdminTeams) Import(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, draw.MaxCSVBytes+(64<<10))
	f, _, err := r.FormFile("csv")
	if err != nil {
		httpErr(w, 400, "csv required")
		return
	}
	defer f.Close()
	valid, errs, err := draw.ParseCSV(f)
	if err != nil {
		httpErr(w, 400, "parse failed")
		return
	}
	items := make([]store.ImportTeam, len(valid))
	for i, v := range valid {
		items[i] = store.ImportTeam{
			Line:     v.Line,
			Name:     v.Team,
			Speaker1: v.S1,
			Speaker2: v.S2,
		}
	}
	conflicts, err := a.Store.CreateTeamsBatch(r.Context(), items)
	if err != nil {
		httpErr(w, 500, "import failed")
		return
	}
	for _, c := range conflicts {
		errs = append(errs, draw.RowError{Line: c.Line, Raw: c.Name, Reason: "duplicate team"})
	}
	if len(errs) > 0 {
		render(w, a.Tmpl, "import_errors", map[string]any{
			"Errs":      errs,
			"CSRFToken": auth.CSRFToken(r),
		})
		return
	}
	http.Redirect(w, r, "/admin/teams", http.StatusSeeOther)
}

// ManualBatch inserts inline-corrected rows from import_errors.html.
func (a AdminTeams) ManualBatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httpErr(w, 400, "bad form")
		return
	}
	var errs []draw.RowError
	for i, raw := range r.Form["row"] {
		rec, err := csv.NewReader(strings.NewReader(raw)).Read()
		if err != nil || len(rec) != 3 {
			errs = append(errs, draw.RowError{Line: i + 1, Raw: raw, Reason: "want Team,Speaker 1,Speaker 2"})
			continue
		}
		if _, err := a.Store.CreateTeam(r.Context(), strings.TrimSpace(rec[0]), strings.TrimSpace(rec[1]), strings.TrimSpace(rec[2])); err != nil {
			errs = append(errs, draw.RowError{Line: i + 1, Raw: raw, Reason: err.Error()})
		}
	}
	if len(errs) > 0 {
		render(w, a.Tmpl, "import_errors", map[string]any{
			"Errs":      errs,
			"CSRFToken": auth.CSRFToken(r),
		})
		return
	}
	http.Redirect(w, r, "/admin/teams", http.StatusSeeOther)
}

// ToggleActive flips elimination status for a team.
func (a AdminTeams) ToggleActive(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("team_id")
	if id == "" {
		httpErr(w, 400, "team_id required")
		return
	}
	teams, err := a.Store.ListTeamsWithSpeakers(r.Context())
	if err != nil {
		httpErr(w, 500, "list failed")
		return
	}
	var row *store.TeamWithSpeakers
	for i, tw := range teams {
		if tw.Team.ID == id {
			row = &teams[i]
			break
		}
	}
	if row == nil {
		httpErr(w, 404, "team not found")
		return
	}
	if err := a.Store.SetTeamActive(r.Context(), id, !row.Team.IsActive); err != nil {
		httpErr(w, 500, "toggle failed")
		return
	}
	row.Team.IsActive = !row.Team.IsActive
	// HTMX swaps a row fragment; plain posts fall back to a full reload.
	if r.Header.Get("HX-Request") == "true" {
		render(w, a.Tmpl, "team_row", *row)
		return
	}
	http.Redirect(w, r, "/admin/teams", http.StatusSeeOther)
}

// Substitute swaps a speaker, re-pointing open draft allocations.
func (a AdminTeams) Substitute(w http.ResponseWriter, r *http.Request) {
	teamID := r.PathValue("teamID")
	oldID, name := r.FormValue("old_speaker_id"), r.FormValue("new_name")
	if teamID == "" || oldID == "" || name == "" {
		httpErr(w, 400, "team, old speaker and new name required")
		return
	}
	newID, err := store.NewID()
	if err != nil {
		httpErr(w, 500, "id failed")
		return
	}
	err = a.Store.SubstituteSpeaker(r.Context(), teamID, oldID, newID, name)
	if err != nil {
		httpErr(w, 500, "substitute failed")
		return
	}
	http.Redirect(w, r, "/admin/teams", http.StatusSeeOther)
}

// Redact renames a speaker in place via hx-patch.
func (a AdminTeams) Redact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("speakerID")
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = strings.TrimSpace(r.Header.Get("HX-Prompt"))
	}
	if id == "" || name == "" {
		httpErr(w, 400, "speaker and name required")
		return
	}
	if err := a.Store.RedactSpeaker(r.Context(), id, name); err != nil {
		httpErr(w, 500, "redact failed")
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(name))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
