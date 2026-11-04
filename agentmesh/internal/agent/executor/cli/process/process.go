package process

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
)

const MaxOutputBytes = 8 << 20

var ErrOutputLimit = errors.New("coding agent output exceeded limit")

type cappedBuffer struct {
	mu       sync.Mutex
	b        bytes.Buffer
	limit    int
	exceeded bool
	onLimit  func()
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remain := b.limit - b.b.Len()
	if remain > 0 {
		if remain > n {
			remain = n
		}
		_, _ = b.b.Write(p[:remain])
	}
	if remain < n && !b.exceeded {
		b.exceeded = true
		if b.onLimit != nil {
			b.onLimit()
		}
	}
	return n, nil
}
func (b *cappedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }
func (b *cappedBuffer) Exceeded() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.exceeded }

type Spec struct {
	Path string
	Args []string
	Dir  string
	Env  []string
}
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}
type Runner struct{}

func (Runner) Run(ctx context.Context, s Spec) (Output, error) {
	cmd := exec.CommandContext(ctx, s.Path, s.Args...)
	configureProcess(cmd)
	cmd.Dir = s.Dir
	if len(s.Env) > 0 {
		cmd.Env = append(cmd.Environ(), s.Env...)
	}
	stdout := &cappedBuffer{limit: MaxOutputBytes}
	stderr := &cappedBuffer{limit: MaxOutputBytes}
	kill := func() { _ = killProcess(cmd) }
	stdout.onLimit = kill
	stderr.onLimit = kill
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	out := Output{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}
	if stdout.Exceeded() || stderr.Exceeded() {
		return out, ErrOutputLimit
	}
	if err != nil {
		return out, err
	}
	return out, nil
}
