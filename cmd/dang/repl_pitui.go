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

	"github.com/kr/pretty"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/ioctx"
	"github.com/vito/dang/pkg/pitui"
)

// ── sync writer (streams dagger output into TUI) ────────────────────────────

type pituiSyncWriter struct {
	mu   sync.Mutex
	repl *replComponent
}

func (w *pituiSyncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	r := w.repl
	w.mu.Unlock()
	if r != nil {
		r.mu.Lock()
		r.lastEntry().writeLog(string(p))
		r.mu.Unlock()
		r.output.markDirty()
		r.tui.RequestRender(false)
	}
	return len(p), nil
}

func (w *pituiSyncWriter) SetRepl(r *replComponent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.repl = r
}

// ── output log component ────────────────────────────────────────────────────

// outputLog renders the structured entries list (past prompts, logs, results).
// It caches rendered lines and only re-renders when marked dirty.
type outputLog struct {
	repl *replComponent

	dirty       bool
	cachedLines []string
}

func (o *outputLog) markDirty() { o.dirty = true }

func (o *outputLog) Invalidate() { o.dirty = true }

func (o *outputLog) Render(ctx pitui.RenderContext) pitui.RenderResult {
	if !o.dirty && o.cachedLines != nil {
		return pitui.RenderResult{Lines: o.cachedLines, Dirty: false}
	}

	type entrySnapshot struct {
		input  string
		logs   string
		result string
	}
	o.repl.mu.Lock()
	snaps := make([]entrySnapshot, len(o.repl.entries))
	for i, e := range o.repl.entries {
		snaps[i] = entrySnapshot{
			input:  e.input,
			logs:   e.logs.String(),
			result: e.result.String(),
		}
	}
	o.repl.mu.Unlock()

	var lines []string
	for _, snap := range snaps {
		if snap.input != "" {
			lines = append(lines, snap.input)
		}
		if snap.logs != "" {
			logLines := strings.Split(snap.logs, "\n")
			if len(logLines) > 0 && logLines[len(logLines)-1] == "" {
				logLines = logLines[:len(logLines)-1]
			}
			lines = append(lines, logLines...)
		}
		if snap.result != "" {
			resLines := strings.Split(snap.result, "\n")
			if len(resLines) > 0 && resLines[len(resLines)-1] == "" {
				resLines = resLines[:len(resLines)-1]
			}
			lines = append(lines, resLines...)
		}
	}
	for i, line := range lines {
		lines[i] = pitui.ExpandTabs(line, 8)
	}
	for i, line := range lines {
		if pitui.VisibleWidth(line) > ctx.Width {
			lines[i] = pitui.Truncate(line, ctx.Width, "")
		}
	}

	o.cachedLines = lines
	o.dirty = false
	return pitui.RenderResult{Lines: lines, Dirty: true}
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

	entries []*replEntry
	quit    chan struct{}

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
	r.output = &outputLog{repl: r, dirty: true}

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
		return dimStyle.Render(s)
	}
	ti.OnSubmit = r.onSubmit
	ti.OnKey = r.onKey
	ti.OnChange = r.updateCompletionMenuPitui

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
	welcome.writeLogLine(dimStyle.Render("Type :help for commands, Tab for completion, Ctrl+C to exit"))
	welcome.writeLogLine("")
	r.entries = append(r.entries, welcome)

	r.completions = r.buildCompletionsPitui()
	r.loadHistoryPitui()

	return r
}

func (r *replComponent) lastEntry() *replEntry {
	if len(r.entries) == 0 {
		r.entries = append(r.entries, newReplEntry(""))
	}
	return r.entries[len(r.entries)-1]
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

	r.mu.Lock()
	r.entries = append(r.entries, newReplEntry(
		promptStyle.Render("dang> ")+line,
	))
	r.mu.Unlock()
	r.output.markDirty()

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
		r.mu.Lock()
		r.entries = nil
		r.mu.Unlock()
		r.output.markDirty()
		return true
	}

	return false
}

// ── eval ────────────────────────────────────────────────────────────────────

