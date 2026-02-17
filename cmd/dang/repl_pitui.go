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

	"github.com/kr/pretty"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/ioctx"
	"github.com/vito/dang/pkg/pitui"
)

// ── sync writer (streams dagger output into TUI) ────────────────────────────

type pituiSyncWriter struct {
	mu     sync.Mutex
	output *outputLog
	tui    *pitui.TUI
}

func (w *pituiSyncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	o := w.output
	t := w.tui
	w.mu.Unlock()
	if o != nil {
		o.WriteString(string(p))
		if t != nil {
			t.RequestRender(false)
		}
	}
	return len(p), nil
}

func (w *pituiSyncWriter) SetTarget(output *outputLog, tui *pitui.TUI) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.output = output
	w.tui = tui
}

// ── output log component ────────────────────────────────────────────────────

// outputLog renders an append-only list of lines. The REPL pushes content
// via WriteString and WriteLine; the component owns its data and needs no
// external locking during render.
type outputLog struct {
	mu    sync.Mutex
	lines []string // raw lines (may contain ANSI)
	dirty bool

	cachedRendered []string
	cachedWidth    int
}

// WriteString appends raw text (may contain newlines) to the log.
func (o *outputLog) WriteString(s string) {
	if s == "" {
		return
	}
	o.mu.Lock()
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	o.lines = append(o.lines, parts...)
	o.dirty = true
	o.mu.Unlock()
}

// WriteLine appends a single line to the log.
func (o *outputLog) WriteLine(line string) {
	o.mu.Lock()
	o.lines = append(o.lines, line)
	o.dirty = true
	o.mu.Unlock()
}

// Clear removes all content.
func (o *outputLog) Clear() {
	o.mu.Lock()
	o.lines = nil
	o.dirty = true
	o.cachedRendered = nil
	o.mu.Unlock()
}

func (o *outputLog) Invalidate() {
	o.mu.Lock()
	o.dirty = true
	o.cachedRendered = nil
	o.mu.Unlock()
}

func (o *outputLog) Render(ctx pitui.RenderContext) pitui.RenderResult {
	o.mu.Lock()
	dirty := o.dirty
	if !dirty && o.cachedRendered != nil && o.cachedWidth == ctx.Width {
		out := o.cachedRendered
		o.mu.Unlock()
		return pitui.RenderResult{Lines: out, Dirty: false}
	}

	// Snapshot raw lines under lock, then release for processing.
	raw := make([]string, len(o.lines))
	copy(raw, o.lines)
	o.mu.Unlock()

	rendered := make([]string, len(raw))
	for i, line := range raw {
		line = pitui.ExpandTabs(line, 8)
		if pitui.VisibleWidth(line) > ctx.Width {
			line = pitui.Truncate(line, ctx.Width, "")
		}
		rendered[i] = line
	}

	o.mu.Lock()
	o.cachedRendered = rendered
	o.cachedWidth = ctx.Width
	o.dirty = false
	o.mu.Unlock()

	return pitui.RenderResult{Lines: rendered, Dirty: true}
}

// ── spinner line component ──────────────────────────────────────────────────

type evalSpinnerLine struct {
	spinner *pitui.Spinner
}

func (e *evalSpinnerLine) Invalidate() {}
func (e *evalSpinnerLine) Render(ctx pitui.RenderContext) pitui.RenderResult {
	return e.spinner.Render(ctx)
}

// ── completion menu overlay ─────────────────────────────────────────────────

type completionOverlay struct {
	items      []string
	index      int
	maxVisible int
}

func (c *completionOverlay) Invalidate() {}

func (c *completionOverlay) Render(ctx pitui.RenderContext) pitui.RenderResult {
	if len(c.items) == 0 {
		return pitui.RenderResult{Dirty: true}
	}
	lines := strings.Split(renderMenuBox(c.items, c.index, c.maxVisible, ctx.Width), "\n")
	return pitui.RenderResult{Lines: lines, Dirty: true}
}

