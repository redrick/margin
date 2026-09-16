package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

var shaRe = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

// Run runs a read-only git command in repo with prompts, optional locks and fsmonitor disabled.
func Run(repo string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-c", "core.fsmonitor=false", "-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "PAGER=cat")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func RevParse(repo, ref string) (string, error) {
	if ref == EmptyTree {
		return ref, nil
	}
	if ref == "" || strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("invalid ref %q", ref)
	}
	out, err := Run(repo, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%q is not a commit in %s", ref, repo)
	}
	return SHA(out)
}

func MergeBase(repo, a, b string) (string, error) {
	out, err := Run(repo, "merge-base", a, b)
	if err != nil {
		return "", err
	}
	return SHA(out)
}

func SHA(out []byte) (string, error) {
	sha := strings.TrimSpace(string(out))
	if !shaRe.MatchString(sha) {
		return "", fmt.Errorf("unexpected git output %q", sha)
	}
	return sha, nil
}

// ReadBlob reads path at commit sha; exists is false when the commit has no such path.
func ReadBlob(repo, sha, path string) ([]byte, bool, error) {
	spec := sha + ":" + path
	if _, err := Run(repo, "cat-file", "-e", spec); err != nil {
		return nil, false, nil
	}
	out, err := Run(repo, "cat-file", "blob", spec)
	return out, err == nil, err
}

func Short(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}