func (r *replComponent) startEvalPitui(expr string) {
	r.mu.Lock()
	e := r.lastEntry()

	result, err := dang.Parse("repl", []byte(expr))
	if err != nil {
		e.writeLogLine(errorStyle.Render(fmt.Sprintf("parse error: %v", err)))
		r.mu.Unlock()
		r.output.markDirty()
		return
	}

	forms := result.(*dang.ModuleBlock).Forms

	if r.debug {
		for _, node := range forms {
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("%# v", pretty.Formatter(node))))
		}
	}

	fresh := hm.NewSimpleFresher()
	_, err = dang.InferFormsWithPhases(r.ctx, forms, r.typeEnv, fresh)
	if err != nil {
		e.writeLogLine(errorStyle.Render(fmt.Sprintf("type error: %v", err)))
		r.mu.Unlock()
		r.output.markDirty()
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

	r.mu.Lock()
	r.evaluating = false
	r.cancelEval = nil
	e := r.lastEntry()
	if cancelled {
		e.writeLogLine(errorStyle.Render("cancelled"))
	} else {
		for _, line := range logs {
			e.writeLogLine(line)
		}
		for _, line := range results {
			e.writeResult(line)
		}
		r.refreshCompletionsPitui()
	}
	r.mu.Unlock()

	// Swap spinner back to text input via the slot.
	r.inputSlot.Set(r.textInput)
	r.tui.SetFocus(r.textInput)
	r.output.markDirty()
	r.tui.RequestRender(false)
}

// ── completion menu ─────────────────────────────────────────────────────────

func (r *replComponent) hideCompletionMenu() {
	r.menuVisible = false
	r.menuItems = nil
	r.menuLabels = nil
	r.menuCompletions = nil
	if r.menuHandle != nil {
		r.menuHandle.Hide()
		r.menuHandle = nil
		r.menuOverlay = nil
	}
	r.hideDetailBubble()
}

func (r *replComponent) menuDisplayItems() []string {
	if len(r.menuLabels) > 0 {
		return r.menuLabels
	}
	return r.menuItems
}

func (r *replComponent) menuBoxWidth() int {
	items := r.menuDisplayItems()
	maxW := 0
	for _, item := range items {
		if w := lipgloss.Width(item); w > maxW {
			maxW = w
		}
	}
	if maxW > 60 {
		maxW = 60
	}
	return maxW + 4 // 2 for padding (" item ") + 2 for border
}

func (r *replComponent) showCompletionMenu() {
	xOff := r.completionXOffsetPitui()
	displayItems := r.menuDisplayItems()
	menuH := min(len(displayItems), r.menuMaxVisible) + 2 // items + border
	linesAbove := len(r.output.cachedLines)               // content lines above the input

	var opts *pitui.OverlayOptions
	if linesAbove >= menuH {
		// Enough room above: show the menu above the input line.
		opts = &pitui.OverlayOptions{
			Width:           pitui.SizeAbs(r.menuBoxWidth()),
			MaxHeight:       pitui.SizeAbs(r.menuMaxVisible + 2),
			Anchor:          pitui.AnchorBottomLeft,
			ContentRelative: true,
			OffsetX:         xOff,
			OffsetY:         -1, // above the input line
			NoFocus:         true,
		}
	} else {
		// Not enough room above: show below the input line.
		// With little content there's no scrolling, so viewport rows
		// match content rows and we can position without ContentRelative.
		opts = &pitui.OverlayOptions{
			Width:     pitui.SizeAbs(r.menuBoxWidth()),
			MaxHeight: pitui.SizeAbs(r.menuMaxVisible + 2),
			Anchor:    pitui.AnchorTopLeft,
			Row:       pitui.SizeAbs(linesAbove + 1), // right below the input
			OffsetX:   xOff,
			NoFocus:   true,
		}
	}
	if r.menuHandle != nil {
		// Reuse existing overlay — just update position and data.
		r.menuOverlay.items = displayItems
		r.menuOverlay.index = r.menuIndex
		r.menuHandle.SetOptions(opts)
	} else {
		r.menuOverlay = &completionOverlay{
			items:      displayItems,
			index:      r.menuIndex,
			maxVisible: r.menuMaxVisible,
		}
		r.menuHandle = r.tui.ShowOverlay(r.menuOverlay, opts)
	}
	r.syncDetailBubble()
}