// renderMenuBox renders the completion dropdown as a bordered box string.
func renderMenuBox(items []string, index, maxVisible, width int) string {
	visible := min(len(items), maxVisible)
	start := 0
	if index >= visible {
		start = index - visible + 1
	}
	end := start + visible

	maxW := 0
	for i := start; i < end && i < len(items); i++ {
		if w := lipgloss.Width(items[i]); w > maxW {
			maxW = w
		}
	}
	if maxW > 60 {
		maxW = 60
	}
	if maxW < 20 {
		maxW = 20
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
	item docItem
}

func (d *detailBubble) Invalidate() {}

func (d *detailBubble) Render(ctx pitui.RenderContext) pitui.RenderResult {
	if d.item.name == "" {
		return pitui.RenderResult{Dirty: true}
	}

	innerW := max(10, ctx.Width-2)

	docTextStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("249"))
	argNameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("117"))
	argTypeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	dimSt := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	titleLine := detailTitleStyle.Render(d.item.name)
	lines := []string{titleLine}

	detail := renderDocDetail(d.item, innerW, docTextStyle, argNameStyle, argTypeStyle, dimSt)
	lines = append(lines, detail...)

	inner := strings.Join(lines, "\n")
	box := detailBorderStyle.Width(innerW).Render(inner)
	return pitui.RenderResult{
		Lines: strings.Split(box, "\n"),
		Dirty: true,
	}
}

// ── REPL component ─────────────────────────────────────────────────────────

type replComponent struct {
	mu sync.Mutex

	// Dang state
	importConfigs []dang.ImportConfig
	debug         bool
	typeEnv       dang.Env
	evalEnv       dang.EvalEnv
	ctx           context.Context

	// UI state
	tui         *pitui.TUI
	textInput   *pitui.TextInput
	output      *outputLog
	spinner     *pitui.Spinner
	spinnerLine *evalSpinnerLine
	inputSlot   *pitui.Slot // swaps between textInput and spinnerLine

	quit chan struct{}

	// Completion
	completions      []string
	menuVisible      bool
	menuItems        []string
	menuCompletions  []dang.Completion // parallel to menuItems; Detail/Documentation
	menuIndex        int
	menuMaxVisible   int
	menuOverlay      *completionOverlay
	menuHandle       *pitui.OverlayHandle
	detailBubble     *detailBubble
	detailHandle     *pitui.OverlayHandle

	// Eval
	evaluating bool
	cancelEval context.CancelFunc

	// History
	history      []string
	historyIndex int
	historyFile  string

	// Doc browser
	docBrowser *docBrowserOverlay
	docHandle  *pitui.OverlayHandle
}

func newReplComponent(ctx context.Context, tui *pitui.TUI, importConfigs []dang.ImportConfig, debug bool) *replComponent {
	typeEnv, evalEnv := buildEnvFromImports(importConfigs)

	ti := pitui.NewTextInput(promptStyle.Render("dang> "))

	r := &replComponent{
		importConfigs:  importConfigs,
		debug:          debug,
		typeEnv:        typeEnv,
		evalEnv:        evalEnv,
		ctx:            ctx,
		tui:            tui,
		textInput:      ti,
		menuMaxVisible: 8,
		historyIndex:   -1,
		historyFile:    "/tmp/dang_history",
		quit:           make(chan struct{}),
	}
	r.output = &outputLog{dirty: true}

	// Spinner
	sp := pitui.NewSpinner(tui)
	sp.Style = func(s string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Render(s)
	}
	sp.Label = dimStyle.Render("Evaluating... (Ctrl+C to cancel)")
	r.spinner = sp
	r.spinnerLine = &evalSpinnerLine{spinner: sp}

	// Input slot starts with text input.
	r.inputSlot = pitui.NewSlot(ti)

	// Text input callbacks.
	ti.SuggestionStyle = func(s string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(s)
	}
	ti.OnSubmit = r.onSubmit
	ti.OnKey = r.onKey

	// Welcome message.
	r.output.WriteLine(welcomeStyle.Render("Welcome to Dang REPL v0.1.0"))
	if len(importConfigs) > 0 {
		var names []string
		for _, ic := range importConfigs {
			names = append(names, ic.Name)
		}
		r.output.WriteLine(dimStyle.Render(fmt.Sprintf("Imports: %s", strings.Join(names, ", "))))
	}
	r.output.WriteLine("")
	r.output.WriteLine(dimStyle.Render("Type :help for commands, Tab for completion, Ctrl+C to exit"))
	r.output.WriteLine("")

	r.completions = r.buildCompletionsPitui()
	r.loadHistoryPitui()

	return r
}

