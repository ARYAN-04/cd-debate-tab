package draw

import (
	"strings"
	"testing"
)

func TestParseCSVValid(t *testing.T) {
	in := "Alpha,Alice,Bob\nBeta,Carol,Dave\n"
	valid, errs, err := ParseCSV(strings.NewReader(in))
	if err != nil || len(errs) != 0 || len(valid) != 2 {
		t.Fatalf("got valid=%v errs=%v err=%v", valid, errs, err)
	}
	if valid[0] != (ValidRow{Line: 1, Team: "Alpha", S1: "Alice", S2: "Bob"}) {
		t.Fatalf("row1: %+v", valid[0])
	}
}

func TestParseCSVTrim(t *testing.T) {
	valid, errs, err := ParseCSV(strings.NewReader("  Alpha , Alice ,  Bob  \n"))
	if err != nil || len(errs) != 0 || len(valid) != 1 {
		t.Fatalf("got valid=%v errs=%v err=%v", valid, errs, err)
	}
	if valid[0].Team != "Alpha" || valid[0].S1 != "Alice" || valid[0].S2 != "Bob" {
		t.Fatalf("untrimmed: %+v", valid[0])
	}
}

func TestParseCSVInvalid(t *testing.T) {
	in := "Only,Two\nA,B,C,D\nGamma,,Hank\n"
	valid, errs, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(valid) != 0 || len(errs) != 3 {
		t.Fatalf("got valid=%v errs=%v", valid, errs)
	}
	if errs[0].Line != 1 || errs[1].Line != 2 || errs[2].Line != 3 {
		t.Fatalf("lines: %+v", errs)
	}
}

func TestParseCSVDupSpeakers(t *testing.T) {
	_, errs, err := ParseCSV(strings.NewReader("Delta,Eve,eve\n"))
	if err != nil || len(errs) != 1 {
		t.Fatalf("got errs=%v err=%v", errs, err)
	}
}

func TestParseCSVQuotedComma(t *testing.T) {
	valid, errs, err := ParseCSV(strings.NewReader("\"Epsilon, Jr.\",Frank,Grace\n"))
	if err != nil || len(errs) != 0 || len(valid) != 1 {
		t.Fatalf("got valid=%v errs=%v err=%v", valid, errs, err)
	}
	if valid[0].Team != "Epsilon, Jr." {
		t.Fatalf("team: %q", valid[0].Team)
	}
}
