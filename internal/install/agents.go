package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/term"

	"github.com/RyabovNick/atl/internal/cli"
)

// agent is a coding agent atl can set up: it gets the atlassian skill and a
// short section in its global instructions file.
type agent struct {
	id, name string
	// configDir is the agent's own directory, shown in the prompt.
	configDir func() (string, error)
	// skillsDir is the directory the agent loads global skills from.
	skillsDir func() (string, error)
	// instructions is the global instructions file the section goes into.
	instructions func() (string, error)
}

func homeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", cli.Fail(cli.ExitConfig, "cannot find the home directory: %v", err)
	}
	return home, nil
}

// inHome builds a path under the home directory, or under the directory an
// environment variable names instead.
func inHome(env string, parts ...string) func() (string, error) {
	return func() (string, error) {
		if env != "" {
			if d := os.Getenv(env); d != "" {
				return d, nil
			}
		}
		home, err := homeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(append([]string{home}, parts...)...), nil
	}
}

func joined(dir func() (string, error), name string) func() (string, error) {
	return func() (string, error) {
		d, err := dir()
		if err != nil {
			return "", err
		}
		return filepath.Join(d, name), nil
	}
}

var (
	claudeDir = inHome("CLAUDE_CONFIG_DIR", ".claude")
	codexDir  = inHome("CODEX_HOME", ".codex")
	// Codex and opencode both load ~/.agents/skills, so they share one copy.
	sharedSkillsDir = inHome("", ".agents", "skills")
)

var agents = []agent{
	{
		id: "claude", name: "Claude Code",
		configDir:    claudeDir,
		skillsDir:    joined(claudeDir, "skills"),
		instructions: joined(claudeDir, "CLAUDE.md"),
	},
	{
		id: "codex", name: "Codex",
		configDir:    codexDir,
		skillsDir:    sharedSkillsDir,
		instructions: joined(codexDir, "AGENTS.md"),
	},
	{
		id: "opencode", name: "opencode",
		configDir:    opencodeDir,
		skillsDir:    sharedSkillsDir,
		instructions: joined(opencodeDir, "AGENTS.md"),
	},
}

func agentIDs() string {
	ids := make([]string, len(agents))
	for i, a := range agents {
		ids[i] = a.id
	}
	return strings.Join(ids, ",")
}

// parseAgents reads a --agents value: comma-separated ids, or "all".
func parseAgents(list string) ([]agent, error) {
	if strings.TrimSpace(list) == "all" {
		return agents, nil
	}
	want := map[string]bool{}
	for id := range strings.SplitSeq(list, ",") {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	var out []agent
	for _, a := range agents {
		if want[a.id] {
			out = append(out, a)
			delete(want, a.id)
		}
	}
	for id := range want {
		return nil, cli.Fail(cli.ExitUsage, "unknown agent %q; choose from %s, or all", id, agentIDs())
	}
	if len(out) == 0 {
		return nil, cli.Fail(cli.ExitUsage, "--agents is empty; choose from %s, or all", agentIDs())
	}
	return out, nil
}

// defaultAgent is selected when install asks, and set up when it cannot ask.
const defaultAgent = "opencode"

func defaultSelection() []bool {
	selected := make([]bool, len(agents))
	for i, a := range agents {
		selected[i] = a.id == defaultAgent
	}
	return selected
}

var errCanceled = errors.New("canceled")

// tilde shortens a path under the home directory to ~/….
func tilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rel, err := filepath.Rel(home, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Join("~", rel)
	}
	return path
}

// openTerminal returns the terminal to ask on, even when stdin is a pipe as in
// `curl … | sh`. Tests replace it so they never prompt.
var openTerminal = func() (in, out *os.File, closeFn func(), err error) {
	if runtime.GOOS == "windows" {
		if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
			return os.Stdin, os.Stdout, func() {}, nil
		}
		return nil, nil, nil, errors.New("not a terminal")
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	return tty, tty, func() { tty.Close() }, nil
}

// chooseAgents asks which agents to set up, starting from defaultAgent alone.
// Without a terminal it takes defaultAgent.
func chooseAgents() ([]agent, error) {
	selected := defaultSelection()
	in, out, closeFn, err := openTerminal()
	if err != nil {
		return parseAgents(defaultAgent)
	}
	defer closeFn()

	state, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return nil, cli.Fail(cli.ExitAPI, "cannot read the terminal: %v", err)
	}
	selected, err = selectLoop(in, out, selected)
	_ = term.Restore(int(in.Fd()), state)
	fmt.Fprintln(out)
	if errors.Is(err, errCanceled) {
		return nil, cli.Fail(cli.ExitUsage, "install canceled")
	}
	if err != nil {
		return nil, cli.Fail(cli.ExitAPI, "reading the terminal: %v", err)
	}
	var picked []agent
	for i, a := range agents {
		if selected[i] {
			picked = append(picked, a)
		}
	}
	return picked, nil
}

// selectLoop draws a checkbox list and edits selected from raw key presses:
// arrows or j/k move, space toggles, a toggles all, enter confirms, q or Esc cancels.
func selectLoop(r io.Reader, w io.Writer, selected []bool) ([]bool, error) {
	selected = append([]bool(nil), selected...)
	cursor, note := 0, ""
	draw := func(first bool) {
		if !first {
			fmt.Fprintf(w, "\x1b[%dA", len(agents)+2)
		}
		fmt.Fprint(w, "\r\x1b[2KSet up atl for which agents? (↑/↓ move, space select, enter confirm)\r\n")
		for i, a := range agents {
			pointer, box := " ", "[ ]"
			if i == cursor {
				pointer = ">"
			}
			if selected[i] {
				box = "[x]"
			}
			dir, _ := a.configDir()
			fmt.Fprintf(w, "\r\x1b[2K%s %s %-12s %s\r\n", pointer, box, a.name, tilde(dir))
		}
		fmt.Fprintf(w, "\r\x1b[2K%s\r\n", note)
	}
	draw(true)
	buf := make([]byte, 8)
	for {
		n, err := r.Read(buf)
		if err != nil {
			return nil, err
		}
		note = ""
		switch key := string(buf[:n]); key {
		case "\x1b[A", "\x1bOA", "k":
			cursor = (cursor + len(agents) - 1) % len(agents)
		case "\x1b[B", "\x1bOB", "j":
			cursor = (cursor + 1) % len(agents)
		case " ":
			selected[cursor] = !selected[cursor]
		case "a":
			all := true
			for _, s := range selected {
				all = all && s
			}
			for i := range selected {
				selected[i] = !all
			}
		case "\r", "\n":
			for _, s := range selected {
				if s {
					return selected, nil
				}
			}
			note = "Select at least one agent with space."
		case "q", "\x1b", "\x03":
			return nil, errCanceled
		}
		draw(false)
	}
}
