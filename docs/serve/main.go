// Command serve hosts the dang documentation site and collects anonymous
// per-paragraph feedback.
//
// It serves the static HTML built by booklit and exposes a single POST
// /feedback endpoint. Submissions are appended to an append-only,
// LLM-readable log. For each submission the server resolves the source
// markdown file and line by fuzzy-matching the quoted excerpt against the
// markdown sources, so maintainers (or an LLM) can jump straight to the text
// the reader is commenting on.
//
// No identifying information is recorded: no IP address, no cookies, no user
// agent. Only the page, the resolved source location, the quoted excerpt and
// the reader's message are written.
//
//	go run ./serve -dir . -src lit -feedback feedback.log -addr :8080
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func main() {
	var (
		dir      = flag.String("dir", ".", "directory of built static docs to serve")
		src      = flag.String("src", "lit", "directory of markdown sources (for file:line resolution)")
		feedback = flag.String("feedback", "feedback.log", "append-only file to record feedback in")
		addr     = flag.String("addr", ":8080", "address to listen on")
	)
	flag.Parse()

	store := &store{path: *feedback}
	res := newResolver(*src)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /feedback", feedbackHandler(store, res))

	// Serve the static docs, but never expose the feedback log itself.
	feedbackName := filepath.Base(*feedback)
	files := http.FileServer(http.Dir(*dir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if filepath.Base(r.URL.Path) == feedbackName {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})

	log.Printf("serving %s on %s, recording feedback to %s", *dir, *addr, *feedback)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

// submission is the JSON body posted by the client widget.
type submission struct {
	Page    string `json:"page"`    // e.g. "/fields.html"
	Excerpt string `json:"excerpt"` // the quoted paragraph text
	Message string `json:"message"` // the reader's message
}

func feedbackHandler(s *store, res *resolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var sub submission
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&sub); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		sub.Message = strings.TrimSpace(sub.Message)
		sub.Excerpt = strings.TrimSpace(sub.Excerpt)
		if sub.Message == "" {
			http.Error(w, "message is required", http.StatusBadRequest)
			return
		}

		loc := res.resolve(sub.Page, sub.Excerpt)
		if err := s.append(time.Now().UTC(), sub, loc); err != nil {
			log.Printf("failed to record feedback: %v", err)
			http.Error(w, "could not record feedback", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// store is an append-only, concurrency-safe feedback log writer.
type store struct {
	mu   sync.Mutex
	path string
}

func (s *store) append(ts time.Time, sub submission, loc location) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	source := "unresolved"
	if loc.ok {
		source = fmt.Sprintf("%s:%d", loc.file, loc.line)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n", ts.Format(time.RFC3339))
	fmt.Fprintf(&b, "page: %s\n", oneLine(sub.Page))
	fmt.Fprintf(&b, "source: %s\n", source)
	fmt.Fprintf(&b, "excerpt: %s\n", oneLine(sub.Excerpt))
	fmt.Fprintf(&b, "message: %s\n", oneLine(sub.Message))
	b.WriteString("---\n")

	_, err = f.WriteString(b.String())
	return err
}

// oneLine collapses newlines so each field stays on a single, greppable line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
