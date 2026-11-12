//go:build !js

package tswasm

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"slices"
)

// Query is a compiled tree-sitter query, e.g. a highlights.scm.
//
// Its text predicates (#eq?, #match?, #any-of? and their not-/any-
// variants) are applied, like go-tree-sitter applies them; other predicates
// and directives (#set!, #offset!, ...) are left to the caller.
type Query struct {
	source       []byte
	captureNames []string
	predicates   [][]textPredicate // by pattern
}

// Capture is one capture of a query match.
type Capture struct {
	// Index is the capture's index in CaptureNames.
	Index uint32

	StartByte, EndByte uint32
}

// QueryError is a query's compilation error.
type QueryError struct {
	Offset uint32 // in the query source
	Kind   string
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("tswasm: query: %s error at offset %d", e.Kind, e.Offset)
}

// queryErrorKinds names TSQueryError's values.
var queryErrorKinds = []string{"no", "syntax", "node type", "field", "capture", "structure", "language"}

// NewQuery compiles a query.
func NewQuery(source string) (*Query, error) {
	q := &Query{source: []byte(source)}
	err := withInstance(context.Background(), func(inst *instance) error {
		ptr, err := inst.query(context.Background(), q)
		if err != nil {
			return err
		}
		return q.describe(context.Background(), inst, ptr)
	})
	if err != nil {
		return nil, err
	}
	return q, nil
}

// CaptureNames returns the query's capture names, by index.
func (q *Query) CaptureNames() []string {
	return q.captureNames
}

// Captures parses source and returns the query's captures in it, in order,
// as ts_query_cursor_next_capture gives them.
func (q *Query) Captures(ctx context.Context, source []byte) ([]Capture, error) {
	var captures []Capture
	err := withInstance(ctx, func(inst *instance) error {
		if err := inst.parse(ctx, source, false); err != nil {
			return err
		}
		var err error
		captures, err = inst.captures(ctx, q, source)
		return err
	})
	return captures, err
}

// query returns q compiled in the instance, compiling it on first use.
func (inst *instance) query(ctx context.Context, q *Query) (uint32, error) {
	if ptr, ok := inst.queries[q]; ok {
		return ptr, nil
	}
	src, err := inst.write(ctx, q.source)
	if err != nil {
		return 0, err
	}
	ptr, err := call(ctx, inst.queryNew, uint64(src), uint64(len(q.source)))
	if _, freeErr := inst.free.Call(ctx, uint64(src)); err == nil {
		err = freeErr
	}
	if err != nil {
		return 0, err
	}
	if ptr == 0 {
		res, err := inst.result(ctx)
		if err != nil {
			return 0, err
		}
		qe := &QueryError{Offset: res[0], Kind: "unknown"}
		if int(res[1]) < len(queryErrorKinds) {
			qe.Kind = queryErrorKinds[res[1]]
		}
		return 0, qe
	}
	inst.queries[q] = ptr
	return ptr, nil
}

// describe reads the query's capture names and predicates.
func (q *Query) describe(ctx context.Context, inst *instance, ptr uint32) error {
	str := func(f string, id uint32) (string, error) {
		fn := inst.queryCaptureName
		if f == "string" {
			fn = inst.queryStringValue
		}
		s, err := call(ctx, fn, uint64(ptr), uint64(id))
		if err != nil {
			return "", err
		}
		res, err := inst.result(ctx)
		if err != nil {
			return "", err
		}
		data, ok := inst.mod.Memory().Read(s, res[0])
		if !ok {
			return "", errFailed
		}
		return string(data), nil
	}

	captures, err := call(ctx, inst.queryCaptureCount, uint64(ptr))
	if err != nil {
		return err
	}
	q.captureNames = make([]string, captures)
	for i := range q.captureNames {
		if q.captureNames[i], err = str("capture", uint32(i)); err != nil {
			return err
		}
	}

	patterns, err := call(ctx, inst.queryPatternCount, uint64(ptr))
	if err != nil {
		return err
	}
	q.predicates = make([][]textPredicate, patterns)
	for p := range q.predicates {
		stepsPtr, err := call(ctx, inst.queryPredicates, uint64(ptr), uint64(p))
		if err != nil {
			return err
		}
		res, err := inst.result(ctx)
		if err != nil {
			return err
		}
		data, err := inst.words(stepsPtr, res[0]*2)
		if err != nil {
			return err
		}
		var pred []step
		for i := range int(res[0]) {
			s := step{kind: word(data, 2*i), id: word(data, 2*i+1)}
			switch s.kind {
			case stepDone:
				tp, err := parsePredicate(pred)
				if err != nil {
					return fmt.Errorf("tswasm: query pattern %d: %w", p, err)
				}
				if tp != nil {
					q.predicates[p] = append(q.predicates[p], *tp)
				}
				pred = nil
				continue
			case stepString:
				if s.str, err = str("string", s.id); err != nil {
					return err
				}
			}
			pred = append(pred, s)
		}
	}
	return nil
}

// TSQueryPredicateStepType's values.
const (
	stepDone = iota
	stepCapture
	stepString
)

type step struct {
	kind, id uint32
	str      string
}

// textPredicate is a text predicate, as go-tree-sitter has them.
type textPredicate struct {
	capture  uint32
	positive bool
	matchAll bool

	otherCapture *uint32 // #eq? against another capture
	str          *string // #eq? against a string
	re           *regexp.Regexp
	anyOf        []string
}

