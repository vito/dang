package main

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/pitui"
)

// ── completion menu ─────────────────────────────────────────────────────────

func (r *replComponent) hideCompletionMenu() {
	r.menuVisible = false
	r.menuItems = nil
	r.menuLabels = nil
	r.menuCompletions = nil
	r.menuIndex = 0
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

func (r *replComponent) menuBoxHeight() int {
	n := len(r.menuDisplayItems())
	h := min(n, r.menuMaxVisible) + 2 // visible items + top/bottom border
	if n > r.menuMaxVisible {
		h++ // info line ("1/42")
	}
	return h
}

func (r *replComponent) showCompletionMenu(ctx pitui.EventContext) {
	displayItems := r.menuDisplayItems()
	menuH := r.menuBoxHeight()

	opts := &pitui.OverlayOptions{
		Width:          pitui.SizeAbs(r.menuBoxWidth()),
		MaxHeight:      pitui.SizeAbs(menuH),
		CursorRelative: true,
		PreferAbove:    true,
		OffsetX:        -r.completionTokenLen(),
		CursorGroup:    r.completionGroup,
		BubbleTarget:   r.textInput,
	}
	if r.menuHandle != nil {
		// Reuse existing overlay — just update position and data.
		r.menuOverlay.items = displayItems
		r.menuOverlay.index = r.menuIndex
		r.menuOverlay.Update()
		r.menuHandle.SetOptions(opts)
	} else {
		r.menuOverlay = &completionOverlay{
			items:      displayItems,
			index:      r.menuIndex,
			maxVisible: r.menuMaxVisible,
			onAccept: func(index int) {
				if index < len(r.menuItems) {
					r.textInput.SetValue(r.menuItems[index])
					r.textInput.CursorEnd()
				}
				r.hideCompletionMenu()
				ctx.SetFocus(r.textInput)
				r.updateCompletionMenu(ctx)
			},
			onDismiss: func() {
				r.hideCompletionMenu()
				ctx.SetFocus(r.textInput)
			},
			onNavigate: func() {
				r.menuIndex = r.menuOverlay.index
				r.syncDetailBubble(ctx)
			},
		}
		r.menuHandle = ctx.ShowOverlay(r.menuOverlay, opts)
		ctx.SetFocus(r.menuOverlay)
	}
	r.syncDetailBubble(ctx)
}

func (r *replComponent) syncMenu(ctx pitui.EventContext) {
	if r.menuOverlay != nil {
		r.menuOverlay.items = r.menuDisplayItems()
		r.menuOverlay.index = r.menuIndex
		r.menuOverlay.Update()
	}
	r.syncDetailBubble(ctx)
	ctx.RequestRender(false)
}

func (r *replComponent) detailBubbleOptions() *pitui.OverlayOptions {
	detailX := -r.completionTokenLen()
	if r.menuHandle != nil {
		// Menu visible — place detail to its right with a 1 col gap.
		detailX += r.menuBoxWidth() + 1
	}

	return &pitui.OverlayOptions{
		Width:          pitui.SizePct(35),
		MaxHeight:      pitui.SizePct(80),
		CursorRelative: true,
		PreferAbove:    true,
		OffsetX:        detailX,
		CursorGroup:    r.completionGroup,
	}
}

func (r *replComponent) showDetailBubble(ctx pitui.EventContext) {
	opts := r.detailBubbleOptions()
	if r.detailBubble == nil {
		r.detailBubble = &detailBubble{}
		r.detailHandle = ctx.ShowOverlay(r.detailBubble, opts)
	} else {
		r.detailHandle.SetOptions(opts)
	}
}

func (r *replComponent) hideDetailBubble() {
	if r.detailHandle != nil {
		r.detailHandle.Hide()
		r.detailHandle = nil
		r.detailBubble = nil
	}
}

func (r *replComponent) syncDetailBubble(ctx pitui.EventContext) {
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

	r.showDetailBubble(ctx)
	r.detailBubble.item = item
	r.detailBubble.Update()
}

// showDetailForCompletion shows the detail bubble for a single completion
// item, without requiring the dropdown menu to be visible.
func (r *replComponent) showDetailForCompletion(ctx pitui.EventContext, c dang.Completion) {
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

	r.showDetailBubble(ctx)
	r.detailBubble.item = item
	r.detailBubble.Update()
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

// completionTokenLen returns the length of the completion token ending at
// the cursor. This includes "receiver.ident" for method completions.
func (r *replComponent) completionTokenLen() int {
	val := r.textInput.Value()
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
	return len(val) - (i + 1)
}


func (r *replComponent) updateCompletionMenu(ctx pitui.EventContext) {
	val := r.textInput.Value()

	if val == "" || strings.HasPrefix(val, ":") {
		r.hideCompletionMenu()
		r.textInput.Suggestion = ""
		r.reconcileFocus(ctx)
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
		r.setMenu(ctx, matches, matchCompletions)
		if len(matches) > 0 {
			r.textInput.Suggestion = matches[0]
		} else {
			r.textInput.Suggestion = ""
		}
		r.reconcileFocus(ctx)
		return
	}

	// Fallback: static completions.
	word := lastIdent(val)
	if word == "" {
		r.hideCompletionMenu()
		r.textInput.Suggestion = ""
		r.reconcileFocus(ctx)
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
	r.setMenu(ctx, matches, nil)
	if len(matches) > 0 {
		r.textInput.Suggestion = matches[0]
	} else {
		r.textInput.Suggestion = ""
	}
	r.reconcileFocus(ctx)
}

// reconcileFocus ensures the focused component matches the current state.
// If the completion menu is visible its overlay gets focus; otherwise focus
// returns to the text input. Called at the end of updateCompletionMenu so
// that hiding/showing the menu never leaves focus on a dead component.
func (r *replComponent) reconcileFocus(ctx pitui.EventContext) {
	if r.evaluating {
		return
	}
	if r.menuVisible && r.menuOverlay != nil {
		ctx.SetFocus(r.menuOverlay)
	} else {
		ctx.SetFocus(r.textInput)
	}
}

func (r *replComponent) setMenu(ctx pitui.EventContext, matches []string, completions []dang.Completion) {
	if len(matches) == 0 {
		r.hideCompletionMenu()
		return
	}
	if len(matches) == 1 {
		// Single match: no dropdown, but show the detail bubble.
		r.hideCompletionMenu()
		if len(completions) == 1 {
			r.showDetailForCompletion(ctx, completions[0])
		}
		return
	}
	r.menuItems = matches
	// menuLabels is set by the caller before calling setMenu
	r.menuCompletions = completions
	r.menuVisible = true
	if r.menuIndex >= len(matches) {
		r.menuIndex = 0
	}
	r.showCompletionMenu(ctx)
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

func (r *replComponent) buildCompletions() []string {
	return buildCompletionList(r.typeEnv)
}

func (r *replComponent) refreshCompletions() {
	r.completions = r.buildCompletions()
}
