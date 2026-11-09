package analysis

import (
	"fmt"

	"github.com/vito/dang/v2/pkg/dang"
)

// Pos is a position in a source file. Both fields are 1-based, like Dang's own
// error locations and the `path:line:col` a reader types: the first character
// of a file is {Line: 1, Column: 1}. Columns count characters as Dang's parser
// does.
type Pos struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

func (p Pos) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// Range is a span of a source file. End is exclusive: it is the position just
// past the last character covered.
type Range struct {
	Start Pos `json:"start"`
	End   Pos `json:"end"`
}

func (r Range) String() string {
	return fmt.Sprintf("%s-%s", r.Start, r.End)
}

// locRange returns the range a Dang source location covers: up to its End when
// it has one, else Length characters on its line, and at least one character.
func locRange(loc *dang.SourceLocation) Range {
	start := Pos{Line: loc.Line, Column: loc.Column}
	end := Pos{Line: loc.Line, Column: loc.Column + loc.Length}
	if loc.Length == 0 {
		end.Column = loc.Column + 1
	}
	if loc.End != nil {
		end = Pos{Line: loc.End.Line, Column: loc.End.Column}
	}
	return Range{Start: start, End: end}
}

// nameRange returns the range covering name, spelled at loc. An escaped name
// (`$rescue`, see dang's RawWordToken) spans its `$` too, which the range leaves
// out: it covers the name itself, so that renaming it keeps the escape.
func nameRange(loc *dang.SourceLocation, name string) Range {
	col := loc.Column
	if loc.Length == len(name)+1 {
		col++
	}
	return Range{
		Start: Pos{Line: loc.Line, Column: col},
		End:   Pos{Line: loc.Line, Column: col + len(name)},
	}
}