// install adds the repl's components to the TUI.
func (r *replComponent) install() {
	r.tui.AddChild(r.output)
	r.tui.AddChild(r.inputSlot)
	r.tui.SetFocus(r.textInput)
}

// onSubmit handles Enter in the text input.
func (r *replComponent) onSubmit(line string) bool {
	if line == "" {
		return false
	}

	r.addHistoryPitui(line)

	r.output.WriteLine(promptStyle.Render("dang> ") + line)

	r.hideCompletionMenu()

	if strings.HasPrefix(line, ":") {
		r.handleCommandPitui(line[1:])
		r.updateCompletionMenuPitui()
		return true
	}

	r.startEvalPitui(line)
	return true
}

// onKey handles keys not consumed by the text input editor.
func (r *replComponent) onKey(data []byte) bool {
	s := string(data)

	// While evaluating: Ctrl+C cancels.
	if r.evaluating {
		if s == pitui.KeyCtrlC {
			if r.cancelEval != nil {
				r.cancelEval()
			}
			return true
		}
		return true // swallow everything else
	}

	// Completion menu navigation.
	if r.menuVisible {
		switch s {
		case pitui.KeyTab:
			if r.menuIndex < len(r.menuItems) {
				r.textInput.SetValue(r.menuItems[r.menuIndex])
				r.textInput.CursorEnd()
			}
			r.hideCompletionMenu()
			r.updateCompletionMenuPitui()
			return true
		case pitui.KeyDown, pitui.KeyCtrlN:
			r.menuIndex++
			if r.menuIndex >= len(r.menuItems) {
				r.menuIndex = 0
			}
			r.syncMenu()
			return true
		case pitui.KeyUp, pitui.KeyCtrlP:
			r.menuIndex--
			if r.menuIndex < 0 {
				r.menuIndex = len(r.menuItems) - 1
			}
			r.syncMenu()
			return true
		case pitui.KeyEscape:
			r.hideCompletionMenu()
			return true
		case pitui.KeyEnter:
			if r.menuIndex < len(r.menuItems) {
				r.textInput.SetValue(r.menuItems[r.menuIndex])
				r.textInput.CursorEnd()
			}
			r.hideCompletionMenu()
			// Fall through — onSubmit will be called by the text input.
			return false
		}
	}

	switch s {
	case pitui.KeyCtrlC:
		if r.textInput.Value() != "" {
			r.textInput.SetValue("")
			r.hideCompletionMenu()
			return true
		}
		close(r.quit)
		return true

	case pitui.KeyCtrlD:
		if r.textInput.Value() == "" {
			close(r.quit)
			return true
		}
		return false

	case pitui.KeyUp:
		if !r.menuVisible {
			r.navigateHistoryPitui(-1)
			return true
		}
	case pitui.KeyDown:
		if !r.menuVisible {
			r.navigateHistoryPitui(1)
			return true
		}

	case pitui.KeyCtrlL:
		r.output.Clear()
		return true
	}

	// After any key that modifies input, update completions.
	defer r.updateCompletionMenuPitui()

	return false
}

// ── eval ────────────────────────────────────────────────────────────────────

