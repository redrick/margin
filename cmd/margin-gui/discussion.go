package main

import (
	"strings"

	"github.com/redrick/margin/internal/doc"
	"github.com/redrick/margin/internal/state"
)

type DiscussionLocation struct {
	Part int `json:"part"`
	At   int `json:"at"`
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
	_, s := d.Station(station)
	if s == nil {
		return lost
	}
	var parts []int
	for i, p := range s.Parts {
		if p.Err == nil && p.Spec.File == file && p.Base == (side == "base") {
			parts = append(parts, i)
		}
	}
	if len(parts) == 0 {
		return lost
	}
	at := find(s.Parts[parts[0]].Lines)
	if at < 0 {
		return lost
	}
	for _, i := range parts {
		p := s.Parts[i]
		if p.Range.Start <= at && at <= p.Range.End {
			return DiscussionLocation{Part: i, At: at}
		}
	}
	return DiscussionLocation{Part: parts[0], At: at}
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
