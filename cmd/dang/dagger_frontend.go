package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"

	"dagger.io/dagger/telemetry"

	"github.com/dagger/dagger/dagql/dagui"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/vito/dang/pkg/pitui"
)

// ── styles ──────────────────────────────────────────────────────────────────

var (
	feSpanRunning  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))  // yellow
	feSpanOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))  // green
	feSpanFailed   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))  // red
	feSpanCached   = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))  // blue
	feSpanPending  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // gray
	feSpanDuration = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // gray
	feTreeLine     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // gray
	feSpanName     = lipgloss.NewStyle().Bold(true)
)

// ── daggerFrontend ──────────────────────────────────────────────────────────
//
// A native pitui component that renders Dagger telemetry using dagui.DB.
// Spans/logs are received via an embedded OTLP HTTP receiver and fed into
// the DB. The component re-renders whenever new data arrives.

type daggerFrontend struct {
	pitui.Compo

	mu   sync.Mutex
	db   *dagui.DB
	opts dagui.FrontendOpts
	tui  *pitui.TUI

	// OTLP receiver
	listener net.Listener
	server   *http.Server
}

func newDaggerFrontend(tui *pitui.TUI) (*daggerFrontend, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for OTLP: %w", err)
	}

	db := dagui.NewDB()

	fe := &daggerFrontend{
		db:  db,
		tui: tui,
		opts: dagui.FrontendOpts{
			TooFastThreshold: 100 * time.Millisecond,
			GCThreshold:      1 * time.Second,
			Verbosity:        dagui.ShowCompletedVerbosity,
			ExpandCompleted:  true,
		},
		listener: l,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/traces", fe.handleTraces)
	mux.HandleFunc("POST /v1/logs", fe.handleLogs)
	mux.HandleFunc("POST /v1/metrics", fe.handleMetrics)

	fe.server = &http.Server{Handler: mux}
	go fe.server.Serve(l) //nolint:errcheck

	return fe, nil
}

// Endpoint returns the base URL for the OTLP receiver (e.g. "http://127.0.0.1:12345").
func (fe *daggerFrontend) Endpoint() string {
	return fmt.Sprintf("http://%s", fe.listener.Addr().String())
}

// Close shuts down the OTLP receiver.
func (fe *daggerFrontend) Close() {
	fe.server.Close()
	fe.listener.Close()
}

// Reset clears the DB for a new evaluation.
func (fe *daggerFrontend) Reset() {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	fe.db = dagui.NewDB()
	fe.Update()
}

// ── OTLP handlers ───────────────────────────────────────────────────────────

func (fe *daggerFrontend) handleTraces(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	spans := telemetry.SpansFromPB(req.ResourceSpans)

	fe.mu.Lock()
	fe.db.ExportSpans(r.Context(), spans) //nolint:errcheck
	fe.mu.Unlock()

	fe.Update()
	fe.tui.RequestRender(false)

	w.WriteHeader(http.StatusCreated)
}

func (fe *daggerFrontend) handleLogs(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	fe.mu.Lock()
	telemetry.ReexportLogsFromPB(r.Context(), fe.db.LogExporter(), &req) //nolint:errcheck
	fe.mu.Unlock()

	fe.Update()
	fe.tui.RequestRender(false)

	w.WriteHeader(http.StatusCreated)
}

func (fe *daggerFrontend) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// ── rendering ───────────────────────────────────────────────────────────────

func (fe *daggerFrontend) Render(ctx pitui.RenderContext) pitui.RenderResult {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	return renderDBTree(fe.db, fe.opts, ctx.Width)
}

// ── shared rendering helpers ────────────────────────────────────────────────

// renderDBTree renders a dagui.DB as a trace tree, returning pitui lines.
func renderDBTree(db *dagui.DB, opts dagui.FrontendOpts, width int) pitui.RenderResult {
	view := db.RowsView(opts)
	rows := view.Rows(opts)

	if len(rows.Order) == 0 {
		return pitui.RenderResult{}
	}

	var lines []string
	for _, row := range rows.Order {
		line := renderTraceRow(row, width)
		lines = append(lines, line)
	}

	return pitui.RenderResult{Lines: lines}
}

func renderTraceRow(row *dagui.TraceRow, width int) string {
	span := row.Span
	now := time.Now()

	// Build indent prefix.
	var prefix strings.Builder
	if row.Depth > 0 {
		for i := 0; i < row.Depth-1; i++ {
			prefix.WriteString(feTreeLine.Render("│ "))
		}
		if row.Next != nil {
			prefix.WriteString(feTreeLine.Render("├─"))
		} else {
			prefix.WriteString(feTreeLine.Render("╰─"))
		}
	}

	// Status symbol.
	symbol := statusSymbol(span)

	// Name.
	name := span.Name
	if name == "" {
		name = "(unnamed)"
	}

	// Duration.
	dur := span.Activity.Duration(now)
	durStr := ""
	if dur > 0 {
		durStr = " " + feSpanDuration.Render(dagui.FormatDuration(dur))
	}

	line := prefix.String() + symbol + " " + feSpanName.Render(name) + durStr

	// Truncate to width.
	if pitui.VisibleWidth(line) > width {
		line = pitui.Truncate(line, width, "…")
	}

	return line
}

func statusSymbol(span *dagui.Span) string {
	switch {
	case span.IsRunningOrEffectsRunning():
		return feSpanRunning.Render("●")
	case span.IsCached():
		return feSpanCached.Render("$")
	case span.IsFailedOrCausedFailure():
		return feSpanFailed.Render("✘")
	case span.IsPending():
		return feSpanPending.Render("○")
	default:
		return feSpanOK.Render("✔")
	}
}

// ── SpanExporter / LogExporter (for direct use) ─────────────────────────────

func (fe *daggerFrontend) SpanExporter() sdktrace.SpanExporter {
	return feSpanExporter{fe}
}

type feSpanExporter struct{ fe *daggerFrontend }

func (e feSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.fe.mu.Lock()
	defer e.fe.mu.Unlock()
	err := e.fe.db.ExportSpans(ctx, spans)
	e.fe.Update()
	e.fe.tui.RequestRender(false)
	return err
}

func (e feSpanExporter) Shutdown(ctx context.Context) error { return nil }

func (fe *daggerFrontend) LogExporter() sdklog.Exporter {
	return feLogExporter{fe}
}

type feLogExporter struct{ fe *daggerFrontend }

func (e feLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.fe.mu.Lock()
	defer e.fe.mu.Unlock()
	err := e.fe.db.LogExporter().Export(ctx, records)
	e.fe.Update()
	e.fe.tui.RequestRender(false)
	return err
}

func (e feLogExporter) Shutdown(ctx context.Context) error  { return nil }
func (e feLogExporter) ForceFlush(ctx context.Context) error { return nil }