func (r *replComponent) startEvalPitui(expr string) {
	r.mu.Lock()

	result, err := dang.Parse("repl", []byte(expr))
	if err != nil {
		r.mu.Unlock()
		r.output.WriteLine(errorStyle.Render(fmt.Sprintf("parse error: %v", err)))
		return
	}

	forms := result.(*dang.ModuleBlock).Forms

	if r.debug {
		for _, node := range forms {
			r.output.WriteLine(dimStyle.Render(fmt.Sprintf("%# v", pretty.Formatter(node))))
		}
	}

	fresh := hm.NewSimpleFresher()
	_, err = dang.InferFormsWithPhases(r.ctx, forms, r.typeEnv, fresh)
	if err != nil {
		r.mu.Unlock()
		r.output.WriteLine(errorStyle.Render(fmt.Sprintf("type error: %v", err)))
		return
	}

	evalCtx, cancel := context.WithCancel(r.ctx)
	r.evaluating = true
	r.cancelEval = cancel
	evalEnv := r.evalEnv
	debug := r.debug
	r.mu.Unlock()

	// Swap text input for spinner via the slot.
	r.inputSlot.Set(r.spinnerLine)
	r.spinner.Start()
	r.tui.SetFocus(nil)
	// Route input to onKey during eval.
	removeListener := r.tui.AddInputListener(func(data []byte) *pitui.InputListenerResult {
		if r.onKey(data) {
			r.tui.RequestRender(false)
			return &pitui.InputListenerResult{Consume: true}
		}
		return nil
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
				r.finishEval(nil, nil, true, removeListener)
				return
			}

			if stdoutBuf.Len() > 0 {
				for _, line := range strings.Split(strings.TrimRight(stdoutBuf.String(), "\n"), "\n") {
					logs = append(logs, line)
				}
				stdoutBuf.Reset()
			}

			if err != nil {
				results = append(results, errorStyle.Render(fmt.Sprintf("evaluation error: %v", err)))
				r.finishEval(logs, results, false, removeListener)
				return
			}

			results = append(results, resultStyle.Render(fmt.Sprintf("=> %s", val.String())))
			if debug {
				results = append(results, dimStyle.Render(fmt.Sprintf("%# v", pretty.Formatter(val))))
			}
		}

		r.finishEval(logs, results, false, removeListener)
	}()
}

func (r *replComponent) finishEval(logs, results []string, cancelled bool, removeListener func()) {
	r.spinner.Stop()
	removeListener()

	if cancelled {
		r.output.WriteLine(errorStyle.Render("cancelled"))
	} else {
		for _, line := range logs {
			r.output.WriteLine(line)
		}
		for _, line := range results {
			r.output.WriteLine(line)
		}
		r.mu.Lock()
		r.refreshCompletionsPitui()
		r.mu.Unlock()
	}

	r.mu.Lock()
	r.evaluating = false
	r.cancelEval = nil
	r.mu.Unlock()

	// Swap spinner back to text input via the slot.
	r.inputSlot.Set(r.textInput)
	r.tui.SetFocus(r.textInput)
	r.tui.RequestRender(false)
}

// ── completion menu ─────────────────────────────────────────────────────────

func (r *replComponent) hideCompletionMenu() {
	r.menuVisible = false
	r.menuItems = nil
	r.menuCompletions = nil
	if r.menuHandle != nil {
		r.menuHandle.Hide()
		r.menuHandle = nil
		r.menuOverlay = nil
	}
	r.hideDetailBubble()
}

func (r *replComponent) showCompletionMenu() {
	xOff := r.completionXOffsetPitui()
	opts := &pitui.OverlayOptions{
		Width:           pitui.SizeAbs(40),
		MaxHeight:       pitui.SizeAbs(r.menuMaxVisible + 2), // items + border
		Anchor:          pitui.AnchorBottomLeft,
		ContentRelative: true,
		OffsetX:         xOff,
		OffsetY:         -1, // above the input line
		NoFocus:         true,
	}
	if r.menuHandle != nil {
		// Reuse existing overlay — just update position and data.
		r.menuOverlay.items = r.menuItems
		r.menuOverlay.index = r.menuIndex
		r.menuHandle.SetOptions(opts)
	} else {
		r.menuOverlay = &completionOverlay{
			items:      r.menuItems,
			index:      r.menuIndex,
			maxVisible: r.menuMaxVisible,
		}
		r.menuHandle = r.tui.ShowOverlay(r.menuOverlay, opts)
	}
	r.syncDetailBubble()
}

func (r *replComponent) syncMenu() {
	if r.menuOverlay != nil {
		r.menuOverlay.items = r.menuItems
		r.menuOverlay.index = r.menuIndex
	}
	r.syncDetailBubble()
	r.tui.RequestRender(false)
}

