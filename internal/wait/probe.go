package wait

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

const maxOutput = 64 << 10

// CommandProber runs argv once per probe, in its own process group so a
// timed-out probe is killed together with its children.
func CommandProber(argv []string) Prober {
	return func(ctx context.Context, timeout time.Duration) Result {
		pctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		cmd := exec.CommandContext(pctx, argv[0], argv[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 2 * time.Second
		out := &cappedBuffer{}
		cmd.Stdout, cmd.Stderr = out, out

		err := cmd.Run()
		r := Result{Output: out.String(), TimedOut: errors.Is(pctx.Err(), context.DeadlineExceeded)}
		var exitErr *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exitErr):
			r.Exit = exitErr.ExitCode()
			if r.Exit < 0 { // killed by signal
				r.Exit = 128 + int(exitErr.Sys().(syscall.WaitStatus).Signal())
			}
		case r.TimedOut || ctx.Err() != nil:
			r.Exit = -1
		default:
			r.StartErr = err
			r.Exit = 127
		}
		return r
	}
}

type cappedBuffer struct{ bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := maxOutput - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
