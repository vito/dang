package editors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/dagger/otel-go/oteltestctx"
	"github.com/dagger/testctx"
	"gotest.tools/v3/golden"
)

func TestMain(m *testing.M) {
	os.Exit(oteltestctx.Main(m))
}

type EditorsSuite struct {
}

func TestEditors(tT *testing.T) {
	testctx.New(tT,
		oteltestctx.WithTracing[*testing.T](),
	).RunTests(EditorsSuite{})
}

// requireSubmodule skips the test if the given editor submodule has not been
// initialized (git submodule update --init).
func requireSubmodule(t *testctx.T, repoRoot, submodule string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(submodule), ".git")); err != nil {
		t.Skipf("%s submodule not initialized; run git submodule update --init", submodule)
	}
}

type highlightCorpusCase struct {
	ID       string
	File     string
	Name     string
	Source   string
	Expected string
	Actual   string
}

type highlightInput struct {
	ParserPath     string               `json:"parser_path"`
	NvimPluginRoot string               `json:"nvim_plugin_root"`
	Cases          []highlightInputCase `json:"cases"`
}

type highlightInputCase struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

type highlightOutput struct {
	Error string                `json:"error,omitempty"`
	Cases []highlightOutputCase `json:"cases"`
}

type highlightOutputCase struct {
	ID    string          `json:"id"`
	Spans []highlightSpan `json:"spans"`
}

