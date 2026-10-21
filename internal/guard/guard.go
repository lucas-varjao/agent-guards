// Package guard flags shell commands that agents commonly get wrong on this
// machine: Bash-only syntax under zsh, hand-written wait loops, and unbounded
// streams.
package guard

import (
	"regexp"
	"strconv"
	"strings"
)

// AllowMarker disables all checks for a command (false positives only).
const AllowMarker = "agent-guard: allow"

// Finding is one rule violation.
type Finding struct {
	Rule string
	Fix  string
}

// cmdPos anchors a word at command position: start of input or after a
// separator / control keyword.
const cmdPos = `(?:^|[;&|({\n]|\b(?:then|do|else|elif)\b|\$\()\s*`

type rule struct {
	name string
	re   *regexp.Regexp
	fix  string
}

func mustRule(name, re, fix string) rule {
	return rule{name, regexp.MustCompile(re), fix}
}

// bashisms fail under zsh even with the bash-like options set in ~/.zshenv.
var bashisms = []rule{
	mustRule("bash-only builtin mapfile/readarray", cmdPos+`(?:mapfile|readarray)\b`,
		`zsh: lines=("${(@f)$(cmd)}")`),
	mustRule("read -a (zsh uses -A)", cmdPos+`(?:builtin\s+)?read\b[^;&|\n]*\s-[a-zA-Z]*a`,
		`zsh: read -rA arr`),
	mustRule("bash-only builtin shopt/compgen/complete", cmdPos+`(?:shopt|compgen|complete)\b`,
		`zsh: setopt/unsetopt; no compgen equivalent in scripts`),
	mustRule("nameref (declare -n)", cmdPos+`(?:declare|local|typeset)\s+-[a-zA-Z]*n\b`,
		`zsh: indirect expansion ${(P)name}`),
	mustRule("export -f", cmdPos+`export\s+-f\b`,
		`define the function inside the child shell, or run it with bash -c`),
	mustRule("indirect expansion ${!var}", `\$\{!`,
		`zsh: ${(P)name} for indirection, ${(k)assoc} for keys`),
	mustRule("bash-only variable", `\$\{?(?:BASH_SOURCE|BASH_REMATCH|PIPESTATUS)\b`,
		`zsh: ${(%):-%x} for the script path, $match/$MATCH for regex groups, $pipestatus`),
	mustRule("case modification ${v,,}/${v^^}", `\$\{\w+(?:,,|\^\^)\}`,
		`zsh: ${(L)v} / ${(U)v}`),
	mustRule("index 0 (zsh arrays start at 1)", `\$\{\w+\[0\]\}`,
		`zsh: ${arr[1]} is the first element`),
}

var (
	runsBash = regexp.MustCompile(`(?:^|[\s;&|(])(?:bash|sh)\s+(?:-[a-zA-Z]+\s+)*(?:-[a-zA-Z]*[cs]\b|\S+\.sh\b)|\|\s*(?:bash|sh)\b`)

	// Quotes count as command position so loops inside bash -c '...' are caught.
	waitLoop = regexp.MustCompile(`(?:^|[;&|({\n'"]|\b(?:then|do|else|elif)\b|\$\()\s*` +
		`(?:(?:while|until)\b|for\b[\s\S]*\bdo\b)[\s\S]*\bsleep\b`)
	watchCmd  = regexp.MustCompile(cmdPos + `watch\s`)
	sleepCmd  = regexp.MustCompile(cmdPos + `sleep\s+(\d+(?:\.\d+)?)([smhd]?)\b`)
	heredoc   = regexp.MustCompile(`(?:^|[^<])<<-?\s*['"]?([A-Za-z_]\w*)['"]?`)
	singleQ   = regexp.MustCompile(`'[^']*'`)
	segmentRe = regexp.MustCompile(`\|\||&&|[;|\n&]`)

	streams = []*regexp.Regexp{
		regexp.MustCompile(`^tail\b.*\s-[a-zA-Z]*[fF]\b`),
		regexp.MustCompile(`^tail\b.*\s--follow\b`),
		regexp.MustCompile(`^journalctl\b.*\s(?:-[a-zA-Z]*f\b|--follow\b)`),
		regexp.MustCompile(`^(?:kubectl|podman|docker|oc)\b.*\s(?:logs|events)\b.*\s(?:-[a-zA-Z]*f\b|--follow\b)`),
		regexp.MustCompile(`^kubectl\b.*\s(?:-w\b|--watch\b|--watch-only\b)`),
		regexp.MustCompile(`^kubectl\b.*\srollout\s+status\b`),
		regexp.MustCompile(`^gh\s+run\s+watch\b`),
	}
	boundedStream = regexp.MustCompile(`--timeout[=\s]|--request-timeout`)
	cmdPrefix     = regexp.MustCompile(`^(?:\s*(?:rtk|command|exec|nohup|env|[A-Z_][A-Z0-9_]*=\S*)\s+)*`)
)

