package draw

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// Limits: 1MB / 2000 rows; trim; case-insensitive speaker-dup check.
const (
	MaxCSVBytes = 1 << 20
	MaxCSVRows  = 2000
)

// ValidRow is one validated CSV line: Team Name, Speaker 1, Speaker 2.
type ValidRow struct {
	Line int
	Team string
	S1   string
	S2   string
}

// RowError carries a failing line for import_errors.html repair.
type RowError struct {
	Line   int
	Raw    string
	Reason string
}

// ParseCSV validates r and splits valid rows from row errors.
// Quoted commas are handled by encoding/csv. No DB access:
// duplicate team names surface later at insert time, not here.
func ParseCSV(r io.Reader) (valid []ValidRow, errs []RowError, err error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxCSVBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > MaxCSVBytes {
		return nil, nil, fmt.Errorf("csv exceeds %d bytes", MaxCSVBytes)
	}
	cr := csv.NewReader(strings.NewReader(string(data)))
	cr.FieldsPerRecord = -1
	rows := 0
	for {
		rec, rerr := cr.Read()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return valid, errs, rerr
		}
		line, _ := cr.FieldPos(0)
		rows++
		if rows > MaxCSVRows {
			return valid, errs, fmt.Errorf("csv exceeds %d rows", MaxCSVRows)
		}
		raw := strings.Join(rec, ",")
		if len(rec) != 3 {
			errs = append(errs, RowError{Line: line, Raw: raw, Reason: fmt.Sprintf("expected 3 columns, got %d", len(rec))})
			continue
		}
		team := strings.TrimSpace(rec[0])
		s1 := strings.TrimSpace(rec[1])
		s2 := strings.TrimSpace(rec[2])
		if team == "" || s1 == "" || s2 == "" {
			errs = append(errs, RowError{Line: line, Raw: raw, Reason: "each row must contain exactly 3 non-empty values"})
			continue
		}
		if strings.EqualFold(s1, s2) {
			errs = append(errs, RowError{Line: line, Raw: raw, Reason: "speaker names must not be identical"})
			continue
		}
		valid = append(valid, ValidRow{Line: line, Team: team, S1: s1, S2: s2})
	}
	return valid, errs, nil
}