func (r *replComponent) syncMenu() {
	if r.menuOverlay != nil {
		r.menuOverlay.items = r.menuDisplayItems()
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
			MaxHeight: pitui.SizePct(80),
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

// showDetailForCompletion shows the detail bubble for a single completion
// item, without requiring the dropdown menu to be visible.
func (r *replComponent) showDetailForCompletion(c dang.Completion) {
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
		isArgCompletion := len(completions) > 0 && completions[0].IsArg
		prefix, partial := splitForSuggestion(val)
		var matches []string
		var labels []string
		var matchCompletions []dang.Completion
		partialLower := strings.ToLower(partial)
		for _, c := range completions {
			cLower := strings.ToLower(c.Label)
			if cLower == partialLower {
				continue
			}
			if strings.HasPrefix(cLower, partialLower) {
				if c.IsArg {
					matches = append(matches, prefix+c.Label+": ")
					labels = append(labels, c.Label+": "+c.Detail)
				} else {
					matches = append(matches, prefix+c.Label)
					labels = append(labels, c.Label)
				}
				matchCompletions = append(matchCompletions, c)
			}
		}
		if !isArgCompletion {
			matches, matchCompletions = sortByCaseWithCompletions(matches, matchCompletions, prefix, partial)
			labels, _ = sortByCaseWithCompletions(labels, nil, "", partial)
		}
		r.menuLabels = labels
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
	r.menuLabels = nil
	r.setMenuPitui(matches, nil)
	if len(matches) > 0 {
		r.textInput.Suggestion = matches[0]
	} else {
		r.textInput.Suggestion = ""
	}
}

func (r *replComponent) setMenuPitui(matches []string, completions []dang.Completion) {
	if len(matches) == 0 {
		r.hideCompletionMenu()
		return
	}
	if len(matches) == 1 {
		// Single match: no dropdown, but show the detail bubble.
		r.hideCompletionMenu()
		if len(completions) == 1 {
			r.showDetailForCompletion(completions[0])
		}
		return
	}
	r.menuItems = matches
	// menuLabels is set by the caller before calling setMenuPitui
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
	r.mu.Lock()
	e := r.lastEntry()

	parts := strings.Fields(cmdLine)
	if len(parts) == 0 {
		e.writeLogLine(errorStyle.Render("empty command"))
		r.mu.Unlock()
		r.output.markDirty()
		return
	}

	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "help":
		e.writeLogLine("Available commands:")
		e.writeLogLine(dimStyle.Render("  :help      - Show this help"))
		e.writeLogLine(dimStyle.Render("  :exit      - Exit the REPL"))
		e.writeLogLine(dimStyle.Render("  :doc       - Interactive API browser"))
		e.writeLogLine(dimStyle.Render("  :env       - Show environment bindings"))
		e.writeLogLine(dimStyle.Render("  :type      - Show type of an expression"))
		e.writeLogLine(dimStyle.Render("  :find      - Find functions/types by pattern"))
		e.writeLogLine(dimStyle.Render("  :reset     - Reset the environment"))
		e.writeLogLine(dimStyle.Render("  :clear     - Clear the screen"))
		e.writeLogLine(dimStyle.Render("  :debug     - Toggle debug mode"))
		e.writeLogLine(dimStyle.Render("  :version   - Show version info"))
		e.writeLogLine(dimStyle.Render("  :quit      - Exit the REPL"))
		e.writeLogLine("")
		e.writeLogLine(dimStyle.Render("Type Dang expressions to evaluate them."))
		e.writeLogLine(dimStyle.Render("Tab for completion, Up/Down for history, Ctrl+L to clear."))

	case "exit", "quit":
		r.mu.Unlock()
		close(r.quit)
		return

	case "clear":
		r.entries = nil

	case "reset":
		r.typeEnv, r.evalEnv = buildEnvFromImports(r.importConfigs)
		r.refreshCompletionsPitui()
		e.writeLogLine(resultStyle.Render("Environment reset."))

	case "debug":
		r.debug = !r.debug
		status := "disabled"
		if r.debug {
			status = "enabled"
		}
		e.writeLogLine(resultStyle.Render(fmt.Sprintf("Debug mode %s.", status)))

	case "env":
		r.envCommandPitui(e, args)

	case "version":
		e.writeLogLine(resultStyle.Render("Dang REPL v0.1.0"))
		if len(r.importConfigs) > 0 {
			var names []string
			for _, ic := range r.importConfigs {
				names = append(names, ic.Name)
			}
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("Imports: %s", strings.Join(names, ", "))))
		} else {
			e.writeLogLine(dimStyle.Render("No imports configured (create a dang.toml)"))
		}

	case "type":
		r.typeCommandPitui(e, args)

	case "find", "search":
		r.findCommandPitui(e, args)

	case "history":
		e.writeLogLine("Recent history:")
		start := 0
		if len(r.history) > 20 {
			start = len(r.history) - 20
		}
		for i := start; i < len(r.history); i++ {
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %d: %s", i+1, r.history[i])))
		}

	case "doc":
		r.mu.Unlock()
		r.output.markDirty()
		r.showDocBrowser()
		return

	default:
		e.writeLogLine(errorStyle.Render(fmt.Sprintf("unknown command: %s (type :help for available commands)", cmd)))
	}
	r.mu.Unlock()
	r.output.markDirty()
}