// parsePredicate reads one predicate's steps, or returns nil for a
// predicate or directive Query doesn't apply.
func parsePredicate(steps []step) (*textPredicate, error) {
	if len(steps) == 0 || steps[0].kind != stepString {
		return nil, fmt.Errorf("predicate must begin with a literal value")
	}
	op := steps[0].str
	args := steps[1:]
	switch op {
	case "eq?", "not-eq?", "any-eq?", "any-not-eq?":
		if len(args) != 2 || args[0].kind != stepCapture {
			return nil, fmt.Errorf("#%s takes a capture and a capture or string", op)
		}
		tp := &textPredicate{
			capture:  args[0].id,
			positive: op == "eq?" || op == "any-eq?",
			matchAll: op == "eq?" || op == "not-eq?",
		}
		if args[1].kind == stepCapture {
			tp.otherCapture = &args[1].id
		} else {
			tp.str = &args[1].str
		}
		return tp, nil
	case "match?", "not-match?", "any-match?", "any-not-match?":
		if len(args) != 2 || args[0].kind != stepCapture || args[1].kind != stepString {
			return nil, fmt.Errorf("#%s takes a capture and a string", op)
		}
		re, err := regexp.Compile(args[1].str)
		if err != nil {
			return nil, fmt.Errorf("#%s: %w", op, err)
		}
		return &textPredicate{
			capture:  args[0].id,
			positive: op == "match?" || op == "any-match?",
			matchAll: op == "match?" || op == "not-match?",
			re:       re,
		}, nil
	case "any-of?", "not-any-of?":
		if len(args) < 1 || args[0].kind != stepCapture {
			return nil, fmt.Errorf("#%s takes a capture and strings", op)
		}
		tp := &textPredicate{capture: args[0].id, positive: op == "any-of?", matchAll: true}
		for _, a := range args[1:] {
			if a.kind != stepString {
				return nil, fmt.Errorf("#%s takes a capture and strings", op)
			}
			tp.anyOf = append(tp.anyOf, a.str)
		}
		return tp, nil
	}
	return nil, nil
}

// matchCapture is a capture of a match, as dang_captures_next gives it.
type matchCapture struct {
	index, start, end uint32
}

// satisfied reports whether a match's captures satisfy the predicate, as
// go-tree-sitter's QueryMatch.SatisfiesTextPredicate decides it.
func (tp *textPredicate) satisfied(captures []matchCapture, source []byte) bool {
	text := func(c matchCapture) []byte {
		if c.end > uint32(len(source)) || c.start > c.end {
			return nil
		}
		return source[c.start:c.end]
	}
	nodes := func(index uint32) []matchCapture {
		var out []matchCapture
		for _, c := range captures {
			if c.index == index {
				out = append(out, c)
			}
		}
		return out
	}
	// decide applies one node's outcome: it settles the predicate when it
	// fails one that must hold of all nodes, or satisfies one that needs
	// any node.
	decide := func(positive bool) (settled, result bool) {
		if positive != tp.positive && tp.matchAll {
			return true, false
		}
		if positive == tp.positive && !tp.matchAll {
			return true, true
		}
		return false, false
	}
	switch {
	case tp.otherCapture != nil:
		nodes1, nodes2 := nodes(tp.capture), nodes(*tp.otherCapture)
		for len(nodes1) > 0 && len(nodes2) > 0 {
			if settled, result := decide(bytes.Equal(text(nodes1[0]), text(nodes2[0]))); settled {
				return result
			}
			nodes1, nodes2 = nodes1[1:], nodes2[1:]
		}
		return len(nodes1) == 0 && len(nodes2) == 0
	case tp.str != nil:
		for _, n := range nodes(tp.capture) {
			if settled, result := decide(string(text(n)) == *tp.str); settled {
				return result
			}
		}
		return true
	case tp.re != nil:
		for _, n := range nodes(tp.capture) {
			if settled, result := decide(tp.re.Match(text(n))); settled {
				return result
			}
		}
		return true
	default: // any-of
		for _, n := range nodes(tp.capture) {
			if slices.Contains(tp.anyOf, string(text(n))) != tp.positive {
				return false
			}
		}
		return true
	}
}

// captures runs q over the kept tree of source.
func (inst *instance) captures(ctx context.Context, q *Query, source []byte) ([]Capture, error) {
	ptr, err := inst.query(ctx, q)
	if err != nil {
		return nil, err
	}
	ok, err := call(ctx, inst.capturesExec, uint64(ptr))
	if err != nil {
		return nil, err
	}
	if ok == 0 {
		return nil, errFailed
	}
	var out []Capture
	var match []matchCapture
	for {
		ev, err := call(ctx, inst.capturesNext)
		if err != nil {
			return nil, err
		}
		if ev == 0 {
			break
		}
		header, err := inst.words(ev, 4)
		if err != nil {
			return nil, err
		}
		matchID, pattern, idx, count := word(header, 0), word(header, 1), word(header, 2), word(header, 3)
		data, err := inst.words(ev+16, count*3)
		if err != nil {
			return nil, err
		}
		match = match[:0]
		for i := range int(count) {
			match = append(match, matchCapture{word(data, 3*i), word(data, 3*i+1), word(data, 3*i+2)})
		}
		if idx >= count || int(pattern) >= len(q.predicates) {
			return nil, errFailed
		}
		satisfied := true
		for i := range q.predicates[pattern] {
			if !q.predicates[pattern][i].satisfied(match, source) {
				satisfied = false
				break
			}
		}
		if !satisfied {
			if _, err := call(ctx, inst.capturesRemove, uint64(matchID)); err != nil {
				return nil, err
			}
			continue
		}
		c := match[idx]
		out = append(out, Capture{Index: c.index, StartByte: c.start, EndByte: c.end})
	}
	// An interrupted query ends early, like a finished one.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
