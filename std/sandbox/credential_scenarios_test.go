package sandbox

import (
	"context"
	"io"
	"strings"
	"testing"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
)

// credentialWorkspace records every environment injection and file write,
// so the scenario can assert against the fake workspace's SetEnv and Put
// calls.
type credentialWorkspace struct {
	recordingWorkspace
	puts [][]byte
}

func (w *credentialWorkspace) Put(_ context.Context, _ string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	w.puts = append(w.puts, data)
	return nil
}

func TestCredentialNotInSandbox(t *testing.T) {
	t.Run("identity.credential-not-in-sandbox", func(t *testing.T) {
		token := "cred-token-5c19ab"
		sb, _ := newFake(false)
		w := &credentialWorkspace{}
		sb.ws = w
		ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerCall})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		ctx := gohan.WithCredential(context.Background(), types.Credential{Token: token})

		code := `echo "$GOHAN_CREDENTIAL"; cat cred.txt 2>/dev/null; env`
		if _, err := ts.Tools()[0].Call(ctx, args(t, runCodeArgs{Code: code})); err != nil {
			t.Fatalf("run_code: %v", err)
		}
		if _, err := ts.Tools()[1].Call(ctx, args(t, writeFileArgs{Path: "out.txt", Content: "body"})); err != nil {
			t.Fatalf("write_file: %v", err)
		}

		for i, env := range w.envs {
			for k, v := range env {
				if strings.Contains(v, token) {
					t.Errorf("SetEnv %d: %s=%q derives from the credential token", i, k, v)
				}
			}
		}
		if len(w.envs) != 0 {
			t.Errorf("SetEnv calls = %d, want 0: the credential is not a secret grant", len(w.envs))
		}
		for i, put := range w.puts {
			if strings.Contains(string(put), token) {
				t.Errorf("Put %d carries the credential token: %q", i, put)
			}
		}
		for _, req := range w.execReqs {
			for _, arg := range req.Argv {
				if strings.Contains(arg, token) {
					t.Errorf("exec argv %q carries the credential token", arg)
				}
			}
		}
	})
}
