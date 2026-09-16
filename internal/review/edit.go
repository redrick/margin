package review

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/redrick/margin/internal/fsx"
)

// AppendNote adds a note to a station and returns its index within the station.
func AppendNote(path, stationID string, n Note) (int, error) {
	idx := 0
	err := edit(path, func(root *yaml.Node) error {
		st, err := stationNode(root, stationID)
		if err != nil {
			return err
		}
		notes := mapValue(st, "notes")
		if notes == nil {
			notes = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			st.Content = append(st.Content, scalar("notes"), notes)
		}
		if notes.Kind != yaml.SequenceNode {
			return fmt.Errorf("station %q notes is not a list", stationID)
		}
		node, err := encode(n)
		if err != nil {
			return err
		}
		notes.Style = 0
		notes.Content = append(notes.Content, node)
		idx = len(notes.Content) - 1
		return nil
	})
	return idx, err
}

func AppendStation(path string, s Station) error {
	return edit(path, func(root *yaml.Node) error {
		stations := mapValue(root, "stations")
		if stations == nil {
			stations = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			root.Content = append(root.Content, scalar("stations"), stations)
		}
		if stations.Kind != yaml.SequenceNode {
			return errors.New("stations is not a list")
		}
		node, err := encode(s)
		if err != nil {
			return err
		}
		stations.Style = 0
		stations.Content = append(stations.Content, node)
		return nil
	})
}

// SetScalar sets a top-level string field; an empty value removes it.
func SetScalar(path, key, value string) error {
	return edit(path, func(root *yaml.Node) error {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value != key {
				continue
			}
			if value == "" {
				root.Content = append(root.Content[:i], root.Content[i+2:]...)
			} else {
				root.Content[i+1] = scalar(value)
			}
			return nil
		}
		if value != "" {
			root.Content = append(root.Content, scalar(key), scalar(value))
		}
		return nil
	})
}

// edit rewrites the review through its YAML node tree so comments survive.
func edit(path string, fn func(root *yaml.Node) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: not a review document", path)
	}
	if err := fn(doc.Content[0]); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if _, err := Parse(path, buf.Bytes()); err != nil {
		return fmt.Errorf("refusing to write an invalid review: %w", err)
	}
	return fsx.WriteFileAtomic(path, buf.Bytes(), 0o644)
}

func stationNode(root *yaml.Node, id string) (*yaml.Node, error) {
	stations := mapValue(root, "stations")
	if stations == nil || stations.Kind != yaml.SequenceNode {
		return nil, errors.New("no stations list")
	}
	for _, s := range stations.Content {
		if v := mapValue(s, "id"); v != nil && v.Value == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no station %q", id)
}

func encode(v any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	literal(&n)
	return &n, nil
}

func literal(n *yaml.Node) {
	if n.Kind == yaml.ScalarNode && strings.Contains(n.Value, "\n") {
		n.Style = yaml.LiteralStyle
	}
	for _, c := range n.Content {
		literal(c)
	}
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
