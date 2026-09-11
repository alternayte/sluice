// Package gitserver is the local git server fixture of SDD §10.4: smart HTTP with a
// token and SSH with a key. Repositories are bare. go-git writes commits into them, so a
// test can build trees that the git command refuses, for example with a ".." entry or a
// symlink. The git binary serves them through `git http-backend`, `git upload-pack` and
// `git receive-pack`.
package gitserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Token is the password that the HTTP server accepts.
const Token = "fixture-token"

// T is the part of testing.TB that the fixture uses. Go tests pass their *testing.T. The
// fixture program of the Playwright tests passes its own implementation.
type T interface {
	Helper()
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	TempDir() string
	Cleanup(func())
}

// Server is a running git server.
type Server struct {
	Root string
	// HTTPBase is the base URL of the smart HTTP server, for example http://127.0.0.1:1234.
	HTTPBase string
	// SSHHost is the host and port of the SSH server.
	SSHHost string
	// ClientKey is the PEM private key that the SSH server accepts.
	ClientKey []byte
	// OtherKey is a PEM private key that the SSH server refuses.
	OtherKey []byte
	// KnownHosts is a known_hosts line for the SSH server.
	KnownHosts string

	t  T
	mu sync.Mutex
}

// File is one tree entry of a commit. Mode defaults to a regular file.
type File struct {
	Path    string
	Content string
	Mode    filemode.FileMode
}

// Start starts the HTTP and SSH servers. They stop at the end of the test.
func Start(t T) *Server {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("the git fixture needs the git command: %v", err)
	}
	s := &Server{Root: t.TempDir(), t: t}
	s.startHTTP()
	s.startSSH()
	return s
}

// Repo creates a bare repository and returns its HTTP and SSH clone URLs.
func (s *Server) Repo(name string) (httpURL, sshURL string) {
	s.t.Helper()
	if _, err := git.PlainInit(filepath.Join(s.Root, name+".git"), true); err != nil {
		s.t.Fatal(err)
	}
	return s.HTTPBase + "/" + name + ".git", "ssh://git@" + s.SSHHost + "/" + name + ".git"
}

func (s *Server) open(repo string) *git.Repository {
	s.t.Helper()
	r, err := git.PlainOpen(filepath.Join(s.Root, repo+".git"))
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

// Commit writes a commit with exactly these files on the branch and returns its SHA.
func (s *Server) Commit(repo, branch, message string, files []File) string {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.open(repo)
	tree, err := writeTree(r, files)
	if err != nil {
		s.t.Fatal(err)
	}
	ref := plumbing.NewBranchReferenceName(branch)
	var parents []plumbing.Hash
	if cur, err := r.Reference(ref, true); err == nil {
		parents = append(parents, cur.Hash())
	}
	sig := object.Signature{Name: "Fixture", Email: "fixture@example.com", When: time.Now()}
	c := &object.Commit{Author: sig, Committer: sig, Message: message, TreeHash: tree, ParentHashes: parents}
	obj := r.Storer.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		s.t.Fatal(err)
	}
	h, err := r.Storer.SetEncodedObject(obj)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := r.Storer.SetReference(plumbing.NewHashReference(ref, h)); err != nil {
		s.t.Fatal(err)
	}
	if head, err := r.Storer.Reference(plumbing.HEAD); err != nil || head.Type() != plumbing.SymbolicReference || head.Target() != ref {
		if len(parents) == 0 && !s.hasBranches(r, ref) {
			_ = r.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, ref))
		}
	}
	return h.String()
}

func (s *Server) hasBranches(r *git.Repository, except plumbing.ReferenceName) bool {
	it, err := r.Branches()
	if err != nil {
		return false
	}
	found := false
	_ = it.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name() != except {
			found = true
		}
		return nil
	})
	return found
}

// Head returns the SHA of a branch, or "" when the branch does not exist.
func (s *Server) Head(repo, branch string) string {
	s.t.Helper()
	ref, err := s.open(repo).Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		return ""
	}
	return ref.Hash().String()
}

// Branches returns the branch names of a repository, sorted.
func (s *Server) Branches(repo string) []string {
	s.t.Helper()
	it, err := s.open(repo).Branches()
	if err != nil {
		s.t.Fatal(err)
	}
	var out []string
	_ = it.ForEach(func(ref *plumbing.Reference) error {
		out = append(out, ref.Name().Short())
		return nil
	})
	sort.Strings(out)
	return out
}

// CommitOf returns the commit at the head of a branch.
func (s *Server) CommitOf(repo, branch string) *object.Commit {
	s.t.Helper()
	r := s.open(repo)
	ref, err := r.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		s.t.Fatalf("branch %s: %v", branch, err)
	}
	c, err := r.CommitObject(ref.Hash())
	if err != nil {
		s.t.Fatal(err)
	}
	return c
}

// FileAt returns the content of a file at the head of a branch.
func (s *Server) FileAt(repo, branch, path string) string {
	s.t.Helper()
	f, err := s.CommitOf(repo, branch).File(path)
	if err != nil {
		s.t.Fatalf("file %s on %s: %v", path, branch, err)
	}
	content, err := f.Contents()
	if err != nil {
		s.t.Fatal(err)
	}
	return content
}

