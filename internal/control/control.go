package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Request struct {
	Cmd string `json:"cmd"`
	Arg string `json:"arg,omitempty"`
}

type Response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

func OK(v any) Response {
	data, err := json.Marshal(v)
	if err != nil {
		return Fail(err)
	}
	return Response{OK: true, Data: data}
}

func Fail(err error) Response { return Response{Error: err.Error()} }

func Dir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "margin")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("margin-%d", os.Getuid()))
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func SocketPath(reviewPath string) string {
	sum := sha256.Sum256([]byte(reviewPath))
	name := strings.TrimSuffix(filepath.Base(reviewPath), ".yaml")
	name = unsafeName.ReplaceAllString(name, "_")
	if len(name) > 40 {
		name = name[:40]
	}
	return filepath.Join(Dir(), name+"-"+hex.EncodeToString(sum[:4])+".sock")
}

type Server struct {
	ln   net.Listener
	path string
}

func Serve(path string, handle func(Request) Response) (*Server, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	if Alive(path) {
		return nil, fmt.Errorf("a viewer is already serving %s", path)
	}
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	s := &Server{ln: ln, path: path}
	go s.loop(handle)
	return s, nil
}

func (s *Server) Close() {
	s.ln.Close()
	os.Remove(s.path)
}

func (s *Server) loop(handle func(Request) Response) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(30 * time.Second))
			var req Request
			if err := json.NewDecoder(conn).Decode(&req); err != nil {
				json.NewEncoder(conn).Encode(Fail(err))
				return
			}
			json.NewEncoder(conn).Encode(handle(req))
		}()
	}
}

func Call(path string, req Request) (Response, error) {
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, err
	}
	if !resp.OK {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

func Alive(path string) bool {
	conn, err := net.DialTimeout("unix", path, 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Live returns sockets of running viewers and removes stale ones.
func Live() []string {
	matches, _ := filepath.Glob(filepath.Join(Dir(), "*.sock"))
	var live []string
	for _, m := range matches {
		if Alive(m) {
			live = append(live, m)
		} else {
			os.Remove(m)
		}
	}
	return live
}
