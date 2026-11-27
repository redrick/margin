package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/redrick/margin/internal/github"
	"github.com/redrick/margin/internal/review"
	"github.com/redrick/margin/internal/state"
	"github.com/redrick/margin/internal/testutil"
)

// fakeGH answers like GitHub for PR #7, whose diff adds every line of file, and records what was posted.
func fakeGH(t *testing.T, repo, file string, otherLine int, posts *[]map[string]any) github.Runner {
	data, err := os.ReadFile(filepath.Join(repo, file))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	patch := fmt.Sprintf("@@ -0,0 +1,%d @@\n+%s", len(lines), strings.Join(lines, "\n+"))
	files, _ := json.Marshal(map[string]string{"filename": file, "patch": patch})
	head := strings.Repeat("a", 40)
	return func(_ string, stdin []byte, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(cmd, "pr view"):
			return []byte(`{"number":7,"title":"T","url":"https://github.com/o/r/pull/7","headRefOid":"` + head + `","state":"OPEN","author":{"login":"other"}}`), nil
		case cmd == "api user --jq .login":
			return []byte("me\n"), nil
		case strings.HasPrefix(cmd, "api --paginate"):
			return files, nil
		case strings.HasPrefix(cmd, "api graphql --paginate"):
			return []byte(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"id":"T9","viewerCanResolve":true,"path":"` + file +
				`","comments":{"nodes":[{"databaseId":5,"body":"issue (blocking): breaks","reactionGroups":[{"content":"HEART","reactors":{"totalCount":1}}]}]}},` +
				`{"id":"T8","line":` + strconv.Itoa(otherLine) + `,"diffSide":"RIGHT","path":"` + file + `","comments":{"nodes":[{"databaseId":6,"body":"someone else","author":{"login":"other"}}]}},` +
				`{"id":"T7","line":null,"originalLine":3,"isOutdated":true,"path":"` + file + `","comments":{"nodes":[{"databaseId":7,"body":"old"}]}}]}}}}}`), nil
		case strings.HasPrefix(cmd, "api graphql -f owner="):
			return []byte(`{"data":{"repository":{"pullRequest":{"comments":{"nodes":[{"url":"u","body":"looks fine","createdAt":"2026-01-02","author":{"login":"other"}}]},` +
				`"reviews":{"nodes":[{"body":"","state":"COMMENTED","submittedAt":"2026-01-01"},{"body":"ok","state":"APPROVED","submittedAt":"2026-01-01"}]}}}}}`), nil
		case strings.HasPrefix(cmd, "api graphql -f id=T9"):
			*posts = append(*posts, map[string]any{"_to": "resolve"})
			return []byte(`{}`), nil
		case strings.HasPrefix(cmd, "api --method POST repos/o/r/pulls/7/"):
			var p map[string]any
			if err := json.Unmarshal(stdin, &p); err != nil {
				t.Fatal(err)
			}
			p["_to"] = args[3]
			*posts = append(*posts, p)
			return []byte(`{"html_url":"https://github.com/o/r/pull/7#posted"}`), nil
		}
		return nil, errors.New("unexpected gh " + cmd)
	}
}

func TestGitHubPosting(t *testing.T) {
	path := testutil.Example(t)
	r := &Reviews{path: path}
	if _, err := r.Current(); err != nil {
		t.Fatal(err)
	}
	d, p, err := r.part("reserve", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec.Side == review.SideBase {
		t.Fatalf("fixture part changed: %+v", p.Spec)
	}
	var posts []map[string]any
	old := ghRun
	ghRun = fakeGH(t, d.Repo, p.Spec.File, p.Range.End+1, &posts)
	t.Cleanup(func() { ghRun = old })

	line := p.Range.Start
	for strings.TrimSpace(p.Lines[line]) == "" {
		line++
	}
	for range 2 {
		if _, err := r.AddComment(CommentInput{Station: "reserve", Part: 0, Line: line, Label: "issue", Blocking: true, Text: "breaks"}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := r.PostGitHubComment("c1"); err == nil {
		t.Fatal("posted without the confirmation having read the pull request")
	}
	plan, err := r.PlanGitHubComment("c1")
	if err != nil {
		t.Fatal(err)
	}
	if pl := plan.Comments[0]; !pl.Inline || pl.Line != line+1 || pl.Body != "issue (blocking): breaks" || plan.PR.Number != 7 {
		t.Fatalf("plan %+v", plan)
	}
	up, err := r.PostGitHubComment("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0]["_to"] != "repos/o/r/pulls/7/comments" || posts[0]["line"] != float64(line+1) {
		t.Fatalf("posted %v", posts)
	}
	if up.Reader.Comments[0].GitHub == "" {
		t.Fatal("posted comment not recorded")
	}
	if _, err := r.PlanGitHubComment("c1"); err == nil {
		t.Fatal("a posted comment can be posted again")
	}
	if _, err := r.ToggleForGitHub("c1"); err == nil {
		t.Fatal("a posted comment can be picked for the review")
	}
	if _, err := r.EditComment("c1", "other words"); err == nil {
		t.Fatal("a posted comment can be reworded")
	}

	if _, err := r.ToggleForGitHub("c2"); err != nil {
		t.Fatal(err)
	}
	rp, err := r.PlanGitHubReview()
	if err != nil {
		t.Fatal(err)
	}
	if len(rp.Comments) != 1 || rp.Comments[0].ID != "c2" {
		t.Fatalf("review plan %+v", rp)
	}
	if _, err := r.SubmitGitHubReview("REQUEST_CHANGES", "please look"); err != nil {
		t.Fatal(err)
	}
	last := posts[len(posts)-1]
	if last["_to"] != "repos/o/r/pulls/7/reviews" || last["event"] != "REQUEST_CHANGES" || last["body"] != "please look" ||
		len(last["comments"].([]any)) != 1 {
		t.Fatalf("review %v", last)
	}
	st, err := state.Load(review.StatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	if q, _ := st.Question("c2"); q.GitHub == "" || q.ForGitHub {
		t.Fatalf("c2 after the review: %+v", q)
	}

	if _, err := r.ResolveGitHubThread("T9", true); err == nil {
		t.Fatal("resolved before the threads were read")
	}
	pull, err := r.GitHubThreads()
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := pull.Mine["c2"]; !ok || v.Reactions[0].Content != "HEART" || !v.CanResolve || v.ID != "T9" || v.Comment != "c2" {
		t.Fatalf("mine %+v", pull.Mine)
	}
	if len(pull.Threads) != 2 || pull.Threads[0].Station != "reserve" || pull.Threads[0].Line != p.Range.End+1 ||
		pull.Threads[1].Station != "" || !pull.Threads[1].Outdated {
		t.Fatalf("threads %+v", pull.Threads)
	}
	if len(pull.Conversation) != 2 || pull.Conversation[0].State != "APPROVED" || pull.Conversation[1].Body != "looks fine" {
		t.Fatalf("conversation %+v", pull.Conversation)
	}
	if _, err := r.ResolveGitHubThread("T9", true); err != nil || posts[len(posts)-1]["_to"] != "resolve" {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := r.ResolveGitHubThread("T-unknown", true); err == nil {
		t.Fatal("resolved a thread margin never read")
	}
}
