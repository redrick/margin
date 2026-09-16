package tui

import (
	"fmt"
	"strings"

	"github.com/redrick/margin/internal/doc"
)

// trackNotes remembers which notes exist and marks the ones that appeared since the last load, so
// notes the agent adds to other stations are not silently hidden behind the current screen.
func (m *Model) trackNotes(d *doc.Doc) (added int, stations []string) {
	keys := map[string]bool{}
	for _, st := range d.Stations {
		listed := false
		for _, n := range st.Notes {
			keys[n.Key] = true
			if m.seen != nil && !m.seen[n.Key] {
				m.fresh[n.Key] = true
				added++
				if !listed {
					stations = append(stations, st.ID)
					listed = true
				}
			}
		}
	}
	for k := range m.fresh {
		if !keys[k] {
			delete(m.fresh, k)
		}
	}
	m.seen = keys
	return added, stations
}

func freshStatus(added int, stations []string) string {
	noun := "notes"
	if added == 1 {
		noun = "note"
	}
	return fmt.Sprintf("%d new %s in %s · N jumps there", added, noun, strings.Join(stations, ", "))
}

func (m *Model) markSeen() {
	if n := m.selectedNote(); n != nil {
		delete(m.fresh, n.Key)
	}
}

func (m *Model) nextFresh() {
	if m.doc == nil || len(m.fresh) == 0 {
		m.setStatus(false, "no new notes")
		return
	}
	count := len(m.doc.Stations)
	for step := 0; step < count; step++ {
		si := (m.station + step) % count
		for i, n := range m.doc.Stations[si].Notes {
			if !m.fresh[n.Key] {
				continue
			}
			if si != m.station {
				m.setStation(si)
			}
			m.gotoNote(i)
			return
		}
	}
}
