package dang

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// displayHandle evaluates source against fakeSchema, imported as Test, and
// returns how Display shows the result, checking that showing it sent
// nothing.
func displayHandle(t *testing.T, source string) string {
	t.Helper()
	ctx, client := fakeGraphQLContext(t, true)
	m, err := LoadDir(ctx, t.TempDir(), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()
	val, _, err := m.Eval(ctx, "", source)
	require.NoError(t, err)
	shown := Display(val)
	require.Zero(t, client.count(), "displaying %s sent a query", source)
	return shown
}

// A handle shows its type and chain with argument names but never their
// values, which may be secrets: not strings, lists, or other handles.
func TestDisplayHandle(t *testing.T) {
	for _, c := range []struct {
		name, source, want string
	}{
		{
			name:   "container chain",
			source: `container.from(address: "alpine").withEnv(name: "A", value: "b")`,
			want:   `<Container: container.from(address: …).withEnv(name: …, value: …)>`,
		},
		{
			name:   "setSecret",
			source: `setSecret(name: "token", plaintext: "hunter2")`,
			want:   `<Secret: setSecret(name: …, plaintext: …)>`,
		},
		{
			name:   "object argument",
			source: `container.withSecretVariable(name: "TOKEN", secret: setSecret(name: "token", plaintext: "hunter2"))`,
			want:   `<Container: container.withSecretVariable(name: …, secret: …)>`,
		},
		{
			name:   "list argument",
			source: `container.withExec(args: ["echo", "hi"])`,
			want:   `<Container: container.withExec(args: …)>`,
		},
		{
			name:   "mutation",
			source: `Mutation.createContainer(name: "a")`,
			want:   `<Container: mutation.createContainer(name: …)>`,
		},
		{
			name:   "list handle",
			source: `containers`,
			want:   `<[Container]: containers>`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := displayHandle(t, c.source)
			require.Equal(t, c.want, got)
			require.NotContains(t, got, "hunter2")
		})
	}
}
