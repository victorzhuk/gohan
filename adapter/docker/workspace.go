package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

// workspace is one open Docker container. SetEnv feeds the environment of
// subsequent Exec calls; it is the only path a resolved secret value takes
// into the workspace.
type workspace struct {
	api         *client.Client
	containerID string
	policy      types.SandboxPolicy
	rec         *Recorder
	netID       string

	// egressAllowIPs are the resolved addresses the chain permits;
	// chainReady marks the chain installed, denySeen the LOG rule's
	// counter already reported.
	egressAllowIPs []string
	chainReady     bool
	denySeen       uint64

	mu  sync.Mutex
	env map[string]string
}

// execTokenEnv tags each exec's environment so the process group can be
// found and killed on expiry inside the container.
const execTokenEnv = "GOHAN_EXEC_TOKEN"

func (w *workspace) envList() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	env := make([]string, 0, len(w.env))
	for k, v := range w.env {
		env = append(env, k+"="+v)
	}
	return env
}

// Exec runs argv in the container through ExecCreate/ExecAttach and reads
// the exit code through ExecInspect. A non-zero exit is a result; a start
// failure is an error. The request Timeout, else the policy MaxExecTime,
// bounds the exec: on expiry the process group is killed inside the
// container and the result (exit code -1) is returned rather than hanging.
func (w *workspace) Exec(ctx context.Context, req types.ExecRequest) (types.ExecResult, error) {
	res, err := w.exec(ctx, req)
	if err == nil {
		w.observeEgress(ctx, res)
	}
	return res, err
}

func (w *workspace) exec(ctx context.Context, req types.ExecRequest) (types.ExecResult, error) {
	start := time.Now()
	timeout := req.Timeout
	if timeout == 0 {
		timeout = w.policy.MaxExecTime
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if len(req.Argv) == 0 {
		return types.ExecResult{}, fmt.Errorf("docker: exec: empty argv")
	}
	token := fmt.Sprintf("%s-%d", w.containerID[:12], time.Now().UnixNano())
	created, err := w.api.ExecCreate(ctx, w.containerID, client.ExecCreateOptions{
		Cmd:          req.Argv,
		WorkingDir:   req.Cwd,
		Env:          append(w.envList(), execTokenEnv+"="+token),
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  len(req.Stdin) > 0,
	})
	if err != nil {
		return types.ExecResult{}, fmt.Errorf("docker: exec create %v: %w", req.Argv, err)
	}
	att, err := w.api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return types.ExecResult{}, fmt.Errorf("docker: exec attach %v: %w", req.Argv, err)
	}
	defer att.Close()

	if _, err := att.Conn.Write(req.Stdin); err != nil && len(req.Stdin) > 0 && ctx.Err() == nil {
		return types.ExecResult{}, fmt.Errorf("docker: exec stdin %v: %w", req.Argv, err)
	}
	_ = att.CloseWrite()

	var stdout, stderr bytes.Buffer
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		_, _ = stdcopy.StdCopy(&stdout, &stderr, att.Reader)
	}()

	timedOut := false
	var finished bool
	var exitCode int
	for {
		insp, err := w.api.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err != nil {
			if ctx.Err() != nil {
				timedOut = true
				break
			}
			return types.ExecResult{}, fmt.Errorf("docker: exec inspect %v: %w", req.Argv, err)
		}
		// The daemon allocates the exec slot during ExecAttach and starts
		// the process afterwards, so an exec that has not started yet
		// reports Running=false with the default exit code. Reading that
		// window as the outcome reports exit 0 for a command that never
		// ran. The attach stream closes only when the process ends, so the
		// exit code is read only after the stream is done.
		if !insp.Running {
			select {
			case <-streamDone:
				exitCode = insp.ExitCode
				timedOut = false
				finished = true
			default:
			}
			if finished {
				break
			}
		}
		if ctx.Err() != nil {
			timedOut = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if timedOut {
		exitCode = -1
		w.killGroup(token)
		select {
		case <-streamDone:
		case <-time.After(5 * time.Second):
			_ = att.Conn.SetReadDeadline(time.Now())
		}
	}
	<-streamDone

	if !timedOut && exitCode == 127 && bytes.HasPrefix(stdout.Bytes(), []byte("OCI runtime exec failed")) {
		return types.ExecResult{}, fmt.Errorf("docker: exec start %v: %s", req.Argv,
			bytes.TrimSuffix(stdout.Bytes(), []byte("\r\n")))
	}

	return types.ExecResult{
		ExitCode: exitCode,
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		Seconds:  time.Since(start).Seconds(),
	}, nil
}

// killGroup kills the exec process group inside the container on expiry.
// ExecInspect reports a host pid, so the group is found by the exec token
// in /proc/*/environ: the exec process and the children inheriting its
// environment die, the container keeps running. Best-effort; the result of
// the timed-out exec is returned either way.
func (w *workspace) killGroup(token string) {
	if token == "" {
		return
	}
	kctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created, err := w.api.ExecCreate(kctx, w.containerID, client.ExecCreateOptions{
		Cmd: []string{"/bin/sh", "-c", fmt.Sprintf(
			`for e in /proc/[0-9]*/environ; do if tr '\0' '\n' <"$e" 2>/dev/null | grep -Fq '%s'; then p=${e%%/environ}; p=${p#/proc/}; kill -KILL "$p" 2>/dev/null; fi; done`, token,
		)},
	})
	if err != nil {
		return
	}
	_, _ = w.api.ExecStart(kctx, created.ID, client.ExecStartOptions{Detach: true})
}

// Put writes r to path inside the container over the archive API.
func (w *workspace) Put(ctx context.Context, p string, r io.Reader) error {
	if strings.TrimPrefix(p, "/") == "" || strings.HasSuffix(p, "/") {
		return fmt.Errorf("docker: put: %q is not a file path", p)
	}
	var buf bytes.Buffer
	body, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("docker: put %s: %w", p, err)
	}
	tw := tar.NewWriter(&buf)
	rel := strings.TrimPrefix(p, "/")
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts)-1; i++ {
		if err := tw.WriteHeader(&tar.Header{
			Name:     strings.Join(parts[:i+1], "/") + "/",
			Typeflag: tar.TypeDir,
			Mode:     0o755,
		}); err != nil {
			return fmt.Errorf("docker: put %s: %w", p, err)
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: rel, Mode: 0o644, Size: int64(len(body))}); err != nil {
		return fmt.Errorf("docker: put %s: %w", p, err)
	}
	if _, err := tw.Write(body); err != nil {
		return fmt.Errorf("docker: put %s: %w", p, err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("docker: put %s: %w", p, err)
	}
	if _, err := w.api.CopyToContainer(ctx, w.containerID, client.CopyToContainerOptions{
		DestinationPath:           "/",
		Content:                   &buf,
		AllowOverwriteDirWithFile: true,
	}); err != nil {
		return fmt.Errorf("docker: put %s: %w", p, err)
	}
	return nil
}

