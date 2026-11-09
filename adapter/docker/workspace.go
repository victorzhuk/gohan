package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/victorzhuk/gohan/core/types"
)

// workspace is one open Docker container. SetEnv feeds the environment of
// subsequent Exec calls; it is the only path a resolved secret value takes
// into the workspace.
type workspace struct {
	api         *client.Client
	containerID string
	// ownerName and anchorID are immutable after Open: ownerName
	// identifies the never-reused workload group the encoded
	// ResourceRef carries, anchorID the trusted helper whose network
	// namespace the workload shares.
	ownerName string
	anchorID  string
	policy    types.SandboxPolicy
	rec       *Recorder
	netID     string

	// egressAllowIPs are the resolved addresses the chain permits;
	// chainReady marks the chain installed, denySeen the LOG rule's
	// counter already reported. egressMu serializes the counter
	// read-modify-write across concurrent Exec calls.
	egressAllowIPs []string
	chainReady     bool
	denySeen       uint64
	egressMu       sync.Mutex

	mu  sync.Mutex
	env map[string]string
}

// ResourceRef is the types.WorkspaceResource identity the cleanup record
// persists and CloseByRef removes: a versioned encoding of the full
// container ID and the immutable owner name, so the anchor and the
// private network stay recoverable after the workload container is
// deleted.
func (w *workspace) ResourceRef() types.WorkspaceRef {
	return types.WorkspaceRef(workloadRefPrefix + w.containerID + ":" + w.ownerName)
}

// setChainReady records complete policy installation.
func (w *workspace) setChainReady(ready bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.chainReady = ready
}

// chainReadyIs reports the installed state.
func (w *workspace) chainReadyIs() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.chainReady
}

// createWorkload creates the stopped workload container: no
// capabilities, no-new-privileges, an explicit idle entrypoint and a
// disabled inherited healthcheck. mode is "none" for deny-all policies
// or "container:<anchor-id>" for the shared trusted namespace.
func (w *workspace) createWorkload(ctx context.Context, image string, p types.SandboxPolicy, mode container.NetworkMode, netID string) error {
	labels := map[string]string{
		LabelSandbox: "true",
		labelRole:    roleWorkload,
		labelOwner:   w.ownerName,
	}
	if netID != "" {
		labels[labelNetwork] = netID
	}
	cfg := &container.Config{
		Image:       image,
		Entrypoint:  []string{"sleep", "infinity"},
		Healthcheck: &container.HealthConfig{Test: []string{"NONE"}},
		Labels:      labels,
	}
	host := &container.HostConfig{
		Resources:   resources(p),
		NetworkMode: mode,
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges"},
	}
	created, err := w.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     cfg,
		HostConfig: host,
	})
	if err != nil {
		return err
	}
	w.containerID = created.ID
	return nil
}

// unwindGroup removes every created group resource after a failed Open,
// each removal under a fresh bounded context so a cancelled or expired
// setup context cannot strand partial resources.
func (w *workspace) unwindGroup() error {
	ctx, cancel := context.WithTimeout(context.Background(), adminTimeout)
	defer cancel()
	var errs []error
	if err := removeContainer(ctx, w.api, w.containerID, true); err != nil {
		errs = append(errs, err)
	}
	if err := removeContainer(ctx, w.api, w.anchorID, true); err != nil {
		errs = append(errs, err)
	}
	if err := removeNetwork(ctx, w.api, w.netID); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
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
	return w.execIn(ctx, w.containerID, req)
}

// adminExec executes one command inside the trusted anchor and returns
// the raw result; the only path policy installation takes. Each
// administration operation carries its own fresh ten-second maximum,
// independent of the workload's execution policy.
func (w *workspace) adminExec(ctx context.Context, argv ...string) (types.ExecResult, error) {
	actx, cancel := context.WithTimeout(ctx, adminTimeout)
	defer cancel()
	return w.execIn(actx, w.anchorID, types.ExecRequest{Argv: argv})
}

// adminRun executes one command inside the trusted anchor. It is the
// only path policy installation and counter observation take; the
// public Exec surface never administers the firewall. The fresh
// ten-second maximum is per operation, so a stalled administration
// read never holds a public Exec past the bound.
func (w *workspace) adminRun(ctx context.Context, argv ...string) ([]byte, error) {
	res, err := w.adminExec(ctx, argv...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("exit %d: %s", res.ExitCode, res.Stderr)
	}
	return res.Stdout, nil
}

func (w *workspace) execIn(ctx context.Context, containerID string, req types.ExecRequest) (types.ExecResult, error) {
	start := time.Now()
	timeout := req.Timeout
	// Administration against the anchor is bounded by adminExec's
	// fresh ten-second maximum, never by the workload's execution
	// policy.
	if timeout == 0 && containerID != w.anchorID {
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
	token := fmt.Sprintf("%s-%d", containerID[:12], time.Now().UnixNano())
	// The anchor receives only the exec token: workload environment
	// values, including resolved secrets, never reach administration.
	env := []string{execTokenEnv + "=" + token}
	if containerID != w.anchorID {
		env = append(w.envList(), execTokenEnv+"="+token)
	}
	created, err := w.api.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd:          req.Argv,
		WorkingDir:   req.Cwd,
		Env:          env,
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

// Close removes the workload container, its trusted anchor and the
// private network through the same group cleanup CloseByRef uses:
// missing members are idempotent and cleanup failures surface as
// errors.
func (w *workspace) Close(ctx context.Context) error {
	var errs []error
	if err := removeContainer(ctx, w.api, w.containerID, true); err != nil {
		errs = append(errs, err)
	}
	if err := removeContainer(ctx, w.api, w.anchorID, true); err != nil {
		errs = append(errs, err)
	}
	if err := removeNetwork(ctx, w.api, w.netID); err != nil {
		errs = append(errs, err)
	}
	for _, id := range []string{w.containerID, w.anchorID} {
		if id == "" {
			continue
		}
		_, err := w.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		switch {
		case err == nil:
			errs = append(errs, fmt.Errorf("docker: close: container %s still present", id))
		case isNotFound(err):
		default:
			errs = append(errs, fmt.Errorf("docker: close: inspect %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}