const waitFix = "use `waitfor` (see `waitfor --help`), which aborts after a few broken probes instead of " +
	"waiting for the timeout; in Claude Code, run it with Bash run_in_background to get one notification " +
	"when it exits"

// Check returns every rule the command violates.
func Check(command string) []Finding {
	if strings.Contains(command, AllowMarker) {
		return nil
	}
	code := stripHeredocs(command)
	var out []Finding

	if !runsBash.MatchString(code) {
		unquoted := singleQ.ReplaceAllString(code, "''")
		for _, r := range bashisms {
			if r.re.MatchString(unquoted) {
				out = append(out, Finding{r.name, r.fix + "; or run real Bash explicitly with bash -c '...'"})
			}
		}
	}

	if waitLoop.MatchString(code) {
		out = append(out, Finding{"hand-written wait/poll loop", waitFix})
	}
	if watchCmd.MatchString(code) {
		out = append(out, Finding{"`watch` never exits", waitFix})
	}
	for _, m := range sleepCmd.FindAllStringSubmatch(code, -1) {
		if seconds(m[1], m[2]) >= 30 {
			out = append(out, Finding{"blind sleep of " + m[1] + m[2], "wait on a condition, not a guess: " + waitFix})
			break
		}
	}
	for _, seg := range segmentRe.Split(code, -1) {
		seg = strings.TrimSpace(seg)
		if strings.HasPrefix(seg, "timeout ") || boundedStream.MatchString(seg) {
			continue
		}
		seg = cmdPrefix.ReplaceAllString(seg, "")
		for _, re := range streams {
			if re.MatchString(seg) {
				out = append(out, Finding{"unbounded stream/watch: " + firstWords(seg, 4),
					"prefix it with `timeout 60s`, pass --timeout, read a bounded snapshot (e.g. tail -n 200), or " + waitFix})
				break
			}
		}
	}
	return out
}

// Message renders findings as the text shown to the agent.
func Message(fs []Finding) string {
	var b strings.Builder
	b.WriteString("agent-guard blocked this command:\n")
	for _, f := range fs {
		b.WriteString("- " + f.Rule + ". Fix: " + f.Fix + "\n")
	}
	b.WriteString("Rewrite the command. Only if this is a false positive (e.g. the pattern is literal data you are " +
		"searching for), append the comment `# " + AllowMarker + "`.\n")
	return b.String()
}

// stripHeredocs drops heredoc bodies: text written to files or stdin is data,
// not code run by this shell.
func stripHeredocs(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	var delims []string
	for _, l := range lines {
		if len(delims) > 0 {
			if strings.TrimSpace(l) == delims[0] {
				delims = delims[1:]
			}
			continue
		}
		out = append(out, l)
		for _, m := range heredoc.FindAllStringSubmatch(l, -1) {
			delims = append(delims, m[1])
		}
	}
	return strings.Join(out, "\n")
}

func seconds(n, unit string) float64 {
	v, _ := strconv.ParseFloat(n, 64)
	switch unit {
	case "m":
		return v * 60
	case "h":
		return v * 3600
	case "d":
		return v * 86400
	}
	return v
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	return strings.Join(f[:min(n, len(f))], " ")
}
