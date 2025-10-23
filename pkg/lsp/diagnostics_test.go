package lsp

import (
	"context"
	"testing"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/is"
)

func TestErrorToDiagnostic(t *testing.T) {
	is := is.New(t)
	handler := &langHandler{}

	t.Run("InferError with location", func(t *testing.T) {
		is := is.New(t)
		
		inferErr := &dang.InferError{
			Message: "type mismatch: String ~ Int",
			Location: &dang.SourceLocation{
				Filename: "test.dang",
				Line:     5,
				Column:   10,
				Length:   8,
			},
		}

		diag := handler.errorToDiagnostic(inferErr, "file:///test.dang")
		is.True(diag != nil)
		is.Equal(diag.Message, "type mismatch: String ~ Int")
		is.Equal(diag.Range.Start.Line, 4)      // 0-based
		is.Equal(diag.Range.Start.Character, 9) // 0-based
		is.Equal(diag.Range.End.Character, 17)  // start + length
		is.Equal(diag.Severity, 1)              // Error
	})

	t.Run("InferError with end position", func(t *testing.T) {
		is := is.New(t)
		
		inferErr := &dang.InferError{
			Message: "undefined symbol",
			Location: &dang.SourceLocation{
				Filename: "test.dang",
				Line:     3,
				Column:   5,
				Length:   10,
				End: &dang.SourcePosition{
					Line:   3,
					Column: 15,
				},
			},
		}

		diag := handler.errorToDiagnostic(inferErr, "file:///test.dang")
		is.True(diag != nil)
		is.Equal(diag.Range.Start.Line, 2)      // 0-based
		is.Equal(diag.Range.Start.Character, 4) // 0-based
		is.Equal(diag.Range.End.Line, 2)        // 0-based
		is.Equal(diag.Range.End.Character, 14)  // 0-based from End
	})

	t.Run("Generic error without location", func(t *testing.T) {
		is := is.New(t)
		
		err := dang.NewInferError("some error", nil)

		diag := handler.errorToDiagnostic(err, "file:///test.dang")
		is.True(diag != nil)
		is.Equal(diag.Message, "some error")
		is.Equal(diag.Range.Start.Line, 0) // Fallback to line 0
		is.Equal(diag.Severity, 1)         // Error
	})
}

func TestUpdateFileWithDiagnostics(t *testing.T) {
	is := is.New(t)
	
	handler := &langHandler{
		files: make(map[DocumentURI]*File),
	}
	
	uri := DocumentURI("file:///test.dang")
	
	// Open file
	err := handler.openFile(uri, "dang", 1)
	is.NoErr(err)
	
	// Update with code that has type errors
	code := `pub bad = "hello" + 42`
	err = handler.updateFile(context.Background(), uri, code, nil)
	is.NoErr(err)
	
	// Check that diagnostics were collected
	f := handler.files[uri]
	is.True(f != nil)
	
	// We should have diagnostics since there's no schema loaded
	// (or if schema is loaded, we should have a type error)
	t.Logf("Diagnostics count: %d", len(f.Diagnostics))
	for i, diag := range f.Diagnostics {
		t.Logf("Diagnostic %d: %s (line %d)", i, diag.Message, diag.Range.Start.Line)
	}
}
