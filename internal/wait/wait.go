// Package wait implements the polling loop behind the waitfor command.
//
// The core rule: "not yet" must be recognized positively. A probe that errors,
// prints nothing, or matches nothing known counts as broken, and a few broken
// probes in a row abort the wait instead of burning the whole timeout.
package wait

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Exit codes returned by Run.
const (
	ExitDone     = 0
	ExitFailed   = 1 // probe reported a terminal failure state (--fail)
	ExitTimeout  = 2
	ExitBroken   = 3 // probe looks broken: repeated errors, empty output, not found
	ExitStale    = 4 // output stopped changing for --stale
	ExitCanceled = 130
)

// Config describes one wait.
type Config struct {
	Timeout      time.Duration
	Every        time.Duration
	ProbeTimeout time.Duration
	Stale        time.Duration // 0 disables stale detection
	MaxErrors    int

	Until       *regexp.Regexp // success when exit 0 and output matches
	Fail        *regexp.Regexp // terminal failure when output matches
	Pending     *regexp.Regexp // output that positively means "not yet"
	PendingExit map[int]bool   // exit codes that positively mean "not yet"
	AllowEmpty  bool

	Probe Prober
}

// Prober runs the probe once.
type Prober func(ctx context.Context, timeout time.Duration) Result

// Result is the outcome of one probe run.
type Result struct {
	Exit     int
	Output   string
	TimedOut bool
	StartErr error
}

// Verdict classifies one probe result.
type Verdict int

const (
	Pending Verdict = iota
	Done
	Failed
	Error // counts toward MaxErrors
	Fatal // aborts immediately
)

func (v Verdict) String() string {
	return [...]string{"pending", "done", "FAILED", "probe-error", "probe-broken"}[v]
}

// Classify decides what a single probe result means.
func Classify(c *Config, r Result) (Verdict, string) {
	out := strings.TrimSpace(r.Output)
	switch {
	case r.StartErr != nil:
		return Fatal, "probe could not start: " + r.StartErr.Error()
	case r.Exit == 126 || r.Exit == 127:
		return Fatal, fmt.Sprintf("exit %d: command not found or not executable", r.Exit)
	case r.TimedOut:
		return Error, fmt.Sprintf("probe exceeded --probe-timeout %s", c.ProbeTimeout)
	}
	if c.Fail != nil && c.Fail.MatchString(out) {
		return Failed, "output matched --fail"
	}
	if r.Exit == 0 && (c.Until == nil || c.Until.MatchString(out)) {
		return Done, "condition met"
	}
	if c.Pending != nil && c.Pending.MatchString(out) {
		return Pending, "output matched --pending"
	}
	if c.PendingExit[r.Exit] {
		return Pending, fmt.Sprintf("exit %d is in --pending-exit", r.Exit)
	}
	if c.Until != nil && r.Exit == 0 && (out != "" || c.AllowEmpty) {
		return Pending, "exit 0, --until not matched yet"
	}
	if r.Exit == 0 {
		return Error, "exit 0 with empty output (wrong jsonpath/filter/selector?)"
	}
	return Error, fmt.Sprintf("exit %d", r.Exit)
}

// Run polls until a terminal state and returns one of the Exit* codes.
func Run(ctx context.Context, c *Config, log io.Writer) int {
	start := time.Now()
	deadline := start.Add(c.Timeout)
	var (
		errCount   int
		lastKey    string
		lastChange = start
		last       Result
		lastWhy    string
	)
	for attempt := 1; ; attempt++ {
		r := c.Probe(ctx, c.ProbeTimeout)
		if ctx.Err() != nil {
			fmt.Fprintf(log, "waitfor: canceled after %s\n", since(start))
			return ExitCanceled
		}
		v, why := Classify(c, r)
		last, lastWhy = r, why

		key := v.String() + "\x00" + strings.TrimSpace(r.Output)
		if key != lastKey {
			fmt.Fprintf(log, "[waitfor %s #%d] %s (%s): %s\n", since(start), attempt, v, why, summary(r.Output))
			lastKey, lastChange = key, time.Now()
		}

		switch v {
		case Done:
			return ExitDone
		case Failed:
			dump(log, "terminal failure state", r)
			return ExitFailed
		case Fatal:
			dump(log, "probe is broken: "+why, r)
			return ExitBroken
		case Error:
			errCount++
			if errCount >= c.MaxErrors {
				dump(log, fmt.Sprintf("probe failed %d times in a row (%s); it is probably broken, not pending", errCount, why), r)
				fmt.Fprintln(log, "waitfor: fix the probe, or declare what \"not yet\" looks like with --pending REGEX / --pending-exit CODES")
				return ExitBroken
			}
		default:
			errCount = 0
			if c.Stale > 0 && time.Since(lastChange) >= c.Stale {
				dump(log, fmt.Sprintf("output unchanged for %s (--stale)", c.Stale), r)
				return ExitStale
			}
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			dump(log, fmt.Sprintf("timeout after %s, last state %s (%s)", c.Timeout, v, lastWhy), last)
			return ExitTimeout
		}
		select {
		case <-ctx.Done():
			fmt.Fprintf(log, "waitfor: canceled after %s\n", since(start))
			return ExitCanceled
		case <-time.After(min(c.Every, remaining)):
		}
	}
}

func since(t time.Time) string { return time.Since(t).Round(time.Second).String() }

// summary condenses output to its last few non-empty lines.
func summary(out string) string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return "<no output>"
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	s := strings.Join(lines, " ⏎ ")
	if r := []rune(s); len(r) > 240 {
		s = string(r[:240]) + "…"
	}
	return s
}

// dump prints the reason and the tail of the last probe output.
func dump(log io.Writer, reason string, r Result) {
	fmt.Fprintf(log, "waitfor: %s\n", reason)
	lines := strings.Split(strings.TrimRight(r.Output, "\n"), "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	fmt.Fprintf(log, "--- last probe output (exit %d) ---\n%s\n---\n", r.Exit, strings.Join(lines, "\n"))
}
