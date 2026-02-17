// Command render-debug launches a live web dashboard for pitui render
// performance metrics. It tails a JSONL log file and streams data to
// the browser via Server-Sent Events.
//
// Usage:
//
//	# Terminal 1: start the REPL with render debug
//	DANG_DEBUG_RENDER=1 dang
//
//	# Terminal 2: launch the dashboard
//	go run ./cmd/render-debug
//	go run ./cmd/render-debug -file /path/to/custom.log
package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

//go:embed dashboard.html
var dashboardFS embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "address to listen on (port 0 = auto)")
	logFile := flag.String("file", "/tmp/dang_render_debug.log", "path to JSONL render debug log")
	noBrowser := flag.Bool("no-open", false, "don't open browser automatically")
	flag.Parse()

	if err := run(*addr, *logFile, !*noBrowser); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(addr, logFile string, openBrowser bool) error {
	hub := newSSEHub()
	go hub.run()
	go tailFile(logFile, hub)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, _ := dashboardFS.ReadFile("dashboard.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data) //nolint:errcheck
	})
	mux.HandleFunc("/events", hub.serveSSE)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	url := fmt.Sprintf("http://%s", ln.Addr())
	fmt.Printf("Dashboard: %s\n", url)
	fmt.Printf("Tailing:   %s\n", logFile)
	fmt.Println("Press Ctrl+C to stop.")

	if openBrowser {
		go openURL(url)
	}

	srv := &http.Server{Handler: mux}
	return srv.Serve(ln)
}

// ---------- SSE hub ---------------------------------------------------------

type sseHub struct {
	clients    map[chan []byte]struct{}
	register   chan chan []byte
	unregister chan chan []byte
	broadcast  chan []byte

	historyMu sync.Mutex
	history   [][]byte
}

const maxHistory = 2000

func newSSEHub() *sseHub {
	return &sseHub{
		clients:    make(map[chan []byte]struct{}),
		register:   make(chan chan []byte),
		unregister: make(chan chan []byte),
		broadcast:  make(chan []byte, 256),
	}
}

func (h *sseHub) addToHistory(line []byte) {
	cp := make([]byte, len(line))
	copy(cp, line)
	h.historyMu.Lock()
	h.history = append(h.history, cp)
	if len(h.history) > maxHistory {
		h.history = h.history[len(h.history)-maxHistory:]
	}
	h.historyMu.Unlock()
}

func (h *sseHub) getHistory() [][]byte {
	h.historyMu.Lock()
	hist := make([][]byte, len(h.history))
	copy(hist, h.history)
	h.historyMu.Unlock()
	return hist
}

func (h *sseHub) run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = struct{}{}
		case c := <-h.unregister:
			delete(h.clients, c)
			close(c)
		case msg := <-h.broadcast:
			for c := range h.clients {
				select {
				case c <- msg:
				default:
				}
			}
		}
	}
}

func (h *sseHub) serveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	for _, line := range h.getHistory() {
		fmt.Fprintf(w, "data: %s\n\n", line)
	}
	flusher.Flush()

	ch := make(chan []byte, 64)
	h.register <- ch
	defer func() { h.unregister <- ch }()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

// ---------- file tailer -----------------------------------------------------

func tailFile(path string, hub *sseHub) {
	send := func(line []byte) {
		hub.addToHistory(line)
		hub.broadcast <- line
	}

	for {
		f, err := os.Open(path)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		f.Seek(0, io.SeekEnd) //nolint:errcheck
		scanner := bufio.NewScanner(f)
		for {
			for scanner.Scan() {
				line := scanner.Bytes()
				if json.Valid(line) {
					send(line)
				}
			}
			time.Sleep(50 * time.Millisecond)

			info, err := f.Stat()
			if err != nil {
				break
			}
			pos, _ := f.Seek(0, io.SeekCurrent)
			if info.Size() < pos {
				f.Close()
				break
			}
			scanner = bufio.NewScanner(f)
		}
		f.Close()
	}
}

// ---------- browser opener --------------------------------------------------

func openURL(url string) {
	time.Sleep(200 * time.Millisecond)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Run() //nolint:errcheck
}