// Get reads path from the container over the archive API. The returned
// reader yields the file content; closing it releases the archive stream.
func (w *workspace) Get(ctx context.Context, p string) (io.ReadCloser, error) {
	res, err := w.api.CopyFromContainer(ctx, w.containerID, client.CopyFromContainerOptions{SourcePath: p})
	if err != nil {
		return nil, fmt.Errorf("docker: get %s: %w", p, err)
	}
	tr := tar.NewReader(res.Content)
	hdr, err := tr.Next()
	if err != nil {
		_ = res.Content.Close()
		return nil, fmt.Errorf("docker: get %s: empty archive: %w", p, err)
	}
	if hdr.FileInfo().IsDir() {
		_ = res.Content.Close()
		return nil, fmt.Errorf("docker: get %s: is a directory", p)
	}
	return &fileReader{Reader: tr, closer: res.Content}, nil
}

type fileReader struct {
	io.Reader
	closer io.Closer
}

func (f *fileReader) Close() error { return f.closer.Close() }

// SetEnv merges env into the injected environment of subsequent Exec
// calls. It is the only path a resolved secret value takes into the
// workspace.
func (w *workspace) SetEnv(_ context.Context, env map[string]string) error {
	if len(env) == 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, v := range env {
		w.env[k] = v
	}
	return nil
}

// run executes one adapter command inside the container and fails on a
// non-zero exit. It bypasses the egress observation wrapper.
func (w *workspace) run(ctx context.Context, argv ...string) ([]byte, error) {
	res, err := w.exec(ctx, types.ExecRequest{Argv: argv})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("exit %d: %s", res.ExitCode, res.Stderr)
	}
	return res.Stdout, nil
}

// Close removes the container this workspace opened and its per-workspace
// egress network.
func (w *workspace) Close(ctx context.Context) error {
	if _, err := w.api.ContainerRemove(ctx, w.containerID, client.ContainerRemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	}); err != nil {
		return fmt.Errorf("docker: close container %s: %w", w.containerID, err)
	}
	if w.netID != "" && w.netID != "none" {
		if _, err := w.api.NetworkRemove(ctx, w.netID, client.NetworkRemoveOptions{}); err != nil {
			return fmt.Errorf("docker: close network %s: %w", w.netID, err)
		}
	}
	return nil
}
