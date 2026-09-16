package control

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeAndCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.sock")
	srv, err := Serve(path, func(r Request) Response {
		if r.Cmd == "echo" {
			return OK(map[string]string{"arg": r.Arg})
		}
		return Fail(errors.New("nope"))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket perm = %o, want 600", perm)
	}

	resp, err := Call(path, Request{Cmd: "echo", Arg: "hi"})
	if err != nil || !strings.Contains(string(resp.Data), `"hi"`) {
		t.Fatalf("echo: resp=%s err=%v", resp.Data, err)
	}
	if _, err := Call(path, Request{Cmd: "other"}); err == nil || err.Error() != "nope" {
		t.Fatalf("expected handler error, got %v", err)
	}
	if _, err := Serve(path, nil); err == nil {
		t.Fatal("second server on the same socket should fail")
	}

	srv.Close()
	if Alive(path) {
		t.Fatal("socket still alive after Close")
	}
}

func TestSocketPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/test")
	a := SocketPath("/reviews/pr 42.review.yaml")
	if a != SocketPath("/reviews/pr 42.review.yaml") {
		t.Fatal("socket path not stable")
	}
	if a == SocketPath("/other/pr 42.review.yaml") {
		t.Fatal("different reviews share a socket")
	}
	if !strings.HasPrefix(a, "/run/user/test/margin/pr_42.review-") || strings.Contains(filepath.Base(a), " ") {
		t.Fatalf("unexpected socket path %q", a)
	}
}
