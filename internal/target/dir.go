package target

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/redrick/margin/internal/config"
	"github.com/redrick/margin/internal/gitx"
)

// Location says where a review file lives and, for a notes folder, which project and topic it
// is filed under.
type Location struct {
	Path    string
	Notes   bool
	Project string
	Topic   string
	// AgentNote is the configured note for the agent, filled in for this review.
	AgentNote string
}

// Locate picks the review file for t from the settings: .margin/reviews in the repository, the
// state directory, or a configured directory.
func Locate(t *Target) Location {
	c := config.Load()
	var loc Location
	switch c.Reviews {
	case config.InRepo, config.InState:
		loc.Path = filepath.Join(reviewsDir(c, t.Repo), fileName(t, false))
	default:
		loc.Notes = config.NotesFolder(c.Reviews)
		if loc.Notes {
			loc.Project, loc.Topic = memoryProject(t.Repo), topicFor(t)
		}
		dir := strings.NewReplacer("{project}", loc.Project, "{topic}", loc.Topic, "{repo}", repoDir(t.Repo)).Replace(config.Expand(c.Reviews))
		loc.Path = filepath.Join(dir, fileName(t, loc.Notes))
	}
	loc.AgentNote = agentNote(c, loc)
	return loc
}

func reviewsDir(c config.Config, repo string) string {
	if c.Reviews == config.InRepo {
		return filepath.Join(mainWorktree(repo), ".margin", "reviews")
	}
	return filepath.Join(StateDir(), repoDir(repo))
}

// Existing lists the review files kept for the repository at repo, wherever the settings keep
// them. In a notes folder that is not split by project it may list other repositories' reviews.
func Existing(repo string) []string {
	c := config.Load()
	var pattern string
	switch c.Reviews {
	case config.InRepo, config.InState:
		pattern = filepath.Join(reviewsDir(c, repo), "*.review.yaml")
	default:
		notes := config.NotesFolder(c.Reviews)
		project := ""
		if strings.Contains(c.Reviews, "{project}") {
			project = memoryProject(repo)
		}
		dir := strings.NewReplacer("{project}", globEscape(project), "{topic}", "*", "{repo}", globEscape(repoDir(repo))).Replace(globEscape(config.Expand(c.Reviews)))
		name := "*.review.yaml"
		if notes {
			name = "margin-" + name
		}
		pattern = filepath.Join(dir, name)
	}
	paths, _ := filepath.Glob(pattern)
	return paths
}

var globMeta = strings.NewReplacer(`*`, `\*`, `?`, `\?`, `[`, `\[`, `\`, `\\`)

func globEscape(s string) string { return globMeta.Replace(s) }

// FileName is the name of the review file of a branch, or of the uncommitted (or staged) changes
// on top of head, as the settings name it; Existing lists the paths to look it up in.
func FileName(branch, head string, staged bool) string {
	t := &Target{Mode: Worktree, Base: head, Staged: staged}
	if branch != "" {
		t.Mode, t.Name = Branch, branch
	}
	c := config.Load()
	return fileName(t, c.Reviews != config.InRepo && c.Reviews != config.InState && config.NotesFolder(c.Reviews))
}

// DefaultBranch is the branch a branch review compares against when no base is given.
func DefaultBranch(repo string) (string, error) { return defaultBranch(repo) }

// LocationOf describes an existing review path, recognising project and topic when it lies in
// the configured notes folder.
func LocationOf(path string) Location {
	c := config.Load()
	loc := Location{Path: path}
	if config.NotesFolder(c.Reviews) {
		re := templateRegexp(config.Expand(c.Reviews))
		if m := re.FindStringSubmatch(filepath.Dir(path)); m != nil {
			loc.Notes = true
			for i, name := range re.SubexpNames() {
				switch name {
				case "project":
					loc.Project = m[i]
				case "topic":
					loc.Topic = m[i]
				}
			}
		}
	}
	loc.AgentNote = agentNote(c, loc)
	return loc
}

func agentNote(c config.Config, loc Location) string {
	return strings.TrimSpace(strings.NewReplacer("{project}", loc.Project, "{topic}", loc.Topic, "{dir}", filepath.Dir(loc.Path)).Replace(c.AgentNote))
}

// templateRegexp matches directories made from a reviews template; each placeholder is one path
// element, captured by name the first time it appears.
func templateRegexp(tmpl string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	seen := map[string]bool{}
	for rest := filepath.Clean(tmpl); rest != ""; {
		i := strings.IndexByte(rest, '{')
		j := strings.IndexByte(rest, '}')
		if i < 0 || j < i {
			b.WriteString(regexp.QuoteMeta(rest))
			break
		}
		b.WriteString(regexp.QuoteMeta(rest[:i]))
		name := rest[i+1 : j]
		if !seen[name] && (name == "project" || name == "topic") {
			seen[name] = true
			b.WriteString("(?P<" + name + ">[^/]+)")
		} else {
			b.WriteString("[^/]+")
		}
		rest = rest[j+1:]
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// mainWorktree is the main working tree of repo, so all its worktrees share one .margin/reviews.
func mainWorktree(repo string) string {
	out, err := gitx.Run(repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return repo
	}
	common := strings.TrimSpace(string(out))
	if filepath.Base(common) != ".git" {
		return repo
	}
	return filepath.Dir(common)
}

func StateDir() string {
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

func fileName(t *Target, notes bool) string {
	name := "commit-" + t.Name
	switch t.Mode {
	case Worktree:
		name = "worktree-" + gitx.Short(t.Base)
		if t.Staged {
			name = "staged-" + gitx.Short(t.Base)
		}
	case Branch:
		name = "branch-" + slug(t.Name)
	}
	if notes {
		name = "margin-" + name
	}
	return name + ".review.yaml"
}

// memoryProject names the project for a notes folder: the ai.memoryProject git setting, else the
// main repository's directory, so every worktree of a repo shares one folder.
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
