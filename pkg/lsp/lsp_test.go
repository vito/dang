package lsp_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"github.com/neovim/go-client/nvim"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	os.Exit(oteltest.Main(m))
}

func TestLSP(tT *testing.T) {
	testctx.New(tT,
		oteltest.WithTracing[*testing.T](),
		oteltest.WithLogging[*testing.T](),
	).RunTests(LSPSuite{})
}

type LSPSuite struct{}

func (LSPSuite) TestNeovimGoToDefinition(ctx context.Context, t *testctx.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	if checkNested(t) {
		return
	}

	testFile(t, sandboxNvim(t), "testdata/gd.dang")
}

func (LSPSuite) TestNeovimCompletion(ctx context.Context, t *testctx.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	if checkNested(t) {
		return
	}

	testFile(t, sandboxNvim(t), "testdata/complete.dang")
}

func (LSPSuite) TestNeovimRename(ctx context.Context, t *testctx.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	if checkNested(t) {
		return
	}

	testFile(t, sandboxNvim(t), "testdata/rename.dang")
}

func (LSPSuite) TestNeovimHover(ctx context.Context, t *testctx.T) {
	if testing.Short() {
		t.SkipNow()
		return
	}

	if checkNested(t) {
		return
	}

	testFile(t, sandboxNvim(t), "testdata/hover.dang")
}

func checkNested(t *testctx.T) bool {
	if os.Getenv("NVIM") != "" {
		t.Skip("detected running from neovim; skipping to avoid hanging")
		return true
	}

	return false
}

func testHover(t *testctx.T, client *nvim.Nvim, testLine int, codes string, expectedContent string) {
	// Execute the key sequence (e.g., "K")
	keys, err := client.ReplaceTermcodes(codes, true, true, true)
	require.NoError(t, err)

	err = client.FeedKeys(keys, "t", true)
	require.NoError(t, err)

	// Wait for floating window to appear and capture its content
	require.Eventually(t, func() bool {
		// Use Lua to find floating windows and get their content
		var content string
		err := client.ExecLua(`
			local wins = vim.api.nvim_list_wins()
			for _, win in ipairs(wins) do
				local config = vim.api.nvim_win_get_config(win)
				if config.relative ~= '' then
					local buf = vim.api.nvim_win_get_buf(win)
					local lines = vim.api.nvim_buf_get_lines(buf, 0, -1, false)
					return table.concat(lines, '\n')
				end
			end
			return ''
		`, &content)

		if err != nil {
			t.Logf("L%03d %s\tfailed to get floating window content: %v", testLine, codes, err)
			return false
		}

		if content == "" {
			t.Logf("L%03d %s\tno floating window found", testLine, codes)
			return false
		}

		if strings.Contains(content, expectedContent) {
			t.Logf("L%03d %s\tmatched hover content: %q", testLine, codes, expectedContent)
			return true
		}

		t.Logf("L%03d %s\tfloating window content %q does not contain %q", testLine, codes, content, expectedContent)
		return false
	}, 2*time.Second, 100*time.Millisecond)

	// Close the hover window
	err = client.FeedKeys("\x1b", "t", true)
	require.NoError(t, err)
}

