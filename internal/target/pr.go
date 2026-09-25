package target

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/redrick/margin/internal/gitx"
	"github.com/redrick/margin/internal/text"
)

type pullRequest struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	URL         string `json:"url"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
}

var prRef = regexp.MustCompile(`^(?:#?\d+|https://[^\s]+/pull/\d+/?)$`)

// pullRequest reviews a GitHub pull request. gh is only asked to read it; its head commit has to be
// in the local repository already, since margin never fetches.
func (t *Target) pullRequest(ref, baseRef string) error {
	ref = strings.TrimSpace(ref)
	if !prRef.MatchString(ref) {
		return fmt.Errorf("--pr wants a pull request number or URL, got %q", ref)
	}
	pr, err := viewPR(t.Repo, strings.TrimPrefix(ref, "#"))
	if err != nil {
		return err
	}
	if !shaRe.MatchString(pr.HeadRefOid) {
		return fmt.Errorf("gh returned an unexpected head commit %q", pr.HeadRefOid)
	}
	tip, err := gitx.RevParse(t.Repo, pr.HeadRefOid)
	if err != nil {
		return fmt.Errorf("the head of PR #%d (%s) is not in this repository; fetch it first, for example:\n  git fetch origin pull/%d/head",
			pr.Number, gitx.Short(pr.HeadRefOid), pr.Number)
	}
	if baseRef == "" {
		baseRef = pr.BaseRefName
		if _, err := gitx.RevParse(t.Repo, "origin/"+baseRef); err == nil {
			baseRef = "origin/" + baseRef
		}
	}
	if strings.HasPrefix(baseRef, "-") {
		return fmt.Errorf("invalid base %q", baseRef)
	}
	baseTip, err := gitx.RevParse(t.Repo, baseRef)
	if err != nil {
		return fmt.Errorf("base branch %s of PR #%d: %w", baseRef, pr.Number, err)
	}
	if t.Base, err = gitx.MergeBase(t.Repo, baseTip, tip); err != nil {
		return fmt.Errorf("PR #%d and %s share no history", pr.Number, baseRef)
	}
	t.Mode, t.Name, t.Head = Branch, fmt.Sprintf("pr-%d", pr.Number), tip
	where := "not checked out, read-only"
	if head, err := gitx.RevParse(t.Repo, "HEAD"); err == nil && head == tip {
		t.Head, where = "", "checked out, includes uncommitted changes"
	}
	title := text.Line(pr.Title)
	t.Title = fmt.Sprintf("PR #%d: %s", pr.Number, title)
	t.Kicker = fmt.Sprintf("%s · %s vs %s (merge-base %s) · %s", filepath.Base(t.Repo), text.Line(pr.HeadRefName), baseRef, gitx.Short(t.Base), where)
	t.commitMessages(t.Base, tip)
	asked := title
	if body := cleanMessage(pr.Body); body != "" {
		asked += "\n\n" + body
	}
	if t.Asked != "" {
		asked += "\n\n" + t.AskedFrom + ":\n" + t.Asked
	}
	t.Asked, t.AskedFrom = asked, fmt.Sprintf("the description of PR #%d", pr.Number)
	t.Changes, err = gitx.Changes(t.Repo, t.Base, t.Head, false)
	return err
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

func viewPR(repo, ref string) (*pullRequest, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, errors.New("--pr needs the GitHub CLI (gh)")
	}
	cmd := exec.Command("gh", "pr", "view", ref, "--json", "number,title,body,url,baseRefName,headRefName,headRefOid")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_PAGER=cat", "NO_COLOR=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr view %s: %v: %s", ref, err, strings.TrimSpace(stderr.String()))
	}
	var pr pullRequest
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, fmt.Errorf("gh pr view %s: %w", ref, err)
	}
	return &pr, nil
}
