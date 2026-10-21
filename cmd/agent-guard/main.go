// Command agent-guard is a PreToolUse hook for Claude Code and Codex that
// blocks shell commands agents commonly get wrong on this machine.
//
//	agent-guard hook            read the hook payload from stdin; exit 2 blocks
//	agent-guard check -- CMD    dry-run a command string
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lucas-varjao/agent-guards/internal/guard"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: agent-guard hook | agent-guard check -- 'command'")
		os.Exit(64)
	}
	switch os.Args[1] {
	case "hook":
		os.Exit(hook(os.Stdin))
	case "check":
		args := os.Args[2:]
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		fs := guard.Check(strings.Join(args, " "))
		if len(fs) == 0 {
			fmt.Println("ok")
			return
		}
		fmt.Print(guard.Message(fs))
		os.Exit(2)
	default:
		fmt.Fprintf(os.Stderr, "agent-guard: unknown subcommand %q\n", os.Args[1])
		os.Exit(64)
	}
}

// hook fails open: anything it cannot parse is allowed.
func hook(r io.Reader) int {
	var payload struct {
		ToolInput struct {
			Command json.RawMessage `json:"command"`
		} `json:"tool_input"`
	}
	data, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil || json.Unmarshal(data, &payload) != nil {
		return 0
	}
	cmd := commandText(payload.ToolInput.Command)
	if cmd == "" {
		return 0
	}
	if fs := guard.Check(cmd); len(fs) > 0 {
		fmt.Fprint(os.Stderr, guard.Message(fs))
		return 2
	}
	return 0
}

// commandText accepts a string or an argv array (older Codex shell payloads).
func commandText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var argv []string
	if json.Unmarshal(raw, &argv) == nil {
		return strings.Join(argv, " ")
	}
	return ""
}