// writeTree stores blobs and nested trees for the files and returns the root tree hash.
func writeTree(r *git.Repository, files []File) (plumbing.Hash, error) {
	type node struct {
		children map[string]*node
		blob     plumbing.Hash
		mode     filemode.FileMode
	}
	root := &node{children: map[string]*node{}}
	for _, f := range files {
		obj := r.Storer.NewEncodedObject()
		obj.SetType(plumbing.BlobObject)
		w, err := obj.Writer()
		if err != nil {
			return plumbing.ZeroHash, err
		}
		if _, err := io.WriteString(w, f.Content); err != nil {
			return plumbing.ZeroHash, err
		}
		_ = w.Close()
		h, err := r.Storer.SetEncodedObject(obj)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		parts := strings.Split(f.Path, "/")
		n := root
		for _, p := range parts[:len(parts)-1] {
			c, ok := n.children[p]
			if !ok {
				c = &node{children: map[string]*node{}}
				n.children[p] = c
			}
			n = c
		}
		mode := f.Mode
		if mode == 0 {
			mode = filemode.Regular
		}
		n.children[parts[len(parts)-1]] = &node{blob: h, mode: mode}
	}
	var write func(n *node) (plumbing.Hash, error)
	write = func(n *node) (plumbing.Hash, error) {
		t := &object.Tree{}
		for name, c := range n.children {
			if c.children != nil {
				h, err := write(c)
				if err != nil {
					return plumbing.ZeroHash, err
				}
				t.Entries = append(t.Entries, object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: h})
			} else {
				t.Entries = append(t.Entries, object.TreeEntry{Name: name, Mode: c.mode, Hash: c.blob})
			}
		}
		// Git sorts tree entries by name, with a "/" after directory names.
		key := func(e object.TreeEntry) string {
			if e.Mode == filemode.Dir {
				return e.Name + "/"
			}
			return e.Name
		}
		sort.Slice(t.Entries, func(i, j int) bool { return key(t.Entries[i]) < key(t.Entries[j]) })
		obj := r.Storer.NewEncodedObject()
		if err := t.Encode(obj); err != nil {
			return plumbing.ZeroHash, err
		}
		return r.Storer.SetEncodedObject(obj)
	}
	return write(root)
}

func (s *Server) startHTTP() {
	gitPath, _ := exec.LookPath("git")
	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Env: []string{"GIT_PROJECT_ROOT=" + s.Root, "GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.receivepack", "GIT_CONFIG_VALUE_0=true"},
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pass, ok := r.BasicAuth(); !ok || pass != Token {
			w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	s.t.Cleanup(func() { _ = srv.Close() })
	s.HTTPBase = "http://" + ln.Addr().String()
}

func newKey() (ed25519.PublicKey, []byte) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		panic(err)
	}
	return pub, pem.EncodeToMemory(block)
}

func (s *Server) startSSH() {
	clientPub, clientPEM := newKey()
	_, otherPEM := newKey()
	s.ClientKey, s.OtherKey = clientPEM, otherPEM
	allowed, err := ssh.NewPublicKey(clientPub)
	if err != nil {
		s.t.Fatal(err)
	}
	_, hostPEM := newKey()
	hostSigner, err := ssh.ParsePrivateKey(hostPEM)
	if err != nil {
		s.t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if bytes.Equal(key.Marshal(), allowed.Marshal()) {
			return nil, nil
		}
		return nil, errors.New("unknown key")
	}}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = ln.Close() })
	s.SSHHost = ln.Addr().String()
	s.KnownHosts = knownhosts.Line([]string{knownhosts.Normalize(s.SSHHost)}, hostSigner.PublicKey())
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serveSSH(conn, cfg)
		}
	}()
}

func (s *Server) serveSSH(conn net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, requests, err := nc.Accept()
		if err != nil {
			continue
		}
		go s.serveSession(ch, requests)
	}
}

func (s *Server) serveSession(ch ssh.Channel, requests <-chan *ssh.Request) {
	defer func() { _ = ch.Close() }()
	for req := range requests {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		verb, arg, _ := strings.Cut(payload.Command, " ")
		repo := strings.Trim(arg, "'\"")
		repo = strings.TrimPrefix(repo, "/")
		if (verb != "git-upload-pack" && verb != "git-receive-pack") || strings.Contains(repo, "..") {
			_ = req.Reply(false, nil)
			return
		}
		_ = req.Reply(true, nil)
		cmd := exec.Command("git", strings.TrimPrefix(verb, "git-"), filepath.Join(s.Root, repo))
		cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
		code := 0
		if err := cmd.Run(); err != nil {
			code = 1
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			}
		}
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
		return
	}
}

// Keep os imported for callers that build paths with the root on some platforms.
var _ = os.PathSeparator

// String describes the server for test logs.
func (s *Server) String() string {
	return fmt.Sprintf("git fixture http=%s ssh=%s", s.HTTPBase, s.SSHHost)
}