type highlightSpan struct {
	Capture   string `json:"capture"`
	Lang      string `json:"lang"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	Priority  int    `json:"priority"`
	Order     int    `json:"order"`
}

type zedExtensionConfig struct {
	Grammars map[string]zedGrammarConfig `toml:"grammars"`
}

type zedGrammarConfig struct {
	Repository string `toml:"repository"`
	Commit     string `toml:"commit"`
	Path       string `toml:"path"`
}

func (EditorsSuite) TestNeovimHighlights(ctx context.Context, t *testctx.T) {
	if testing.Short() {
		t.Skip("skipping Neovim highlight tests in short mode")
	}

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	requireSubmodule(t, repoRoot, "editors/nvim")

	casesByFile, cases, err := loadHighlightCorpus(filepath.Join("highlights", "corpus"))
	if err != nil {
		t.Fatalf("load highlight corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatalf("no highlight corpus cases found")
	}

	parserPath := buildDangTreeSitterParser(t, repoRoot)
	results := runNeovimHighlightDump(ctx, t, repoRoot, parserPath, cases)

	for _, testCase := range cases {
		spans, ok := results[testCase.ID]
		if !ok {
			t.Fatalf("Neovim output missing case %q", testCase.Name)
		}
		testCase.Actual = renderHighlightTree(testCase.Source, spans)
	}

	corpusFiles := sortedMapKeys(casesByFile)

	if golden.FlagUpdate() {
		for _, file := range corpusFiles {
			if err := writeHighlightCorpus(file, casesByFile[file]); err != nil {
				t.Fatalf("update %s: %v", file, err)
			}
		}
		return
	}

	for _, file := range corpusFiles {
		file := file
		fileCases := casesByFile[file]
		t.Run(filepath.Base(file), func(_ context.Context, t *testctx.T) {
			for _, testCase := range fileCases {
				testCase := testCase
				t.Run(testCase.Name, func(_ context.Context, t *testctx.T) {
					if testCase.Actual != testCase.Expected {
						t.Errorf(
							"highlight corpus mismatch for %s\n\nexpected:\n%s\n\nactual:\n%s\n\nrun go test ./editors -run %q -update to regenerate",
							testCase.Name,
							testCase.Expected,
							testCase.Actual,
							"TestEditors/TestNeovimHighlights",
						)
					}
				})
			}
		})
	}
}

func (EditorsSuite) TestZedHighlightQueryCompatibility(ctx context.Context, t *testctx.T) {
	if testing.Short() {
		t.Skip("skipping Zed highlight query compatibility tests in short mode")
	}

	if _, err := exec.LookPath("tree-sitter"); err != nil {
		t.Skip("tree-sitter CLI not installed; skipping Zed highlight query compatibility tests")
	}

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	requireSubmodule(t, repoRoot, "editors/zed")

	casesByFile, _, err := loadHighlightCorpus(filepath.Join("highlights", "corpus"))
	if err != nil {
		t.Fatalf("load highlight corpus: %v", err)
	}

	extensionPath := filepath.Join(repoRoot, "editors", "zed", "extension.toml")
	queryPath := filepath.Join(repoRoot, "editors", "zed", "languages", "dang", "highlights.scm")
	trackTestInput(t, extensionPath)
	trackTestInput(t, queryPath)

	grammar := loadZedDangGrammarConfig(t, extensionPath)
	if grammar.Path == "" {
		t.Fatalf("editors/zed/extension.toml [grammars.dang] is missing path")
	}
	if grammar.Commit == "" {
		t.Fatalf("editors/zed/extension.toml [grammars.dang] is missing commit")
	}

	grammarPath := extractGitTree(t, repoRoot, grammar.Commit, grammar.Path)

	for _, file := range sortedMapKeys(casesByFile) {
		fileCases := casesByFile[file]
		t.Run(filepath.Base(file), func(ctx context.Context, t *testctx.T) {
			for _, testCase := range fileCases {
				testCase := testCase
				t.Run(testCase.Name, func(ctx context.Context, t *testctx.T) {
					runZedHighlightQuerySmoke(ctx, t, grammarPath, queryPath, testCase)
				})
			}
		})
	}
}

func loadHighlightCorpus(dir string) (map[string][]*highlightCorpusCase, []*highlightCorpusCase, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)

	casesByFile := map[string][]*highlightCorpusCase{}
	var allCases []*highlightCorpusCase
	for _, file := range files {
		fileCases, err := parseHighlightCorpusFile(file)
		if err != nil {
			return nil, nil, err
		}
		casesByFile[file] = fileCases
		allCases = append(allCases, fileCases...)
	}

	for i, testCase := range allCases {
		testCase.ID = fmt.Sprintf("%s:%d", filepath.ToSlash(testCase.File), i)
	}

	return casesByFile, allCases, nil
}

func parseHighlightCorpusFile(path string) ([]*highlightCorpusCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")

	var cases []*highlightCorpusCase
	for i := 0; i < len(lines); {
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		if i >= len(lines) {
			break
		}

		if lines[i] != "===" {
			return nil, fmt.Errorf("%s:%d: expected ===", path, i+1)
		}
		i++

		nameStart := i
		for i < len(lines) && lines[i] != "===" {
			i++
		}
		if i >= len(lines) {
			return nil, fmt.Errorf("%s:%d: missing closing ===", path, nameStart+1)
		}
		name := strings.TrimSpace(strings.Join(lines[nameStart:i], "\n"))
		if name == "" {
			return nil, fmt.Errorf("%s:%d: empty case name", path, nameStart+1)
		}
		i++

		if i < len(lines) && lines[i] == "" {
			i++
		}

		sourceStart := i
		for i < len(lines) && lines[i] != "---" {
			i++
		}
		if i >= len(lines) {
			return nil, fmt.Errorf("%s:%d: missing ---", path, sourceStart+1)
		}
		source := joinCorpusBlock(lines[sourceStart:i])
		i++

		if i < len(lines) && lines[i] == "" {
			i++
		}

		expectedStart := i
		for i < len(lines) && lines[i] != "===" {
			i++
		}
		expected := joinCorpusBlock(lines[expectedStart:i])

		cases = append(cases, &highlightCorpusCase{
			File:     path,
			Name:     name,
			Source:   source,
			Expected: expected,
		})
	}

	return cases, nil
}

func joinCorpusBlock(lines []string) string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func writeHighlightCorpus(path string, cases []*highlightCorpusCase) error {
	var buf bytes.Buffer
	for i, testCase := range cases {
		if i > 0 {
			buf.WriteByte('\n')
		}
		fmt.Fprintf(&buf, "===\n%s\n===\n\n%s\n\n---\n\n%s\n", testCase.Name, testCase.Source, testCase.Actual)
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

func loadZedDangGrammarConfig(t *testctx.T, extensionPath string) zedGrammarConfig {
	t.Helper()

	var config zedExtensionConfig
	if _, err := toml.DecodeFile(extensionPath, &config); err != nil {
		t.Fatalf("decode %s: %v", extensionPath, err)
	}

	grammar, ok := config.Grammars["dang"]
	if !ok {
		t.Fatalf("%s is missing [grammars.dang]", extensionPath)
	}
	return grammar
}

func extractGitTree(t *testctx.T, repoRoot, commit, path string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("skipping Zed highlight query compatibility tests on Windows")
	}
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not installed; skipping Zed highlight query compatibility tests")
	}

	cmd := exec.Command("git", "-C", repoRoot, "cat-file", "-e", commit+"^{commit}")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("grammar commit %s from editors/zed/extension.toml is not present locally; skipping Zed compatibility test\n%s", commit, output)
	}

	archivePath := filepath.Join(t.TempDir(), "tree.tar")
	cmd = exec.Command("git", "-C", repoRoot, "archive", "--format=tar", "--output", archivePath, commit, path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("archive %s:%s: %v\n%s", commit, path, err, output)
	}

	outDir := t.TempDir()
	cmd = exec.Command("tar", "-xf", archivePath, "-C", outDir)
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("extract %s: %v\n%s", archivePath, err, output)
	}

	return filepath.Join(outDir, filepath.FromSlash(path))
}

func runZedHighlightQuerySmoke(ctx context.Context, t *testctx.T, grammarPath, queryPath string, testCase *highlightCorpusCase) {
	t.Helper()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "case.dang")
	if err := os.WriteFile(sourcePath, []byte(testCase.Source), 0644); err != nil {
		t.Fatalf("write Zed highlight source: %v", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tree-sitter", "query", "--captures", "--grammar-path", grammarPath, queryPath, sourcePath)
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("tree-sitter query timed out\n%s", output)
	}
	if err != nil {
		t.Fatalf("tree-sitter query failed: %v\n%s", err, output)
	}
	if !bytes.Contains(output, []byte("capture:")) {
		t.Fatalf("tree-sitter query produced no captures\n%s", output)
	}
}

func buildDangTreeSitterParser(t *testctx.T, repoRoot string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("skipping Neovim highlight tests on Windows")
	}

	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc not installed; skipping Neovim highlight tests")
	}

	out := filepath.Join(t.TempDir(), "dang.so")
	srcDir := filepath.Join(repoRoot, "treesitter", "src")
	parserC := filepath.Join(srcDir, "parser.c")
	scannerC := filepath.Join(srcDir, "scanner.c")
	trackTestInput(t, parserC)
	trackTestInput(t, scannerC)

	args := []string{
		"-fPIC",
		"-I", srcDir,
		parserC,
		scannerC,
		"-o", out,
	}
	if runtime.GOOS == "darwin" {
		args = append([]string{"-dynamiclib"}, args...)
	} else {
		args = append([]string{"-shared"}, args...)
	}

	cmd := exec.Command(cc, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build Dang tree-sitter parser: %v\n%s", err, output)
	}

	return out
}

func trackTestInput(t *testctx.T, path string) {
	t.Helper()
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("read test input %s: %v", path, err)
	}
}

func trackTestInputs(t *testctx.T, pattern string) {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob test inputs %s: %v", pattern, err)
	}
	if len(matches) == 0 {
		t.Fatalf("glob test inputs %s: no matches", pattern)
	}
	for _, match := range matches {
		trackTestInput(t, match)
	}
}

func runNeovimHighlightDump(ctx context.Context, t *testctx.T, repoRoot, parserPath string, cases []*highlightCorpusCase) map[string][]highlightSpan {
	t.Helper()

	nvimBin := os.Getenv("DANG_HIGHLIGHT_NEOVIM_BIN")
	if nvimBin == "" {
		var err error
		nvimBin, err = exec.LookPath("nvim")
		if err != nil {
			t.Skip("nvim not installed; skipping Neovim highlight tests")
		}
	}

	tmp := t.TempDir()
	inputPath := filepath.Join(tmp, "input.json")
	outputPath := filepath.Join(tmp, "output.json")

	input := highlightInput{
		ParserPath:     parserPath,
		NvimPluginRoot: filepath.Join(repoRoot, "editors", "nvim"),
	}
	for _, testCase := range cases {
		input.Cases = append(input.Cases, highlightInputCase{ID: testCase.ID, Source: testCase.Source})
	}

	inputBytes, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal Neovim highlight input: %v", err)
	}
	if err := os.WriteFile(inputPath, inputBytes, 0644); err != nil {
		t.Fatalf("write Neovim highlight input: %v", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	scriptPath := filepath.Join(repoRoot, "editors", "highlights", "highlight_dump.lua")
	trackTestInput(t, scriptPath)
	trackTestInput(t, filepath.Join(repoRoot, "editors", "nvim", "lua", "dang", "init.lua"))
	trackTestInputs(t, filepath.Join(repoRoot, "editors", "nvim", "queries", "dang", "*.scm"))

	cmd := exec.CommandContext(ctx, nvimBin, "--clean", "--headless", "-n", "--noplugin", "+lua dofile(vim.env.DANG_HIGHLIGHT_SCRIPT)")
	cmd.Env = append(os.Environ(),
		"NVIM_APPNAME=dang-highlight-tests-"+strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()),
		"DANG_HIGHLIGHT_INPUT="+inputPath,
		"DANG_HIGHLIGHT_OUTPUT="+outputPath,
		"DANG_HIGHLIGHT_SCRIPT="+scriptPath,
	)

	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("Neovim highlight dump timed out\n%s", output)
	}
	if err != nil {
		t.Fatalf("Neovim highlight dump failed: %v\n%s", err, output)
	}

	outputBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read Neovim highlight output: %v\nNeovim output:\n%s", err, output)
	}

	var decoded highlightOutput
	if err := json.Unmarshal(outputBytes, &decoded); err != nil {
		t.Fatalf("decode Neovim highlight output: %v\n%s", err, outputBytes)
	}
	if decoded.Error != "" {
		t.Fatalf("Neovim highlight dump error:\n%s", decoded.Error)
	}

	results := map[string][]highlightSpan{}
	for _, result := range decoded.Cases {
		results[result.ID] = result.Spans
	}
	return results
}

func renderHighlightTree(source string, spans []highlightSpan) string {
	boundaries := map[int]bool{0: true, len(source): true}
	for _, span := range spans {
		if span.StartByte < 0 || span.EndByte > len(source) || span.StartByte >= span.EndByte {
			continue
		}
		boundaries[span.StartByte] = true
		boundaries[span.EndByte] = true
	}

	positions := make([]int, 0, len(boundaries))
	for pos := range boundaries {
		positions = append(positions, pos)
	}
	sort.Ints(positions)

	type renderedSegment struct {
		captures []string
		text     string
	}

	var segments []renderedSegment
	for i := 0; i+1 < len(positions); i++ {
		start, end := positions[i], positions[i+1]
		if start == end {
			continue
		}

		active := activeHighlightSpans(spans, start, end)
		captures := make([]string, len(active))
		for j, span := range active {
			captures[j] = span.Capture
		}

		text := source[start:end]
		if len(segments) > 0 && sameStrings(segments[len(segments)-1].captures, captures) {
			segments[len(segments)-1].text += text
			continue
		}

		segments = append(segments, renderedSegment{captures: captures, text: text})
	}

	var buf bytes.Buffer
	buf.WriteString("(highlights")
	for _, segment := range segments {
		buf.WriteString("\n  ")
		if len(segment.captures) == 0 {
			buf.WriteString(strconv.Quote(segment.text))
			continue
		}

		buf.WriteByte('(')
		for i, capture := range segment.captures {
			if i > 0 {
				buf.WriteByte(' ')
			}
			buf.WriteByte('@')
			buf.WriteString(capture)
		}
		buf.WriteByte(' ')
		buf.WriteString(strconv.Quote(segment.text))
		buf.WriteByte(')')
	}
	buf.WriteString("\n)")
	return buf.String()
}

func activeHighlightSpans(spans []highlightSpan, start, end int) []highlightSpan {
	var active []highlightSpan
	for _, span := range spans {
		if span.StartByte <= start && span.EndByte >= end {
			active = append(active, span)
		}
	}

	sort.SliceStable(active, func(i, j int) bool {
		if active[i].Priority != active[j].Priority {
			return active[i].Priority < active[j].Priority
		}
		return active[i].Order < active[j].Order
	})

	return active
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
