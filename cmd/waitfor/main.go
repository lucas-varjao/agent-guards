// Command waitfor polls a probe command until a condition holds, failing fast
// when the probe itself is broken instead of waiting for the timeout.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lucas-varjao/agent-guards/internal/wait"
)

const maxTimeout = 60 * time.Minute

const usage = `usage: waitfor --timeout DUR [flags] -- PROBE [ARGS...]
       waitfor --timeout DUR [flags] -c 'SHELL COMMAND'

Runs PROBE every --every until it reports success. Each probe is classified:

  done     exit 0, and output matches --until (when given)
  failed   output matches --fail                      -> exit 1 at once
  pending  output matches --pending, exit code is in --pending-exit,
           or (with --until) exit 0 with non-empty output
  error    anything else: non-zero exit, empty output, probe timeout
  broken   command not found / not executable         -> exit 3 at once

--max-errors consecutive errors abort with exit 3: a probe that keeps erroring
is broken (typo, wrong jsonpath, missing auth), not "still waiting". Without
--until, a non-zero exit is only "not yet" if you declare it with --pending or
--pending-exit.

Exit codes: 0 done, 1 failed, 2 timeout, 3 probe broken, 4 stale, 64 usage.

Examples:
  waitfor --timeout 5m --until '^Succeeded$' --fail 'Failed' -- \
    kubectl --context default get pod job-x -o jsonpath='{.status.phase}'
  waitfor --timeout 2m --every 3s --pending-exit 7 -- curl -fsS http://127.0.0.1:8080/healthz
  waitfor --timeout 20m --every 30s --until '"status":"success"' --fail '"status":"(failed|canceled)"' \
    -c 'glab api projects/:id/pipelines/123 | jq -c "{status}"'

Flags:
`

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("waitfor", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}
	var (
		c                        wait.Config
		until, fail, pending, sh string
		pendingExit              string
	)
	fs.DurationVar(&c.Timeout, "timeout", 0, "overall limit (required, max 60m); keep it close to the expected duration")
	fs.DurationVar(&c.Every, "every", 10*time.Second, "interval between probes")
	fs.DurationVar(&c.ProbeTimeout, "probe-timeout", 30*time.Second, "limit for a single probe run (counts as an error)")
	fs.DurationVar(&c.Stale, "stale", 0, "abort with exit 4 if pending output stays unchanged this long (0 = off)")
	fs.IntVar(&c.MaxErrors, "max-errors", 3, "consecutive probe errors before aborting as broken")
	fs.StringVar(&until, "until", "", "regex the output must match (with exit 0) to succeed")
	fs.StringVar(&fail, "fail", "", "regex marking a terminal failure state")
	fs.StringVar(&pending, "pending", "", "regex marking output that means \"not yet\" (any exit code)")
	fs.StringVar(&pendingExit, "pending-exit", "", "comma-separated exit codes that mean \"not yet\"")
	fs.BoolVar(&c.AllowEmpty, "allow-empty", false, "with --until, treat empty output as pending instead of an error")
	fs.StringVar(&sh, "c", "", "probe as a shell command string, run with $SHELL -c")

	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 64
	}
	if err := configure(&c, fs.Args(), until, fail, pending, pendingExit, sh); err != nil {
		fmt.Fprintf(os.Stderr, "waitfor: %v (see waitfor --help)\n", err)
		return 64
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return wait.Run(ctx, &c, os.Stderr)
}

func configure(c *wait.Config, args []string, until, fail, pending, pendingExit, sh string) error {
	switch {
	case c.Timeout <= 0:
		return fmt.Errorf("--timeout is required")
	case c.Timeout > maxTimeout:
		return fmt.Errorf("--timeout %s exceeds %s; wait in smaller steps or use a native notification", c.Timeout, maxTimeout)
	case c.Every < time.Second:
		return fmt.Errorf("--every must be at least 1s")
	case c.Every > c.Timeout:
		return fmt.Errorf("--every %s is longer than --timeout %s", c.Every, c.Timeout)
	case c.ProbeTimeout <= 0 || c.ProbeTimeout > c.Timeout:
		return fmt.Errorf("--probe-timeout must be in (0, --timeout]")
	case c.MaxErrors < 1:
		return fmt.Errorf("--max-errors must be at least 1")
	case (sh == "") == (len(args) == 0):
		return fmt.Errorf("give exactly one probe: -c 'command' or -- argv")
	}

	var err error
	for _, re := range []struct {
		dst  **regexp.Regexp
		src  string
		name string
	}{{&c.Until, until, "--until"}, {&c.Fail, fail, "--fail"}, {&c.Pending, pending, "--pending"}} {
		if re.src == "" {
			continue
		}
		if *re.dst, err = regexp.Compile(re.src); err != nil {
			return fmt.Errorf("%s: %v", re.name, err)
		}
	}

	c.PendingExit = map[int]bool{}
	for f := range strings.SplitSeq(pendingExit, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		code, err := strconv.Atoi(f)
		if err != nil || code == 0 {
			return fmt.Errorf("--pending-exit: invalid exit code %q", f)
		}
		c.PendingExit[code] = true
	}

	if sh != "" {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		args = []string{shell, "-c", sh}
	}
	c.Probe = wait.CommandProber(args)
	return nil
}