func testFile(t *testctx.T, client *nvim.Nvim, file string) {
	err := client.Command(`edit ` + file)
	require.NoError(t, err)

	testBuf, err := client.CurrentBuffer()
	require.NoError(t, err)

	window, err := client.CurrentWindow()
	require.NoError(t, err)

	require.Eventually(t, func() bool { // wait for LSP client to attach
		var b bool
		err := client.Eval(`luaeval('#vim.lsp.get_clients({bufnr = 0}) > 0')`, &b)
		return err == nil && b
	}, 5*time.Second, 10*time.Millisecond)

	lineCount, err := client.BufferLineCount(testBuf)
	require.NoError(t, err)

	t.Logf("lines: %d", lineCount)

	t.Cleanup(func() {
		var fn string
		err := client.Eval(`luaeval('vim.lsp.log.get_filename()')`, &fn)
		require.NoError(t, err)

		if testing.Verbose() {
			lspLogs, err := os.ReadFile(fn)
			if err == nil {
				t.Logf("language server logs:\n\n%s", string(lspLogs))
			}
		}
	})

	for testLine := 1; testLine <= lineCount; testLine++ {
		mode, err := client.Mode()
		require.NoError(t, err)

		if mode.Mode != "n" {
			// reset back to normal mode; some tests can't <esc> immediately because
			// they have to wait for the language server (e.g. completion)
			err = client.FeedKeys("\x1b", "t", true)
			require.NoError(t, err)
		}

		err = client.SetWindowCursor(window, [2]int{testLine, 0})
		require.NoError(t, err)

		lineb, err := client.CurrentLine()
		require.NoError(t, err)
		line := string(lineb)

		line = strings.ReplaceAll(line, "nofmt ", "")
		line, test, ok := strings.Cut(line, " # test: ")
		if !ok {
			continue
		}

		// Check for hover test (keys -> hover: expected)
		if keys, hover, ok := strings.Cut(test, " => hover: "); ok {
			codes := strings.TrimSpace(keys)
			expectedContent := strings.TrimSpace(hover)
			testHover(t, client, testLine, codes, expectedContent)
			continue
		}

		keys, assertion, ok := strings.Cut(test, " => ")
		if !ok {
			t.Errorf("invalid test line: %q", line)
			continue
		}

		codes := strings.TrimSpace(keys)

		// Split codes on {delay:...} markers
		parts := strings.Split(codes, "{delay:")
		for i, part := range parts {
			if i > 0 {
				// Extract delay duration and remaining keys
				delayEnd := strings.Index(part, "}")
				if delayEnd == -1 {
					t.Fatalf("invalid delay syntax: missing }")
				}
				delayStr := part[:delayEnd]
				delay, err := time.ParseDuration(delayStr)
				require.NoError(t, err)

				t.Logf("L%03d sleeping for %v", testLine, delay)
				time.Sleep(delay)

				part = part[delayEnd+1:] // remaining keys after }
			}

			if part == "" {
				continue
			}

			keys, err := client.ReplaceTermcodes(part, true, true, true)
			require.NoError(t, err)

			err = client.FeedKeys(keys, "t", true)
			require.NoError(t, err)
		}

		targetPos := strings.Index(assertion, "┃")
		target := strings.ReplaceAll(assertion, "┃", "")
		target = strings.ReplaceAll(target, "\\t", "\t")

		require.Eventually(t, func() bool { // wait for the definition to be found
			lineb, err := client.CurrentLine()
			require.NoError(t, err)
			line := string(lineb)
			line, _, _ = strings.Cut(line, " # test:")

			pos, err := client.WindowCursor(window)
			require.NoError(t, err)

			idx := strings.Index(line, target)
			if idx == -1 {
				t.Logf("L%03d %s\tline %q does not contain %q", testLine, codes, string(line), target)
				return false
			}

			col := targetPos + idx // account for leading whitespace

			if pos[1] != col {
				t.Logf("L%03d %s\tline %q: at %d, need %d", testLine, codes, string(line), pos[1], col)
				return false
			}

			t.Logf("L%03d %s\tmatched: %s", testLine, codes, assertion)

			return true
		}, 1*time.Second, 100*time.Millisecond)

		// go back from definition to initial test buffer
		err = client.SetCurrentBuffer(testBuf)
		require.NoError(t, err)
	}
}

func sandboxNvim(t *testctx.T) *nvim.Nvim {
	ctx := context.Background()

	cmd := os.Getenv("DANG_LSP_NEOVIM_BIN")
	if cmd == "" {
		var err error
		cmd, err = exec.LookPath("nvim")
		if err != nil {
			t.Skip("nvim not installed; skipping LSP tests")
		}
	}

	client, err := nvim.NewChildProcess(
		nvim.ChildProcessCommand(cmd),
		nvim.ChildProcessArgs("--clean", "-n", "--embed", "--headless", "--noplugin", "-V10nvim.log"),
		nvim.ChildProcessContext(ctx),
		nvim.ChildProcessLogf(t.Logf),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		err := client.Close()
		if err != nil {
			t.Logf("failed to close neovim: %s", err)
		}

		if t.Failed() {
			nvimLogs, err := os.ReadFile("nvim.log")
			if err == nil {
				for _, line := range lastN(strings.Split(string(nvimLogs), "\n"), 10) {
					t.Logf("neovim: %s", line)
				}
			}
		}
	})

	err = client.Command(`source testdata/config.lua`)
	require.NoError(t, err)

	paths, err := client.RuntimePaths()
	require.NoError(t, err)

	t.Logf("runtimepath: %v", paths)

	return client
}

func lastN[T any](vals []T, n int) []T {
	if len(vals) <= n {
		return vals
	}

	return vals[len(vals)-n:]
}
