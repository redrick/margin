package main

import (
	"strings"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/state"
)

type DiscussionLocation struct {
	station string
	Part    int `json:"part"`
	At      int `json:"at"`
}

func locateDiscussionLine(d *doc.Doc, q state.Question) DiscussionLocation {
	return locateDiscussionCode(d, q.Station, q.File, q.Side, func(lines []string) int {
		at := q.Line - 1
		if at < 0 {
			return -1
		}
		if at < len(lines) && matchesDiscussionLine(lines, at, q.Needle) {
			return at
		}
		return uniqueDiscussionLine(len(lines), q.Needle, func(i int) bool {
			return strings.TrimSpace(lines[i]) == q.Needle
		})
	})
}

func locateDiscussionCode(d *doc.Doc, station, file, side string, find func([]string) int) DiscussionLocation {
	lost := DiscussionLocation{Part: -1, At: -1}
	_, original := d.Station(station)
	if original == nil && !d.HasBase {
		return lost
	}
	partMatches := func(p *doc.Part) bool {
		return p.Err == nil && p.Spec.File == file && p.Base == (side == "base")
	}
	var at = -1
	for _, s := range d.Stations {
		for _, p := range s.Parts {
			if partMatches(p) {
				at = find(p.Lines)
				break
			}
		}
		if at >= 0 {
			break
		}
	}
	if at < 0 {
		return lost
	}
	fallback := lost
	if original != nil {
		for i, p := range original.Parts {
			if !partMatches(p) {
				continue
			}
			location := DiscussionLocation{station: station, Part: i, At: at}
			if p.Range.Start <= at && at <= p.Range.End {
				return location
			}
			if fallback.station == "" {
				fallback = location
			}
		}
	}
	if d.HasBase {
		for _, s := range d.Stations {
			for i, p := range s.Parts {
				if partMatches(p) && p.Range.Start <= at && at <= p.Range.End {
					return DiscussionLocation{station: s.ID, Part: i, At: at}
				}
			}
		}
	}
	return fallback
}

func uniqueDiscussionLine(count int, excerpt string, matches func(int) bool) int {
	if strings.TrimSpace(excerpt) == "" {
		return -1
	}
	at := -1
	for i := 0; i < count; i++ {
		if matches(i) {
			if at >= 0 {
				return -1
			}
			at = i
		}
	}
	return at
}

func matchesDiscussionLine(lines []string, at int, needle string) bool {
	line := strings.TrimSpace(lines[at])
	// A comment on a blank line stores the nearest nonblank line above as its anchor.
	if line == "" && needle != "" {
		for at > 0 && line == "" {
			at--
			line = strings.TrimSpace(lines[at])
		}
	}
	return line == needle
}
