// Command gitserver runs the local git server fixture of SDD §10.4 for the Playwright
// tests. It prints one JSON line with its URLs and serves a control API on loopback:
//
//	POST /repos/{name}                    create a bare repository
//	POST /repos/{name}/commits            commit files to a branch
//	GET  /repos/{name}/branches           list branches
//	GET  /repos/{name}/commits/{branch}   head commit of a branch
//	GET  /repos/{name}/file?branch=&path= file content at a branch
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/alternayte/sluice/internal/testutil/gitserver"
)

// fixtureT implements gitserver.T for a program: a failure stops the process.
type fixtureT struct {
	mu       sync.Mutex
	cleanups []func()
}

func (f *fixtureT) Helper()                           {}
func (f *fixtureT) Fatal(args ...any)                 { log.Fatal(args...) }
func (f *fixtureT) Fatalf(format string, args ...any) { log.Fatalf(format, args...) }

func (f *fixtureT) TempDir() string {
	d, err := os.MkdirTemp("", "sluice-gitfixture-*")
	if err != nil {
		log.Fatal(err)
	}
	f.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func (f *fixtureT) Cleanup(fn func()) {
	f.mu.Lock()
	f.cleanups = append(f.cleanups, fn)
	f.mu.Unlock()
}

func (f *fixtureT) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	ft := &fixtureT{}
	g := gitserver.Start(ft)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /repos/{name}", func(w http.ResponseWriter, r *http.Request) {
		httpURL, sshURL := g.Repo(r.PathValue("name"))
		writeJSON(w, map[string]string{"http_url": httpURL, "ssh_url": sshURL})
	})
	mux.HandleFunc("POST /repos/{name}/commits", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Branch  string           `json:"branch"`
			Message string           `json:"message"`
			Files   []gitserver.File `json:"files"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"sha": g.Commit(r.PathValue("name"), in.Branch, in.Message, in.Files)})
	})
	mux.HandleFunc("GET /repos/{name}/branches", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, g.Branches(r.PathValue("name")))
	})
	mux.HandleFunc("GET /repos/{name}/commits/{branch...}", func(w http.ResponseWriter, r *http.Request) {
		c := g.CommitOf(r.PathValue("name"), r.PathValue("branch"))
		writeJSON(w, map[string]string{"sha": c.Hash.String(), "author_name": c.Author.Name, "author_email": c.Author.Email, "message": c.Message})
	})
	mux.HandleFunc("GET /repos/{name}/file", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, g.FileAt(r.PathValue("name"), r.URL.Query().Get("branch"), r.URL.Query().Get("path")))
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	info, _ := json.Marshal(map[string]string{"control": "http://" + ln.Addr().String(), "http_base": g.HTTPBase, "token": gitserver.Token})
	fmt.Println(string(info))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	_ = srv.Close()
	ft.close()
}
