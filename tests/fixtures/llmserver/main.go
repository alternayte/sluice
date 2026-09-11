// Command llmserver runs the scripted LLM fixture of SDD §10.4 for the Playwright tests.
// It prints one JSON line with its URLs and serves a control API on loopback:
//
//	POST /script    append replies (a JSON array of llmserver.Reply)
//	POST /reset     remove the script and the recorded requests
//	GET  /requests  the recorded requests
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alternayte/sluice/internal/testutil/llmserver"
)

func main() {
	s := llmserver.New()
	defer s.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /script", func(w http.ResponseWriter, r *http.Request) {
		var replies []llmserver.Reply
		if err := json.NewDecoder(r.Body).Decode(&replies); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.Push(replies...)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /reset", func(w http.ResponseWriter, _ *http.Request) {
		s.Reset()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /requests", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Requests())
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	info, _ := json.Marshal(map[string]string{"control": "http://" + ln.Addr().String(), "anthropic_url": s.URL, "openai_url": s.OpenAIURL})
	fmt.Println(string(info))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	_ = srv.Close()
}