func (r *replComponent) showDetailBubble() {
	if r.detailBubble == nil {
		r.detailBubble = &detailBubble{}
		r.detailHandle = r.tui.ShowOverlay(r.detailBubble, &pitui.OverlayOptions{
			Width:     pitui.SizePct(35),
			MaxHeight: pitui.SizeAbs(15),
			Anchor:    pitui.AnchorTopRight,
			Margin:    pitui.OverlayMargin{Top: 1, Right: 1},
			NoFocus:   true,
		})
	}
}

func (r *replComponent) hideDetailBubble() {
	if r.detailHandle != nil {
		r.detailHandle.Hide()
		r.detailHandle = nil
		r.detailBubble = nil
	}
}

func (r *replComponent) syncDetailBubble() {
	if !r.menuVisible || len(r.menuCompletions) == 0 {
		r.hideDetailBubble()
		return
	}
	idx := r.menuIndex
	if idx < 0 || idx >= len(r.menuCompletions) {
		r.hideDetailBubble()
		return
	}
	c := r.menuCompletions[idx]

	item, found := docItemFromEnv(r.typeEnv, c.Label)
	if !found {
		item, found = r.resolveCompletionDocItem(c)
	}
	if !found {
		if c.Detail == "" && c.Documentation == "" {
			r.hideDetailBubble()
			return
		}
		item = docItem{
			name:    c.Label,
			typeStr: c.Detail,
			doc:     c.Documentation,
		}
	}

	r.showDetailBubble()
	r.detailBubble.item = item
}

// resolveCompletionDocItem tries to resolve a member completion's docItem
// by inferring the receiver type from the current input.
func (r *replComponent) resolveCompletionDocItem(c dang.Completion) (docItem, bool) {
	val := r.textInput.Value()
	dotIdx := -1
	for i := len(val) - 1; i >= 0; i-- {
		if val[i] == '.' {
			dotIdx = i
			break
		}
	}
	if dotIdx < 0 {
		return docItem{}, false
	}
	receiverText := val[:dotIdx]
	receiverType := dang.InferReceiverType(r.ctx, r.typeEnv, receiverText)
	if receiverType == nil {
		return docItem{}, false
	}
	unwrapped := unwrapType(receiverType)
	env, ok := unwrapped.(dang.Env)
	if !ok {
		return docItem{}, false
	}
	return docItemFromEnv(env, c.Label)
}

func (r *replComponent) completionXOffsetPitui() int {
	val := r.textInput.Value()
	promptWidth := lipgloss.Width(promptStyle.Render("dang> "))
	i := len(val) - 1
	for i >= 0 && isIdentByte(val[i]) {
		i--
	}
	if i >= 0 && val[i] == '.' {
		i--
		for i >= 0 && isIdentByte(val[i]) {
			i--
		}
	}
	tokenStart := i + 1
	return promptWidth + tokenStart
}

func (r *replComponent) updateCompletionMenuPitui() {
	val := r.textInput.Value()

	if val == "" || strings.HasPrefix(val, ":") {
		r.hideCompletionMenu()
		r.textInput.Suggestion = ""
		return
	}

	cursorPos := len(val)
	completions := dang.CompleteInput(r.ctx, r.typeEnv, val, cursorPos)

	if len(completions) > 0 {
		prefix, partial := splitForSuggestion(val)
		var matches []string
		var matchCompletions []dang.Completion
		partialLower := strings.ToLower(partial)
		for _, c := range completions {
			cLower := strings.ToLower(c.Label)
			if cLower == partialLower {
				continue
			}
			if strings.HasPrefix(cLower, partialLower) {
				matches = append(matches, prefix+c.Label)
				matchCompletions = append(matchCompletions, c)
			}
		}
		matches, matchCompletions = sortByCaseWithCompletions(matches, matchCompletions, prefix, partial)
		r.setMenuPitui(matches, matchCompletions)
		if len(matches) > 0 {
			r.textInput.Suggestion = matches[0]
		} else {
			r.textInput.Suggestion = ""
		}
		return
	}

	// Fallback: static completions.
	word := lastIdent(val)
	if word == "" {
		r.hideCompletionMenu()
		r.textInput.Suggestion = ""
		return
	}

	var exactCase, otherCase []string
	wordLower := strings.ToLower(word)
	for _, c := range r.completions {
		cLower := strings.ToLower(c)
		if cLower == wordLower {
			continue
		}
		if strings.HasPrefix(c, word) {
			exactCase = append(exactCase, c)
		} else if strings.HasPrefix(cLower, wordLower) {
			otherCase = append(otherCase, c)
		}
	}
	matches := append(exactCase, otherCase...)
	r.setMenuPitui(matches, nil)
	if len(matches) > 0 {
		r.textInput.Suggestion = matches[0]
	} else {
		r.textInput.Suggestion = ""
	}
}

