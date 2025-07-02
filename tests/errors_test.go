package tests

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/vito/dash/pkg/dash"
	"github.com/vito/dash/pkg/ioctx"
	"gotest.tools/v3/golden"
)

var updateGolden = flag.Bool("update-golden", false, "update golden files")

// TestErrorMessages tests that error messages match golden files
func TestErrorMessages(t *testing.T) {
	errorsDir := filepath.Join("errors")
	
	// Find all .dash files in the errors directory
	dashFiles, err := filepath.Glob(filepath.Join(errorsDir, "*.dash"))
	if err != nil {
		t.Fatalf("Failed to find .dash files: %v", err)
	}

	if len(dashFiles) == 0 {
		t.Fatal("No .dash test files found in tests/errors/")
	}

	for _, dashFile := range dashFiles {
		dashFile := dashFile // capture loop variable
		
		// Extract test name from filename
		testName := strings.TrimSuffix(filepath.Base(dashFile), ".dash")
		
		t.Run(testName, func(t *testing.T) {
			output := runDashFile(t, dashFile)
			
			// Compare with golden file
			golden.Assert(t, output, testName+".golden")
		})
	}
}

// runDashFile runs a Dash file and captures combined stdout/stderr
func runDashFile(t *testing.T, dashFile string) string {
	ctx := context.Background()
	
	// Connect to Dagger (required for Dash execution)
	dag, err := dagger.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect to Dagger: %v", err)
	}
	defer dag.Close()
	
	// Introspect the GraphQL schema (required for Dash execution)
	schema, err := introspectSchema(ctx, dag)
	if err != nil {
		t.Fatalf("Failed to introspect schema: %v", err)
	}
	
	// Create buffers to capture output
	var stdout, stderr bytes.Buffer
	
	// Set up context with captured stdout/stderr
	ctx = ioctx.StdoutToContext(ctx, &stdout)
	ctx = ioctx.StderrToContext(ctx, &stderr)
	
	// Run the Dash file
	err = dash.RunFile(ctx, dag.GraphQLClient(), schema, dashFile, false)
	
	// Combine stdout and stderr output
	var combined bytes.Buffer
	combined.Write(stdout.Bytes())
	if err != nil {
		// Write error to stderr buffer, then add to combined output
		stderr.WriteString(err.Error() + "\n")
	}
	combined.Write(stderr.Bytes())
	
	return combined.String()
}


// TestUpdateGoldenFiles is a helper test that can be run with -update-golden flag to regenerate golden files
func TestUpdateGoldenFiles(t *testing.T) {
	if !*updateGolden {
		t.Skip("Use -update-golden flag to regenerate golden files")
	}
	
	errorsDir := filepath.Join("errors")
	dashFiles, err := filepath.Glob(filepath.Join(errorsDir, "*.dash"))
	if err != nil {
		t.Fatalf("Failed to find .dash files: %v", err)
	}

	for _, dashFile := range dashFiles {
		testName := strings.TrimSuffix(filepath.Base(dashFile), ".dash")
		
		// Run the dash file and capture output
		output := runDashFile(t, dashFile)
		
		// Write golden file
		goldenFile := filepath.Join("testdata", testName+".golden")
		if err := os.MkdirAll(filepath.Dir(goldenFile), 0755); err != nil {
			t.Fatalf("Failed to create testdata directory: %v", err)
		}
		
		if err := os.WriteFile(goldenFile, []byte(output), 0644); err != nil {
			t.Fatalf("Failed to write golden file %s: %v", goldenFile, err)
		}
		
		t.Logf("Updated golden file: %s", goldenFile)
	}
}