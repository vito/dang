package analysis

import (
	"fmt"
	"strings"
)

// RenderOptions configures how diagnostics are rendered as text.
type RenderOptions struct {
	// MaxNames caps how many of a receiver's member names are listed; the
	// rest are counted. Zero means 60.
	MaxNames int

	// Width is the column the list of member names wraps at. Zero means
	// 100.
	Width int
}

func (o RenderOptions) maxNames() int {
	if o.MaxNames <= 0 {
		return 60
	}
	return o.MaxNames
}

func (o RenderOptions) width() int {
	if o.Width <= 0 {
		return 100
	}
	return o.Width
}

// Render renders diagnostics as compact text, one Format after another:
// what a CLI prints, or a tool returns to a model.
func Render(diags []Diagnostic, opts RenderOptions) string {
	var out strings.Builder
	for _, d := range diags {
		out.WriteString(d.Format(opts))
		out.WriteString("\n")
	}
	return out.String()
}

// Format renders the diagnostic as `path:line:col: severity: message`, then
// its Context, if any, on indented lines:
//
//	main.dang:2:1: error: list does not have method "findFirst"
//	  closest: find { item: a => Boolean! }: a  returns the first element ...
//	  [String!]! has 25 members: all any contains dropFirst ...
//
// Names are listed only up to opts.MaxNames, and only their names.
func (d Diagnostic) Format(opts RenderOptions) string {
	var out strings.Builder
	out.WriteString(d.String())
	c := d.Context
	if c == nil {
		return out.String()
	}
	if c.Member != nil {
		out.WriteString("\n  signature: " + c.Member.QualifiedSignature() + docSuffix(*c.Member))
	}
	for _, m := range c.Near {
		out.WriteString("\n  closest: " + m.Signature() + docSuffix(m))
	}
	if len(c.Search) > 0 {
		label := fmt.Sprintf("  docs matching %q: ", c.Query)
		for i, m := range c.Search {
			if i == 0 {
				out.WriteString("\n" + label)
			} else {
				out.WriteString("\n" + strings.Repeat(" ", len(label)))
			}
			out.WriteString(m.QualifiedSignature() + docSuffix(m))
		}
	}
	if len(c.Has) > 0 {
		out.WriteString("\n")
		out.WriteString(nameList(c.Receiver, c.Has, opts))
	}
	return out.String()
}

// nameList renders `  <recv> has N members: a b c` wrapped at opts' width,
// with up to opts' MaxNames names.
func nameList(recv string, members []Member, opts RenderOptions) string {
	noun := "members"
	if len(members) == 1 {
		noun = "member"
	}
	if recv == "" {
		recv = "it"
	}
	words := make([]string, 0, len(members)+1)
	for i, m := range members {
		if i == opts.maxNames() {
			words = append(words, fmt.Sprintf("(%d more)", len(members)-i))
			break
		}
		words = append(words, m.Name)
	}

	const indent = "    "
	var out strings.Builder
	line := fmt.Sprintf("  %s has %d %s:", recv, len(members), noun)
	for _, w := range words {
		if len(line)+1+len(w) > opts.width() {
			out.WriteString(line + "\n")
			line = indent + w
		} else {
			line += " " + w
		}
	}
	out.WriteString(line)
	return out.String()
}

// docSuffix is the first line of m's doc, shortened, after two spaces; empty
// when it has none. A deprecated member says so instead.
func docSuffix(m Member) string {
	if m.Deprecated != "" {
		return "  deprecated: " + oneLine(m.Deprecated)
	}
	if m.Doc == "" {
		return ""
	}
	return "  " + oneLine(m.Doc)
}

// oneLine is the first line of s, cut to about 80 characters.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	const limit = 80
	if r := []rune(s); len(r) > limit {
		s = strings.TrimSpace(string(r[:limit-1])) + "…"
	}
	return s
}