func (r *replComponent) setMenuPitui(matches []string, completions []dang.Completion) {
	if len(matches) <= 1 {
		r.hideCompletionMenu()
		return
	}
	r.menuItems = matches
	r.menuCompletions = completions
	r.menuVisible = true
	if r.menuIndex >= len(matches) {
		r.menuIndex = 0
	}
	r.showCompletionMenu()
}

// sortByCaseWithCompletions sorts matches (and their parallel completions)
// so that exact-case-prefix matches come before case-insensitive ones.
func sortByCaseWithCompletions(matches []string, completions []dang.Completion, prefix, partial string) ([]string, []dang.Completion) {
	var exactM, otherM []string
	var exactC, otherC []dang.Completion
	for i, m := range matches {
		suffix := strings.TrimPrefix(m, prefix)
		if strings.HasPrefix(suffix, partial) {
			exactM = append(exactM, m)
			if i < len(completions) {
				exactC = append(exactC, completions[i])
			}
		} else {
			otherM = append(otherM, m)
			if i < len(completions) {
				otherC = append(otherC, completions[i])
			}
		}
	}
	return append(exactM, otherM...), append(exactC, otherC...)
}

// ── commands ────────────────────────────────────────────────────────────────

func (r *replComponent) handleCommandPitui(cmdLine string) {
	parts := strings.Fields(cmdLine)
	if len(parts) == 0 {
		r.output.WriteLine(errorStyle.Render("empty command"))
		return
	}

	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "help":
		r.output.WriteLine("Available commands:")
		r.output.WriteLine(dimStyle.Render("  :help      - Show this help"))
		r.output.WriteLine(dimStyle.Render("  :exit      - Exit the REPL"))
		r.output.WriteLine(dimStyle.Render("  :doc       - Interactive API browser"))
		r.output.WriteLine(dimStyle.Render("  :env       - Show environment bindings"))
		r.output.WriteLine(dimStyle.Render("  :type      - Show type of an expression"))
		r.output.WriteLine(dimStyle.Render("  :find      - Find functions/types by pattern"))
		r.output.WriteLine(dimStyle.Render("  :reset     - Reset the environment"))
		r.output.WriteLine(dimStyle.Render("  :clear     - Clear the screen"))
		r.output.WriteLine(dimStyle.Render("  :debug     - Toggle debug mode"))
		r.output.WriteLine(dimStyle.Render("  :version   - Show version info"))
		r.output.WriteLine(dimStyle.Render("  :quit      - Exit the REPL"))
		r.output.WriteLine("")
		r.output.WriteLine(dimStyle.Render("Type Dang expressions to evaluate them."))
		r.output.WriteLine(dimStyle.Render("Tab for completion, Up/Down for history, Ctrl+L to clear."))

	case "exit", "quit":
		close(r.quit)
		return

	case "clear":
		r.output.Clear()

	case "reset":
		r.mu.Lock()
		r.typeEnv, r.evalEnv = buildEnvFromImports(r.importConfigs)
		r.refreshCompletionsPitui()
		r.mu.Unlock()
		r.output.WriteLine(resultStyle.Render("Environment reset."))

	case "debug":
		r.mu.Lock()
		r.debug = !r.debug
		status := "disabled"
		if r.debug {
			status = "enabled"
		}
		r.mu.Unlock()
		r.output.WriteLine(resultStyle.Render(fmt.Sprintf("Debug mode %s.", status)))

	case "env":
		r.envCommandPitui(args)

	case "version":
		r.output.WriteLine(resultStyle.Render("Dang REPL v0.1.0"))
		r.mu.Lock()
		configs := r.importConfigs
		r.mu.Unlock()
		if len(configs) > 0 {
			var names []string
			for _, ic := range configs {
				names = append(names, ic.Name)
			}
			r.output.WriteLine(dimStyle.Render(fmt.Sprintf("Imports: %s", strings.Join(names, ", "))))
		} else {
			r.output.WriteLine(dimStyle.Render("No imports configured (create a dang.toml)"))
		}

	case "type":
		r.typeCommandPitui(args)

	case "find", "search":
		r.findCommandPitui(args)

	case "history":
		r.output.WriteLine("Recent history:")
		start := 0
		if len(r.history) > 20 {
			start = len(r.history) - 20
		}
		for i := start; i < len(r.history); i++ {
			r.output.WriteLine(dimStyle.Render(fmt.Sprintf("  %d: %s", i+1, r.history[i])))
		}

	case "doc":
		r.showDocBrowser()
		return

	default:
		r.output.WriteLine(errorStyle.Render(fmt.Sprintf("unknown command: %s (type :help for available commands)", cmd)))
	}
}

