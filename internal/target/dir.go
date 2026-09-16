package target

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/redrick/margin/internal/gitx"
)

// Location says where a review file lives. Reviews never go inside the reviewed repo.
type Location struct {
	Path    string
	Vault   string
	Project string
	Topic   string
}

// Locate picks the review directory: $MARGIN_DIR, else the vault of the Claude Code
// obsidian-memory skill when it is installed, else ~/.local/state/margin.
func Locate(t *Target) Location {
	if d := os.Getenv("MARGIN_DIR"); d != "" {
		return Location{Path: filepath.Join(d, repoDir(t.Repo), fileName(t, false))}
	}
	if vault := memoryVault(); vault != "" {
		project, topic := memoryProject(t.Repo), topicFor(t)
		return Location{
			Path:    filepath.Join(vault, project, "topics", topic, fileName(t, true)),
			Vault:   vault,
			Project: project,
			Topic:   topic,
		}
	}
	return Location{Path: filepath.Join(stateDir(), repoDir(t.Repo), fileName(t, false))}
}

// LocationOf describes an existing review path, recognising reviews kept in the memory vault.
func LocationOf(path string) Location {
	loc := Location{Path: path}
	vault := memoryVault()
	if vault == "" {
		return loc
	}
	rel, err := filepath.Rel(vault, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return loc
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) >= 4 && parts[1] == "topics" {
		loc.Vault, loc.Project, loc.Topic = vault, parts[0], parts[2]
	}
	return loc
}

func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "margin")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "margin")
	}
	return filepath.Join(home, ".local", "state", "margin")
}

func repoDir(repo string) string {
	sum := sha256.Sum256([]byte(repo))
	return slug(filepath.Base(repo)) + "-" + hex.EncodeToString(sum[:3])
}

func fileName(t *Target, vault bool) string {
	name := "commit-" + t.Name
	switch t.Mode {
	case Worktree:
		name = "worktree-" + gitx.Short(t.Base)
	case Branch:
		name = "branch-" + slug(t.Name)
	}
	if vault {
		name = "margin-" + name
	}
	return name + ".review.yaml"
}

var vaultRoot = regexp.MustCompile("\\*\\*Vault root:\\*\\*\\s*`([^`]+)`")

// memoryVault returns the vault root declared by the obsidian-memory skill, or "".
func memoryVault() string {
	home, _ := os.UserHomeDir()
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		if home == "" {
			return ""
		}
		dir = filepath.Join(home, ".claude")
	}
	data, err := os.ReadFile(filepath.Join(dir, "skills", "obsidian-memory", "SKILL.md"))
	if err != nil {
		return ""
	}
	m := vaultRoot.FindSubmatch(data)
	if m == nil {
		return ""
	}
	root := strings.TrimSpace(string(m[1]))
	if strings.HasPrefix(root, "~/") && home != "" {
		root = filepath.Join(home, root[2:])
	}
	if !filepath.IsAbs(root) {
		return ""
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Clean(root)
}

// memoryProject names the vault folder the way the skill does: the ai.memoryProject git setting,
// else the main repository's directory, so every worktree of a repo shares one folder.
func memoryProject(repo string) string {
	if out, err := gitx.Run(repo, "config", "--get", "ai.memoryProject"); err == nil {
		if p := safeName(string(out)); p != "" {
			return p
		}
	}
	if out, err := gitx.Run(repo, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		if p := safeName(filepath.Base(filepath.Dir(strings.TrimSpace(string(out))))); p != "" {
			return p
		}
	}
	if p := safeName(filepath.Base(repo)); p != "" {
		return p
	}
	return "unknown"
}

func topicFor(t *Target) string {
	switch t.Mode {
	case Commit:
		return "commit-" + t.Name
	case Branch:
		if s := safeName(slug(strings.TrimPrefix(t.Name, "origin/"))); s != "" {
			return s
		}
	case Worktree:
		out, err := gitx.Run(t.Repo, "symbolic-ref", "--quiet", "--short", "HEAD")
		name := strings.TrimSpace(string(out))
		switch {
		case err != nil, name == "main", name == "master", name == "trunk", name == "develop":
		default:
			if s := safeName(slug(name)); s != "" {
				return s
			}
		}
	}
	return "working-tree"
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func slug(s string) string { return strings.Trim(unsafeName.ReplaceAllString(s, "-"), "-") }

// safeName accepts s as a single path element, rejecting separators and dot-only names.
func safeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.Trim(s, ".") == "" || strings.ContainsAny(s, `/\`) {
		return ""
	}
	return s
}
