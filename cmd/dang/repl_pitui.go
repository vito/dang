package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"charm.land/lipgloss/v2"
	"dagger.io/dagger"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/kr/pretty"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/ioctx"
	"github.com/vito/dang/pkg/pitui"
)

// ── sync writer (streams dagger output into TUI) ────────────────────────────

// pituiSyncWriter is an io.Writer that directs output to a specific
// entryView. The target is set at eval start and cleared at eval end,
// so streaming output always lands on the correct entry regardless of
// any concurrent container mutations.
//
// Writes are buffered and flushed via a coalescing goroutine so that
// high-frequency Dagger progress output doesn't call Update() on every
// write. The flush goroutine drains the pending buffer at most once per
// render frame.
type pituiSyncWriter struct {
	mu      sync.Mutex
	target  *entryView
	pending strings.Builder
	dirty   chan struct{} // capacity-1 channel; non-blocking send coalesces
	done    chan struct{} // closed to stop the flush goroutine
}

func newPituiSyncWriter() *pituiSyncWriter {
	w := &pituiSyncWriter{
		dirty: make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
	go w.flushLoop()
	return w
}

func (w *pituiSyncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if w.target != nil {
		w.pending.Write(p)
	}
	w.mu.Unlock()
	// Coalescing signal — non-blocking so rapid writes merge.
	select {
	case w.dirty <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (w *pituiSyncWriter) flushLoop() {
	for {
		select {
		case <-w.dirty:
			w.flush()
		case <-w.done:
			w.flush() // drain any remaining data
			return
		}
	}
}

func (w *pituiSyncWriter) flush() {
	w.mu.Lock()
	ev := w.target
	data := w.pending.String()
	w.pending.Reset()
	w.mu.Unlock()
	if ev != nil && data != "" {
		ev.mu.Lock()
		ev.entry.writeLog(data)
		ev.mu.Unlock()
		ev.Update()
	}
}

// SetTarget directs future writes to the given entryView. Pass nil to
// suppress output (e.g. between evals). Flushes any pending data to the
// previous target before switching.
func (w *pituiSyncWriter) SetTarget(ev *entryView) {
	w.flush()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.target = ev
}

// Stop shuts down the flush goroutine. Must be called when the writer
// is no longer needed.
func (w *pituiSyncWriter) Stop() {
	close(w.done)
}

// ── entry view component ────────────────────────────────────────────────────

// entryView wraps a single replEntry as a pitui.Component. It embeds
// pitui.Compo for automatic render caching. Once finalized (result
// written, eval done), nobody calls Update(), so the framework skips
// Render() entirely and returns the cached result — making 200+ past
// entries O(1) per render cycle.
type entryView struct {
	pitui.Compo

	mu    sync.Mutex // protects entry writes from streaming goroutines
	entry *replEntry
}

func newEntryView(entry *replEntry) *entryView {
	ev := &entryView{entry: entry}
	ev.Update() // dirty for first render
	return ev
}

func (ev *entryView) Render(ctx pitui.RenderContext) pitui.RenderResult {
	ev.mu.Lock()
	defer ev.mu.Unlock()

	// Snapshot entry data.
	input := ev.entry.input
	logs := ev.entry.logs.String()
	result := ev.entry.result.String()

	var lines []string
	if input != "" {
		inputLines := strings.Split(input, "\n")
		if len(inputLines) > 0 && inputLines[len(inputLines)-1] == "" {
			inputLines = inputLines[:len(inputLines)-1]
		}
		lines = append(lines, inputLines...)
	}
	if logs != "" {
		logLines := strings.Split(logs, "\n")
		if len(logLines) > 0 && logLines[len(logLines)-1] == "" {
			logLines = logLines[:len(logLines)-1]
		}
		lines = append(lines, logLines...)
	}
	if result != "" {
		resLines := strings.Split(result, "\n")
		if len(resLines) > 0 && resLines[len(resLines)-1] == "" {
			resLines = resLines[:len(resLines)-1]
		}
		lines = append(lines, resLines...)
	}
	for i, line := range lines {
		lines[i] = pitui.ExpandTabs(line, 8)
	}
	for i, line := range lines {
		if pitui.VisibleWidth(line) > ctx.Width {
			lines[i] = pitui.Truncate(line, ctx.Width, "")
		}
	}

	return pitui.RenderResult{Lines: lines}
}

// ── completion menu overlay ─────────────────────────────────────────────────

type completionOverlay struct {
	pitui.Compo
	items      []string
	index      int
	maxVisible int
}

func (c *completionOverlay) Render(ctx pitui.RenderContext) pitui.RenderResult {
	if len(c.items) == 0 {
		return pitui.RenderResult{}
	}
	lines := strings.Split(renderMenuBox(c.items, c.index, c.maxVisible, ctx.Width), "\n")
	return pitui.RenderResult{Lines: lines}
}

// renderMenuBox renders the completion dropdown as a bordered box string.
func renderMenuBox(items []string, index, maxVisible, width int) string {
	visible := min(len(items), maxVisible)
	start := 0
	if index >= visible {
		start = index - visible + 1
	}
	end := start + visible

	// Compute max width from ALL items so the box doesn't resize as the
	// user scrolls through the list.
	maxW := 0
	for _, item := range items {
		if w := lipgloss.Width(item); w > maxW {
			maxW = w
		}
	}
	if maxW > 60 {
		maxW = 60
	}
	if maxW+4 > width {
		maxW = width - 4
	}
	if maxW < 4 {
		maxW = 4
	}

	var menuLines []string
	for i := start; i < end && i < len(items); i++ {
		item := items[i]
		if lipgloss.Width(item) > maxW {
			item = item[:maxW-3] + "..."
		}
		padded := fmt.Sprintf(" %-*s ", maxW, item)
		if i == index {
			menuLines = append(menuLines, menuSelectedStyle.Render(padded))
		} else {
			menuLines = append(menuLines, menuStyle.Render(padded))
		}
	}

	if len(items) > visible {
		info := fmt.Sprintf(" %d/%d ", index+1, len(items))
		menuLines = append(menuLines, dimStyle.Render(info))
	}

	inner := strings.Join(menuLines, "\n")
	return menuBorderStyle.Render(inner)
}

// ── detail bubble (viewport-relative overlay) ───────────────────────────────

type detailBubble struct {
	pitui.Compo
	item docItem
}

func (d *detailBubble) Render(ctx pitui.RenderContext) pitui.RenderResult {
	if d.item.name == "" {
		return pitui.RenderResult{}
	}

	// lipgloss Width(n) sets the TOTAL width including borders, so the
	// usable content area is n-2 (left border + right border).
	contentW := max(8, ctx.Width-2)

	docTextStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("249"))
	argNameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	argTypeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	dimSt := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	titleLine := detailTitleStyle.Render(d.item.name)
	lines := []string{titleLine}

	detail := renderDocDetail(d.item, contentW, docTextStyle, argNameStyle, argTypeStyle, dimSt)
	lines = append(lines, detail...)

	// Truncate inner content so the border fits within the height budget.
	// The border adds 2 lines (top + bottom).
	if ctx.Height > 0 && len(lines) > ctx.Height-2 {
		maxInner := ctx.Height - 2
		if maxInner > 1 {
			lines = lines[:maxInner-1]
			lines = append(lines, dimSt.Render("..."))
		} else if maxInner > 0 {
			lines = lines[:maxInner]
		}
	}

	inner := strings.Join(lines, "\n")
	box := detailBorderStyle.Width(ctx.Width).Render(inner)
	return pitui.RenderResult{
		Lines: strings.Split(box, "\n"),
	}
}

// ── REPL component ─────────────────────────────────────────────────────────

// replComponent is the main REPL controller. It orchestrates eval, input
// handling, and UI state. Completion logic lives in repl_completion.go,
// commands in repl_commands.go, and history in repl_history.go.
//
// Lock ordering: r.mu must be acquired before ev.mu. Never hold ev.mu
// while acquiring r.mu.
type replComponent struct {
	mu sync.Mutex

	// Dang state
	importConfigs []dang.ImportConfig
	debug         bool
	typeEnv       dang.Env
	evalEnv       dang.EvalEnv
	ctx           context.Context

	// UI state
	tui            *pitui.TUI
	textInput      *pitui.TextInput
	entryContainer *pitui.Container
	spinner        *pitui.Spinner
	inputSlot      *pitui.Slot // swaps between textInput and spinner

	quit     chan struct{}
	quitOnce sync.Once

	// Completion
	completions     []string
	menuVisible     bool
	menuItems       []string          // replacement values (full input text)
	menuLabels      []string          // display labels for the menu
	menuCompletions []dang.Completion // parallel to menuItems; Detail/Documentation
	menuIndex       int
	menuMaxVisible  int
	menuOverlay     *completionOverlay
	menuHandle      *pitui.OverlayHandle
	detailBubble    *detailBubble
	detailHandle    *pitui.OverlayHandle

	// Eval
	evaluating bool
	cancelEval context.CancelFunc
	daggerLog  *pituiSyncWriter // Dagger log output target (set per-eval)

	// History
	history *replHistory

	// Doc browser
	docBrowser *docBrowserOverlay
	docHandle  *pitui.OverlayHandle

	// Render debug
	debugRender     bool
	debugRenderFile *os.File
}

func newReplComponent(ctx context.Context, tui *pitui.TUI, importConfigs []dang.ImportConfig, debug bool) *replComponent {
	typeEnv, evalEnv := buildEnvFromImports(importConfigs)

	ti := pitui.NewTextInput(promptStyle.Render("dang> "))
	ti.ContinuationPrompt = promptStyle.Render("  ... ")

	r := &replComponent{
		importConfigs:  importConfigs,
		debug:          debug,
		typeEnv:        typeEnv,
		evalEnv:        evalEnv,
		ctx:            ctx,
		tui:            tui,
		textInput:      ti,
		entryContainer: &pitui.Container{},
		menuMaxVisible: 8,
		history:        newReplHistory(),
		quit:           make(chan struct{}),
	}

	// Spinner
	sp := pitui.NewSpinner()
	sp.Style = func(s string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Render(s)
	}
	sp.Label = dimStyle.Render("Evaluating... (Ctrl+C to cancel)")
	r.spinner = sp

	// Input slot starts with text input.
	r.inputSlot = pitui.NewSlot(ti)

	// Text input callbacks.
	ti.SuggestionStyle = func(s string) string {
		return dimStyle.Render(s)
	}
	ti.OnSubmit = r.onSubmit
	ti.OnKey = r.onKey
	ti.OnChange = r.updateCompletionMenu

	// Welcome message.
	welcome := newReplEntry("")
	welcome.writeLogLine(welcomeStyle.Render("Welcome to Dang REPL v0.1.0"))
	if len(importConfigs) > 0 {
		var names []string
		for _, ic := range importConfigs {
			names = append(names, ic.Name)
		}
		welcome.writeLogLine(dimStyle.Render(fmt.Sprintf("Imports: %s", strings.Join(names, ", "))))
	}
	welcome.writeLogLine("")
	welcome.writeLogLine(dimStyle.Render("Type :help for commands, Tab for completion, Alt+Enter for multiline, Ctrl+C to exit"))
	welcome.writeLogLine("")
	ev := newEntryView(welcome)
	r.entryContainer.AddChild(ev)

	r.completions = r.buildCompletions()
	r.history.Load()

	return r
}

// activeEntryView returns the last (active) entryView in the container.
// Caller must hold r.mu OR otherwise ensure no concurrent mutation of
// the container's children (e.g. during startup before the TUI is running).
func (r *replComponent) activeEntryView() *entryView {
	children := r.entryContainer.Children
	if len(children) == 0 {
		return nil
	}
	ev, _ := children[len(children)-1].(*entryView)
	return ev
}

// addEntry creates a new entryView, appends it to the container, and returns it.
// Caller must hold r.mu.
func (r *replComponent) addEntry(input string) *entryView {
	entry := newReplEntry(input)
	ev := newEntryView(entry)
	r.entryContainer.AddChild(ev)
	return ev
}

// install adds the repl's components to the TUI.
func (r *replComponent) install() {
	r.tui.AddChild(r.entryContainer)
	r.tui.AddChild(r.inputSlot)
	r.tui.SetFocus(r.textInput)
}

// onSubmit handles Enter in the text input.
func (r *replComponent) onSubmit(line string) bool {
	if line == "" {
		return false
	}

	r.history.Add(line)

	// Format the echoed input: first line with prompt, continuation lines with "  ... ".
	inputLines := strings.Split(line, "\n")
	var echoedLines []string
	for i, l := range inputLines {
		if i == 0 {
			echoedLines = append(echoedLines, promptStyle.Render("dang> ")+l)
		} else {
			echoedLines = append(echoedLines, promptStyle.Render("  ... ")+l)
		}
	}

	r.mu.Lock()
	r.addEntry(strings.Join(echoedLines, "\n"))
	r.mu.Unlock()

	r.hideCompletionMenu()

	if strings.HasPrefix(line, ":") {
		r.handleCommand(line[1:])
		r.updateCompletionMenu()
		return true
	}

	r.startEval(line)
	return true
}

// onKey handles keys not consumed by the text input editor.
func (r *replComponent) onKey(key uv.Key) bool {
	// While evaluating: Ctrl+C cancels.
	if r.evaluating {
		if key.Code == 'c' && key.Mod == uv.ModCtrl {
			if r.cancelEval != nil {
				r.cancelEval()
			}
			return true
		}
		return true // swallow everything else
	}

	// Completion menu navigation.
	if r.menuVisible {
		switch {
		case key.Code == uv.KeyTab && key.Mod == 0:
			if r.menuIndex < len(r.menuItems) {
				r.textInput.SetValue(r.menuItems[r.menuIndex])
				r.textInput.CursorEnd()
			}
			r.hideCompletionMenu()
			r.updateCompletionMenu()
			return true
		case key.Code == uv.KeyDown && key.Mod == 0,
			key.Code == 'n' && key.Mod == uv.ModCtrl:
			r.menuIndex++
			if r.menuIndex >= len(r.menuItems) {
				r.menuIndex = 0
			}
			r.syncMenu()
			return true
		case key.Code == uv.KeyUp && key.Mod == 0,
			key.Code == 'p' && key.Mod == uv.ModCtrl:
			r.menuIndex--
			if r.menuIndex < 0 {
				r.menuIndex = len(r.menuItems) - 1
			}
			r.syncMenu()
			return true
		case key.Code == uv.KeyEscape:
			r.hideCompletionMenu()
			return true
		case key.Code == uv.KeyEnter && key.Mod == 0:
			if r.menuIndex < len(r.menuItems) {
				r.textInput.SetValue(r.menuItems[r.menuIndex])
				r.textInput.CursorEnd()
			}
			r.hideCompletionMenu()
			// Fall through — onSubmit will be called by the text input.
			return false
		}
	}

	switch {
	case key.Code == 'c' && key.Mod == uv.ModCtrl:
		if r.textInput.Value() != "" {
			r.textInput.SetValue("")
			r.hideCompletionMenu()
			return true
		}
		r.quitOnce.Do(func() { close(r.quit) })
		return true

	case key.Code == 'd' && key.Mod == uv.ModCtrl:
		if r.textInput.Value() == "" {
			r.quitOnce.Do(func() { close(r.quit) })
			return true
		}
		return false

	case key.Code == uv.KeyUp && key.Mod == 0:
		if !r.menuVisible {
			r.history.Navigate(-1, r.textInput)
			return true
		}
	case key.Code == uv.KeyDown && key.Mod == 0:
		if !r.menuVisible {
			r.history.Navigate(1, r.textInput)
			return true
		}

	case key.Code == 'l' && key.Mod == uv.ModCtrl:
		r.mu.Lock()
		r.entryContainer.Clear()
		r.mu.Unlock()
		return true
	}

	return false
}

// ── eval ────────────────────────────────────────────────────────────────────

func (r *replComponent) startEval(expr string) {
	r.mu.Lock()
	ev := r.activeEntryView()

	result, err := dang.Parse("repl", []byte(expr))
	if err != nil {
		ev.mu.Lock()
		ev.entry.writeLogLine(errorStyle.Render(fmt.Sprintf("parse error: %v", err)))
		ev.Update()
		ev.mu.Unlock()
		r.mu.Unlock()
		return
	}

	forms := result.(*dang.ModuleBlock).Forms

	if r.debug {
		ev.mu.Lock()
		for _, node := range forms {
			ev.entry.writeLogLine(dimStyle.Render(fmt.Sprintf("%# v", pretty.Formatter(node))))
		}
		ev.Update()
		ev.mu.Unlock()
	}

	fresh := hm.NewSimpleFresher()
	_, err = dang.InferFormsWithPhases(r.ctx, forms, r.typeEnv, fresh)
	if err != nil {
		ev.mu.Lock()
		ev.entry.writeLogLine(errorStyle.Render(fmt.Sprintf("type error: %v", err)))
		ev.Update()
		ev.mu.Unlock()
		r.mu.Unlock()
		return
	}

	evalCtx, cancel := context.WithCancel(r.ctx)
	r.evaluating = true
	r.cancelEval = cancel
	evalEnv := r.evalEnv
	debug := r.debug
	// Direct Dagger log output to this entry for the duration of eval.
	if r.daggerLog != nil {
		r.daggerLog.SetTarget(ev)
	}
	r.mu.Unlock()

	// Swap text input for spinner via the slot.
	r.inputSlot.Set(r.spinner)
	r.spinner.Start()
	r.tui.SetFocus(nil)
	// Route input to onKey during eval so Ctrl+C can cancel.
	removeListener := r.tui.AddInputListener(func(ev uv.Event) bool {
		if kp, ok := ev.(uv.KeyPressEvent); ok {
			if r.onKey(uv.Key(kp)) {
				r.tui.RequestRender(false)
				return true
			}
		}
		return false
	})
	r.tui.RequestRender(false)

	go func() {
		var logs []string
		var results []string

		var stdoutBuf bytes.Buffer
		ctx := ioctx.StdoutToContext(evalCtx, &stdoutBuf)
		ctx = ioctx.StderrToContext(ctx, &stdoutBuf)

		for _, node := range forms {
			val, err := dang.EvalNode(ctx, evalEnv, node)

			if evalCtx.Err() != nil {
				r.finishEval(ev, nil, nil, true, removeListener)
				return
			}

			if stdoutBuf.Len() > 0 {
				logs = append(logs, strings.Split(strings.TrimRight(stdoutBuf.String(), "\n"), "\n")...)
				stdoutBuf.Reset()
			}

			if err != nil {
				results = append(results, errorStyle.Render(fmt.Sprintf("evaluation error: %v", err)))
				r.finishEval(ev, logs, results, false, removeListener)
				return
			}

			results = append(results, resultStyle.Render(fmt.Sprintf("=> %s", val.String())))
			if debug {
				results = append(results, dimStyle.Render(fmt.Sprintf("%# v", pretty.Formatter(val))))
			}
		}

		r.finishEval(ev, logs, results, false, removeListener)
	}()
}

func (r *replComponent) finishEval(ev *entryView, logs, results []string, cancelled bool, removeListener func()) {
	r.spinner.Stop()
	removeListener()

	r.mu.Lock()
	r.evaluating = false
	r.cancelEval = nil
	if ev != nil {
		ev.mu.Lock()
		if cancelled {
			ev.entry.writeLogLine(errorStyle.Render("cancelled"))
		} else {
			for _, line := range logs {
				ev.entry.writeLogLine(line)
			}
			for _, line := range results {
				ev.entry.writeResult(line)
			}
		}
		ev.Update()
		ev.mu.Unlock()
	}
	if !cancelled {
		r.refreshCompletions()
	}
	r.mu.Unlock()

	// Swap spinner back to text input via the slot.
	r.inputSlot.Set(r.textInput)
	r.tui.SetFocus(r.textInput)
	r.tui.RequestRender(false)
}

// ── doc browser ─────────────────────────────────────────────────────────────

func (r *replComponent) showDocBrowser() {
	db := newDocBrowserOverlay(r.typeEnv)
	db.onExit = func() {
		if r.docHandle != nil {
			r.docHandle.Hide()
			r.docHandle = nil
			r.docBrowser = nil
			r.tui.SetFocus(r.textInput)
		}
	}
	r.docBrowser = db
	r.docHandle = r.tui.ShowOverlay(db, &pitui.OverlayOptions{
		Width:     pitui.SizePct(100),
		MaxHeight: pitui.SizePct(100),
		Anchor:    pitui.AnchorTopLeft,
	})
	r.tui.SetFocus(db)
}

// ── entry point ─────────────────────────────────────────────────────────────

func runREPLTUI(ctx context.Context, importConfigs []dang.ImportConfig, moduleDir string, debug bool) error {
	term := pitui.NewProcessTerminal()
	tui := pitui.New(term)
	tui.SetShowHardwareCursor(true)

	// Install debug writer early so the loading spinner is captured.
	var debugRenderFile *os.File
	if os.Getenv("DANG_DEBUG_RENDER") != "" {
		logPath := "/tmp/dang_render_debug.log"
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err == nil {
			debugRenderFile = f
			tui.SetDebugWriter(f)
		}
	}

	if err := tui.Start(); err != nil {
		return fmt.Errorf("TUI start: %w", err)
	}

	// If there's a Dagger module, load it with a spinner visible.
	daggerLog := newPituiSyncWriter()
	defer daggerLog.Stop()
	var daggerConn *dagger.Client
	if moduleDir != "" {
		loadSp := pitui.NewSpinner()
		loadSp.Style = func(s string) string {
			return lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Render(s)
		}
		loadSp.Label = dimStyle.Render(fmt.Sprintf("Loading Dagger module from %s...", moduleDir))
		tui.AddChild(loadSp)
		loadSp.Start()

		dag, err := dagger.Connect(ctx,
			dagger.WithLogOutput(daggerLog),
			dagger.WithEnvironmentVariable("DAGGER_PROGRESS", "dots"),
		)
		if err != nil {
			loadSp.Stop()
			loadSp.Label = errorStyle.Render(fmt.Sprintf("Failed to connect to Dagger: %v", err))
			tui.RequestRender(false)
		} else {
			daggerConn = dag
			provider := dang.NewGraphQLClientProvider(dang.GraphQLConfig{})
			client, schema, err := provider.ServeDaggerModule(ctx, dag, moduleDir)
			if err != nil {
				loadSp.Stop()
				loadSp.Label = errorStyle.Render(fmt.Sprintf("Failed to load Dagger module: %v", err))
				tui.RequestRender(false)
			} else {
				importConfigs = append(importConfigs, dang.ImportConfig{
					Name:       "Dagger",
					Client:     client,
					Schema:     schema,
					AutoImport: true,
				})
			}
			loadSp.Stop()
		}
		tui.RemoveChild(loadSp)
	}

	if len(importConfigs) > 0 {
		ctx = dang.ContextWithImportConfigs(ctx, importConfigs...)
	}

	repl := newReplComponent(ctx, tui, importConfigs, debug)
	if debugRenderFile != nil {
		repl.debugRender = true
		repl.debugRenderFile = debugRenderFile
	}
	repl.daggerLog = daggerLog
	repl.install()

	if daggerConn != nil {
		defer daggerConn.Close() //nolint:errcheck
	}

	// Wait for quit signal or interrupt.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-repl.quit:
	case <-sigCh:
	case <-ctx.Done():
	}

	signal.Stop(sigCh)
	if debugRenderFile != nil {
		debugRenderFile.Close()
	}
	tui.Stop()
	fmt.Println("Goodbye!")
	return nil
}
