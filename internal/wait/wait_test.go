package wait

import (
	"context"
	"errors"
	"io"
	"regexp"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	re := regexp.MustCompile
	cases := []struct {
		name string
		cfg  Config
		res  Result
		want Verdict
	}{
		{"exit 0 no until", Config{}, Result{Exit: 0}, Done},
		{"nonzero undeclared", Config{}, Result{Exit: 1}, Error},
		{"pending exit", Config{PendingExit: map[int]bool{7: true}}, Result{Exit: 7}, Pending},
		{"pending regex", Config{Pending: re("refused")}, Result{Exit: 1, Output: "Connection refused"}, Pending},
		{"until matched", Config{Until: re("^Running$")}, Result{Output: "Running\n"}, Done},
		{"until not yet", Config{Until: re("^Running$")}, Result{Output: "Pending"}, Pending},
		{"until needs exit 0", Config{Until: re("Running")}, Result{Exit: 1, Output: "Running"}, Error},
		{"empty output is broken", Config{Until: re("Running")}, Result{Output: "  \n"}, Error},
		{"empty allowed", Config{Until: re("Running"), AllowEmpty: true}, Result{}, Pending},
		{"fail wins", Config{Until: re("."), Fail: re("Failed")}, Result{Output: "Failed"}, Failed},
		{"not found", Config{}, Result{Exit: 127}, Fatal},
		{"start error", Config{}, Result{Exit: 127, StartErr: errors.New("no such file")}, Fatal},
		{"probe timeout", Config{PendingExit: map[int]bool{-1: true}}, Result{Exit: -1, TimedOut: true}, Error},
	}
	for _, c := range cases {
		if got, why := Classify(&c.cfg, c.res); got != c.want {
			t.Errorf("%s: got %s (%s), want %s", c.name, got, why, c.want)
		}
	}
}

func seq(results ...Result) Prober {
	i := 0
	return func(context.Context, time.Duration) Result {
		r := results[min(i, len(results)-1)]
		i++
		return r
	}
}

func TestRun(t *testing.T) {
	base := Config{Timeout: time.Second, Every: time.Millisecond, ProbeTimeout: time.Second, MaxErrors: 3,
		Until: regexp.MustCompile("^ok$")}
	cases := []struct {
		name  string
		probe Prober
		stale time.Duration
		want  int
	}{
		{"done after pending", seq(Result{Output: "wait"}, Result{Output: "ok"}), 0, ExitDone},
		{"broken probe aborts fast", seq(Result{Exit: 1, Output: "error: the server doesn't have a resource type"}), 0, ExitBroken},
		{"errors reset by pending", seq(Result{Exit: 1}, Result{Exit: 1}, Result{Output: "wait"},
			Result{Exit: 1}, Result{Exit: 1}, Result{Output: "ok"}), 0, ExitDone},
		{"timeout", seq(Result{Output: "wait"}), 0, ExitTimeout},
		{"stale", seq(Result{Output: "wait"}), 20 * time.Millisecond, ExitStale},
	}
	for _, c := range cases {
		cfg := base
		cfg.Probe, cfg.Stale = c.probe, c.stale
		start := time.Now()
		if got := Run(context.Background(), &cfg, io.Discard); got != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, got, c.want)
		}
		if c.want == ExitBroken && time.Since(start) > 100*time.Millisecond {
			t.Errorf("%s: took %s, should abort without waiting for the timeout", c.name, time.Since(start))
		}
	}
}

func TestCommandProber(t *testing.T) {
	r := CommandProber([]string{"sh", "-c", "echo hi; exit 7"})(context.Background(), time.Second)
	if r.Exit != 7 || r.Output != "hi\n" {
		t.Errorf("got %+v", r)
	}
	r = CommandProber([]string{"sleep", "5"})(context.Background(), 50*time.Millisecond)
	if !r.TimedOut {
		t.Errorf("expected timeout, got %+v", r)
	}
	r = CommandProber([]string{"definitely-not-a-command-xyz"})(context.Background(), time.Second)
	if v, _ := Classify(&Config{}, r); v != Fatal {
		t.Errorf("missing command: got %s", v)
	}
}