func (r *replComponent) envCommandPitui(args []string) {
	filter := ""
	showAll := false
	if len(args) > 0 {
		if args[0] == "all" {
			showAll = true
		} else {
			filter = args[0]
		}
	}
	r.output.WriteLine("Current environment bindings:")
	r.output.WriteLine("")
	r.mu.Lock()
	bindings := r.typeEnv.Bindings(dang.PublicVisibility)
	r.mu.Unlock()
	count := 0
	for name, scheme := range bindings {
		if filter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(filter)) {
			continue
		}
		if !showAll && count >= 20 {
			r.output.WriteLine(dimStyle.Render("  ... use ':env all' to see all"))
			break
		}
		t, _ := scheme.Type()
		if t != nil {
			r.output.WriteLine(dimStyle.Render(fmt.Sprintf("  %s : %s", name, t)))
		} else {
			r.output.WriteLine(dimStyle.Render(fmt.Sprintf("  %s", name)))
		}
		count++
	}
	r.output.WriteLine("")
	r.output.WriteLine(dimStyle.Render("Use ':doc' for interactive API browsing"))
}

func (r *replComponent) typeCommandPitui(args []string) {
	if len(args) == 0 {
		r.output.WriteLine(dimStyle.Render("Usage: :type <expression>"))
		return
	}
	expr := strings.Join(args, " ")
	result, err := dang.Parse("type-check", []byte(expr))
	if err != nil {
		r.output.WriteLine(errorStyle.Render(fmt.Sprintf("parse error: %v", err)))
		return
	}
	node := result.(*dang.Block)
	r.mu.Lock()
	inferredType, err := dang.Infer(r.ctx, r.typeEnv, node, false)
	r.mu.Unlock()
	if err != nil {
		r.output.WriteLine(errorStyle.Render(fmt.Sprintf("type error: %v", err)))
		return
	}
	r.output.WriteLine(fmt.Sprintf("Expression: %s", expr))
	r.output.WriteLine(resultStyle.Render(fmt.Sprintf("Type: %s", inferredType)))
	trimmed := strings.TrimSpace(expr)
	if !strings.Contains(trimmed, " ") {
		r.mu.Lock()
		scheme, found := r.typeEnv.SchemeOf(trimmed)
		r.mu.Unlock()
		if found {
			if t, _ := scheme.Type(); t != nil {
				r.output.WriteLine(dimStyle.Render(fmt.Sprintf("Scheme: %s", scheme)))
			}
		}
	}
}

