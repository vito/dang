package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

//go:embed render_debug_dashboard.html
var dashboardFS embed.FS

func renderDebugCmd() *cobra.Command {
	var (
		addr    string
		logFile string
		open    bool
	)

	cmd := &cobra.Command{
		Use:   "render-debug",
		Short: "Launch a live dashboard for pitui render performance",
		Long: `Starts a local web server that displays real-time charts of
pitui rendering performance metrics. Use alongside a REPL session
that has :debug-render enabled (or DANG_DEBUG_RENDER=1).

The dashboard reads JSONL data from the log file and streams it
to the browser via Server-Sent Events.`,
		Example: `  # In terminal 1: start the REPL with render debug
  DANG_DEBUG_RENDER=1 dang

  # In terminal 2: launch the dashboard
  dang render-debug
  dang render-debug --open
  dang render-debug --file /path/to/custom.log`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRenderDebug(addr, logFile, open)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:0", "Address to listen on (use port 0 for auto)")
	cmd.Flags().StringVar(&logFile, "file", "/tmp/dang_render_debug.log", "Path to the JSONL render debug log")
	cmd.Flags().BoolVar(&open, "open", true, "Open browser automatically")

	return cmd
}

func runRenderDebug(addr, logFile string, openBrowser bool) error {
	// SSE hub: fans out JSONL records to all connected browsers.
	hub := newSSEHub()
	go hub.run()

	// Tail the log file.
	go tailFile(logFile, hub)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, _ := dashboardFS.ReadFile("render_debug_dashboard.html")
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
					// slow client, drop
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

	// Send history first so newly connected browsers see past data.
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

		// Seek to end — only show new data.
		f.Seek(0, io.SeekEnd) //nolint:errcheck
		scanner := bufio.NewScanner(f)
		for {
			for scanner.Scan() {
				line := scanner.Bytes()
				// Validate JSON.
				if json.Valid(line) {
					send(line)
				}
			}
			// EOF — wait for more data.
			time.Sleep(50 * time.Millisecond)

			// Check if file was truncated/rotated.
			info, err := f.Stat()
			if err != nil {
				break
			}
			pos, _ := f.Seek(0, io.SeekCurrent)
			if info.Size() < pos {
				// File was truncated — reopen.
				f.Close()
				break
			}
			// Reset scanner to keep reading from current position.
			scanner = bufio.NewScanner(f)
		}
		f.Close()
	}
}

// ---------- browser opener --------------------------------------------------

func openURL(url string) {
	// Small delay so the server is ready.
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