func (r *replComponent) envCommandPitui(e *replEntry, args []string) {
	filter := ""
	showAll := false
	if len(args) > 0 {
		if args[0] == "all" {
			showAll = true
		} else {
			filter = args[0]
		}
	}
	e.writeLogLine("Current environment bindings:")
	e.writeLogLine("")
	count := 0
	for name, scheme := range r.typeEnv.Bindings(dang.PublicVisibility) {
		if filter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(filter)) {
			continue
		}
		if !showAll && count >= 20 {
			e.writeLogLine(dimStyle.Render("  ... use ':env all' to see all"))
			break
		}
		t, _ := scheme.Type()
		if t != nil {
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s : %s", name, t)))
		} else {
			e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s", name)))
		}
		count++
	}
	e.writeLogLine("")
	e.writeLogLine(dimStyle.Render("Use ':doc' for interactive API browsing"))
}

func (r *replComponent) typeCommandPitui(e *replEntry, args []string) {
	if len(args) == 0 {
		e.writeLogLine(dimStyle.Render("Usage: :type <expression>"))
		return
	}
	expr := strings.Join(args, " ")
	result, err := dang.Parse("type-check", []byte(expr))
	if err != nil {
		e.writeLogLine(errorStyle.Render(fmt.Sprintf("parse error: %v", err)))
		return
	}
	node := result.(*dang.Block)
	inferredType, err := dang.Infer(r.ctx, r.typeEnv, node, false)
	if err != nil {
		e.writeLogLine(errorStyle.Render(fmt.Sprintf("type error: %v", err)))
		return
	}
	e.writeLogLine(fmt.Sprintf("Expression: %s", expr))
	e.writeLogLine(resultStyle.Render(fmt.Sprintf("Type: %s", inferredType)))
	trimmed := strings.TrimSpace(expr)
	if !strings.Contains(trimmed, " ") {
		if scheme, found := r.typeEnv.SchemeOf(trimmed); found {
			if t, _ := scheme.Type(); t != nil {
				e.writeLogLine(dimStyle.Render(fmt.Sprintf("Scheme: %s", scheme)))
			}
		}
	}
}

func (r *replComponent) findCommandPitui(e *replEntry, args []string) {
	if len(args) == 0 {
		e.writeLogLine(dimStyle.Render("Usage: :find <pattern>"))
		return
	}
	pattern := strings.ToLower(args[0])
	e.writeLogLine(fmt.Sprintf("Searching for '%s'...", pattern))
	found := false
	for name, scheme := range r.typeEnv.Bindings(dang.PublicVisibility) {
		if strings.Contains(strings.ToLower(name), pattern) {
			t, _ := scheme.Type()
			if t != nil {
				e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s : %s", name, t)))
			} else {
				e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s", name)))
			}
			found = true
		}
	}
	for name, env := range r.typeEnv.NamedTypes() {
		if strings.Contains(strings.ToLower(name), pattern) {
			doc := env.GetModuleDocString()
			if doc != "" {
				if len(doc) > 60 {
					doc = doc[:57] + "..."
				}
				e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s - %s", name, doc)))
			} else {
				e.writeLogLine(dimStyle.Render(fmt.Sprintf("  %s", name)))
			}
			found = true
		}
	}
	if !found {
		e.writeLogLine(dimStyle.Render(fmt.Sprintf("No matches found for '%s'", pattern)))
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
	db := newDocBrowserOverlay(r.typeEnv, r.tui)
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

func runREPLPitui(ctx context.Context, importConfigs []dang.ImportConfig, moduleDir string, debug bool) error {
	term := pitui.NewProcessTerminal()
	tui := pitui.New(term)
	tui.SetShowHardwareCursor(true)

	if err := tui.Start(); err != nil {
		return fmt.Errorf("TUI start: %w", err)
	}

	// If there's a Dagger module, load it with a spinner visible.
	daggerLog := &pituiSyncWriter{}
	var daggerConn *dagger.Client
	if moduleDir != "" {
		loadSp := pitui.NewSpinner(tui)
		loadSp.Style = func(s string) string {
			return lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Render(s)
		}
		loadSp.Label = dimStyle.Render(fmt.Sprintf("Loading Dagger module from %s...", moduleDir))
		loadLine := &evalSpinnerLine{spinner: loadSp}
		tui.AddChild(loadLine)
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
		tui.RemoveChild(loadLine)
	}

	if len(importConfigs) > 0 {
		ctx = dang.ContextWithImportConfigs(ctx, importConfigs...)
	}

	repl := newReplComponent(ctx, tui, importConfigs, debug)
	daggerLog.SetRepl(repl)
	repl.install()

	if daggerConn != nil {
		defer daggerConn.Close()
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