func (r *replComponent) findCommandPitui(args []string) {
	if len(args) == 0 {
		r.output.WriteLine(dimStyle.Render("Usage: :find <pattern>"))
		return
	}
	pattern := strings.ToLower(args[0])
	r.output.WriteLine(fmt.Sprintf("Searching for '%s'...", pattern))
	r.mu.Lock()
	bindings := r.typeEnv.Bindings(dang.PublicVisibility)
	namedTypes := r.typeEnv.NamedTypes()
	r.mu.Unlock()
	found := false
	for name, scheme := range bindings {
		if strings.Contains(strings.ToLower(name), pattern) {
			t, _ := scheme.Type()
			if t != nil {
				r.output.WriteLine(dimStyle.Render(fmt.Sprintf("  %s : %s", name, t)))
			} else {
				r.output.WriteLine(dimStyle.Render(fmt.Sprintf("  %s", name)))
			}
			found = true
		}
	}
	for name, env := range namedTypes {
		if strings.Contains(strings.ToLower(name), pattern) {
			doc := env.GetModuleDocString()
			if doc != "" {
				if len(doc) > 60 {
					doc = doc[:57] + "..."
				}
				r.output.WriteLine(dimStyle.Render(fmt.Sprintf("  %s - %s", name, doc)))
			} else {
				r.output.WriteLine(dimStyle.Render(fmt.Sprintf("  %s", name)))
			}
			found = true
		}
	}
	if !found {
		r.output.WriteLine(dimStyle.Render(fmt.Sprintf("No matches found for '%s'", pattern)))
	}
}

// ── completions ─────────────────────────────────────────────────────────────

func (r *replComponent) buildCompletionsPitui() []string {
	return buildCompletionList(r.typeEnv)
}

func (r *replComponent) refreshCompletionsPitui() {
	r.completions = r.buildCompletionsPitui()
}

// ── history ─────────────────────────────────────────────────────────────────

func (r *replComponent) addHistoryPitui(line string) {
	if len(r.history) > 0 && r.history[len(r.history)-1] == line {
		r.historyIndex = -1
		return
	}
	r.history = append(r.history, line)
	r.historyIndex = -1
	r.saveHistoryPitui()
}

func (r *replComponent) navigateHistoryPitui(direction int) {
	if len(r.history) == 0 {
		return
	}
	if direction < 0 {
		if r.historyIndex == -1 {
			r.historyIndex = len(r.history) - 1
		} else if r.historyIndex > 0 {
			r.historyIndex--
		}
	} else {
		if r.historyIndex == -1 {
			return
		}
		r.historyIndex++
		if r.historyIndex >= len(r.history) {
			r.historyIndex = -1
			r.textInput.SetValue("")
			return
		}
	}
	if r.historyIndex >= 0 && r.historyIndex < len(r.history) {
		r.textInput.SetValue(r.history[r.historyIndex])
		r.textInput.CursorEnd()
	}
}

func (r *replComponent) loadHistoryPitui() {
	data, err := os.ReadFile(r.historyFile)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			r.history = append(r.history, line)
		}
	}
}

func (r *replComponent) saveHistoryPitui() {
	entries := r.history
	if len(entries) > 1000 {
		entries = entries[len(entries)-1000:]
	}
	data := strings.Join(entries, "\n") + "\n"
	_ = os.WriteFile(r.historyFile, []byte(data), 0644)
}

// ── doc browser ─────────────────────────────────────────────────────────────

func (r *replComponent) showDocBrowser() {
	db := newDocBrowserOverlay(r.typeEnv)
	db.onExit = func() {
		if r.docHandle != nil {
			r.docHandle.Hide()
			r.docHandle = nil
			r.docBrowser = nil
		}
	}
	r.docBrowser = db
	r.docHandle = r.tui.ShowOverlay(db, &pitui.OverlayOptions{
		Width:     pitui.SizePct(100),
		MaxHeight: pitui.SizePct(100),
		Anchor:    pitui.AnchorTopLeft,
	})
}

// ── entry point ─────────────────────────────────────────────────────────────

func runREPLPitui(ctx context.Context, importConfigs []dang.ImportConfig, debug bool, daggerLog *pituiSyncWriter) error {
	term := pitui.NewProcessTerminal()
	tui := pitui.New(term)
	tui.SetShowHardwareCursor(true)

	repl := newReplComponent(ctx, tui, importConfigs, debug)
	daggerLog.SetTarget(repl.output, tui)
	repl.install()

	if err := tui.Start(); err != nil {
		return fmt.Errorf("TUI start: %w", err)
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
	tui.Stop()
	fmt.Println("Goodbye!")
	return nil
}
