// Command seed loads mock data for local testing: 6 teams, one concluded
// round (side-bias history), and one open draft. Skips when teams exist.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"cd-debate-tab/internal/database"
	"cd-debate-tab/internal/draw"
	"cd-debate-tab/internal/store"
)

var teams = [][3]string{
	{"Aurora", "M. Chen", "J. Okafor"},
	{"Boreal", "A. Haddad", "S. Lindqvist"},
	{"Cinder", "T. Mwangi", "R. Patel"},
	{"Drift", "L. Novak", "E. Garcia"},
	{"Ember", "K. Tan", "D. Adeyemi"},
	{"Flint", "N. Rossi", "P. Kim"},
}

func main() {
	path := "debate.db"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	db, err := database.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	st := store.New(db)
	if err := st.InitSchema(ctx); err != nil {
		log.Fatal(err)
	}
	existing, err := st.ListTeamsWithSpeakers(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if len(existing) > 0 {
		fmt.Println("already seeded, skipping")
		return
	}
	for _, tm := range teams {
		if _, err := st.CreateTeam(ctx, tm[0], tm[1], tm[2]); err != nil {
			log.Fatal(err)
		}
	}
	svc := draw.CryptoDefaults(st)
	r1, err := st.CreateRound(ctx, "Round 1", 1, 2)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := svc.Generate(ctx, r1.ID); err != nil {
		log.Fatal(err)
	}
	if err := svc.Publish(ctx, r1.ID); err != nil {
		log.Fatal(err)
	}
	if _, err := st.Conclude(ctx, r1.ID); err != nil {
		log.Fatal(err)
	}
	r2, err := st.CreateRound(ctx, "Round 2", 2, 2)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := svc.Generate(ctx, r2.ID); err != nil {
		log.Fatal(err)
	}
	fmt.Println("seeded 6 teams, Round 1 concluded, Round 2 draft")
}
