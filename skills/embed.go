// Package skills holds the agent skills margin ships, embedded so the binaries can install them.
package skills

import _ "embed"

//go:embed margin/SKILL.md
var Margin []byte
