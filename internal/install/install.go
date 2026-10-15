// Package install sets atl up for coding agents (Claude Code, Codex,
// opencode): the binary, the skill and a short instructions section.
package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/RyabovNick/atl/internal/atlassian"
	"github.com/RyabovNick/atl/internal/cli"
	"github.com/RyabovNick/atl/skill"
)

const (
	agentsBegin = "<!-- atl:begin -->"
	agentsEnd   = "<!-- atl:end -->"
)

// executable is replaced in tests, where os.Executable is the test binary.
var executable = os.Executable

func opencodeDir() (string, error) {
	if d := os.Getenv("OPENCODE_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "opencode"), nil
	}
	// opencode uses ~/.config on macOS and Windows too, not their native config dirs.
	return inHome("", ".config", "opencode")()
}

func defaultBinDir() (string, error) {
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "Programs", "atl"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", cli.Fail(cli.ExitConfig, "cannot find the home directory: %v", err)
	}
	return filepath.Join(home, ".local", "bin"), nil
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "atl.exe"
	}
	return "atl"
}

// atomicWrite replaces path in one rename, so a reader never sees a half-written
// file and an interrupted install leaves the old one in place.
func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// writeIfChanged reports created, updated or unchanged. An existing file keeps
// its permissions unless mode is forced.
func writeIfChanged(path string, data []byte, mode fs.FileMode, forceMode bool) (string, error) {
	old, err := os.ReadFile(path)
	status := "updated"
	switch {
	case err == nil && bytes.Equal(old, data):
		return "unchanged", nil
	case errors.Is(err, fs.ErrNotExist):
		status = "created"
	case err != nil:
		return "", err
	}
	if status == "updated" && !forceMode {
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	}
	if err := atomicWrite(path, data, mode); err != nil {
		return "", err
	}
	return status, nil
}

func newline(doc string) string {
	if strings.Contains(doc, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

// findBlock locates the atl block; any other marker arrangement is an error,
// because guessing which text belongs to atl could delete the user's own.
func findBlock(doc string) (start, end int, found bool, err error) {
	nb, ne := strings.Count(doc, agentsBegin), strings.Count(doc, agentsEnd)
	if nb == 0 && ne == 0 {
		return 0, 0, false, nil
	}
	b, e := strings.Index(doc, agentsBegin), strings.Index(doc, agentsEnd)
	if nb == 1 && ne == 1 && b < e {
		return b, e + len(agentsEnd), true, nil
	}
	return 0, 0, false, fmt.Errorf("found %d %q and %d %q markers; expected one of each, in order", nb, agentsBegin, ne, agentsEnd)
}

// forcedSpan is what --force replaces in a damaged file: from the first marker
// to the last end marker, or to the end of the file when none follows.
func forcedSpan(doc string) (start, end int) {
	start = strings.Index(doc, agentsBegin)
	if e := strings.Index(doc, agentsEnd); start < 0 || (e >= 0 && e < start) {
		start = e
	}
	end = len(doc)
	if e := strings.LastIndex(doc, agentsEnd); e > start {
		end = e + len(agentsEnd)
	}
	return start, end
}

func upsertBlock(doc, body string, force bool) (string, error) {
	nl := newline(doc)
	body = strings.TrimRight(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	block := agentsBegin + nl + strings.ReplaceAll(body, "\n", nl) + nl + agentsEnd

	start, end, found, err := findBlock(doc)
	if err != nil {
		if !force {
			return "", err
		}
		start, end = forcedSpan(doc)
		found = true
	}
	if found {
		rest := doc[end:]
		if rest == "" {
			rest = nl
		}
		return doc[:start] + block + rest, nil
	}
	if doc == "" {
		return block + nl, nil
	}
	if !strings.HasSuffix(doc, nl) {
		doc += nl
	}
	if !strings.HasSuffix(doc, nl+nl) {
		doc += nl
	}
	return doc + block + nl, nil
}

// removeBlock undoes upsertBlock, including the blank line it added before the block.
func removeBlock(doc string) (string, bool, error) {
	start, end, found, err := findBlock(doc)
	if err != nil || !found {
		return doc, false, err
	}
	nl := newline(doc)
	before := doc[:start]
	after := strings.TrimPrefix(doc[end:], nl)
	if strings.HasSuffix(before, nl+nl) {
		before = before[:len(before)-len(nl)]
	}
	return before + after, true, nil
}

func installBinary(binDir string) (string, string, error) {
	dst := filepath.Join(binDir, binaryName())
	src, err := executable()
	if err == nil {
		src, err = filepath.EvalSymlinks(src)
	}
	if err != nil {
		return dst, "", fmt.Errorf("cannot find the running binary: %w", err)
	}
	if si, srcErr := os.Stat(src); srcErr == nil {
		if di, dstErr := os.Stat(dst); dstErr == nil && os.SameFile(si, di) {
			return dst, "unchanged", nil
		}
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return dst, "", err
	}
	status, err := writeIfChanged(dst, data, 0o755, true)
	return dst, status, err
}

// installInstructions adds or updates the atl section in an agent's instructions file.
func installInstructions(path string, force bool) (string, error) {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	doc, err := upsertBlock(string(old), skill.Instructions, force)
	if err != nil {
		return "", cli.Fail(cli.ExitConfig, "%s: %v; fix it by hand or rerun with --force", path, err)
	}
	return writeIfChanged(path, []byte(doc), 0o644, false)
}

// legacySkillDir is where atl used to put the opencode skill. opencode now
// reads the copy in ~/.agents/skills, and two copies would clash.
func legacySkillDir() (string, error) {
	d, err := opencodeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "skills", "atlassian"), nil
}

func agentNames(as []agent) string {
	names := make([]string, len(as))
	for i, a := range as {
		names[i] = a.name
	}
	return strings.Join(names, ", ")
}

func onPath(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == "" {
			continue
		}
		a, b := filepath.Clean(p), filepath.Clean(dir)
		if a == b || (runtime.GOOS == "windows" && strings.EqualFold(a, b)) {
			return true
		}
	}
	return false
}

func pathHint(dir string) string {
	if runtime.GOOS == "windows" {
		return "add " + dir + " to your user PATH (Settings → System → About → Advanced system settings → Environment Variables)"
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dir, home+string(filepath.Separator)) {
		dir = "$HOME" + dir[len(home):]
	}
	return fmt.Sprintf("add to your shell profile:  export PATH=\"%s:$PATH\"", dir)
}

type step struct{ what, path, status string }

func writeSteps(w io.Writer, title string, steps []step) {
	fmt.Fprintf(w, "%s\n", title)
	for _, s := range steps {
		if s.path == "" {
			fmt.Fprintf(w, "  %-12s %s\n", s.what, s.status)
			continue
		}
		fmt.Fprintf(w, "  %-12s %s (%s)\n", s.what, s.path, s.status)
	}
}

// Run implements `atl install`.
func Run(args []string, stdout, stderr io.Writer) error {
	fl := cli.NewFlags("install", "", "Install atl for coding agents: copy this binary to --bin-dir, write the atlassian\n"+
		"skill, and add a short section to each agent's global instructions file. Safe to rerun.\n"+
		"Without --agents it asks, starting from the agents found on this machine.", stderr)
	binDefault, err := defaultBinDir()
	if err != nil {
		return err
	}
	binDir := fl.String("bin-dir", binDefault, "where to copy the binary")
	agentList := fl.String("agents", "", "agents to set up without asking: "+agentIDs()+", or all")
	noBinary := fl.Bool("no-binary", false, "do not copy the binary")
	noInstructions := fl.Bool("no-instructions", false, "do not touch CLAUDE.md or AGENTS.md")
	force := fl.Bool("force", false, "replace a damaged atl section in an instructions file: from the first atl marker to the last end marker, or to the end of the file")
	pos, err := cli.ParseFlags(fl, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return cli.Fail(cli.ExitUsage, "install takes no arguments")
	}
	var chosen []agent
	if *agentList != "" {
		chosen, err = parseAgents(*agentList)
	} else {
		chosen, err = chooseAgents()
	}
	if err != nil {
		return err
	}

	var steps []step
	if *noBinary {
		steps = append(steps, step{"binary", "", "skipped (--no-binary)"})
	} else {
		path, status, binErr := installBinary(*binDir)
		if binErr != nil {
			return cli.Fail(cli.ExitAPI, "installing the binary to %s: %v", path, binErr)
		}
		steps = append(steps, step{"binary", path, status})
	}

	written := map[string]bool{}
	for _, a := range chosen {
		dir, err := a.skillsDir()
		if err != nil {
			return err
		}
		path := filepath.Join(dir, "atlassian", "SKILL.md")
		if written[path] {
			continue
		}
		written[path] = true
		status, err := writeIfChanged(path, skill.Skill, 0o644, false)
		if err != nil {
			return cli.Fail(cli.ExitAPI, "writing the skill: %v", err)
		}
		steps = append(steps, step{"skill", path, status})
	}
	if slices.ContainsFunc(chosen, func(a agent) bool { return a.id == "opencode" }) {
		legacy, err := legacySkillDir()
		if err != nil {
			return err
		}
		if removePath(legacy) == "removed" {
			steps = append(steps, step{"old skill", legacy, "removed"})
		}
	}

	for _, a := range chosen {
		if *noInstructions {
			steps = append(steps, step{a.name, "", "instructions skipped (--no-instructions)"})
			continue
		}
		path, err := a.instructions()
		if err != nil {
			return err
		}
		status, err := installInstructions(path, *force)
		if err != nil {
			if _, ok := errors.AsType[*cli.Error](err); ok {
				return err
			}
			return cli.Fail(cli.ExitAPI, "writing %s: %v", path, err)
		}
		steps = append(steps, step{a.name, path, status})
	}

	writeSteps(stdout, "Installed atl "+cli.Version+":", steps)
	if !*noBinary && !onPath(*binDir) {
		fmt.Fprintf(stdout, "\n! %s is not on PATH; %s\n", *binDir, pathHint(*binDir))
	}
	fmt.Fprintf(stdout, `
Next steps:
  1. Set %s and %s in your shell profile,
     or put them in %s (mode 600).
     Confluence needs a personal access token (avatar → Settings → Personal Access Tokens),
     sent as Bearer: leave CONFLUENCE_USERNAME unset. Tokens from API-token apps may be rejected.
  2. Run: atl doctor
  3. Restart %s to load the skill.
`, atlassian.CredentialVars("jira"), atlassian.CredentialVars("confluence"), atlassian.ConfigPath(), agentNames(chosen))
	return nil
}

// RunUninstall implements `atl uninstall`.
func RunUninstall(args []string, stdout, stderr io.Writer) error {
	fl := cli.NewFlags("uninstall", "", "Remove what `atl install` added for every agent: the binary in --bin-dir, the\n"+
		"atlassian skill, and the atl section of each instructions file. The config file is left alone.", stderr)
	binDefault, err := defaultBinDir()
	if err != nil {
		return err
	}
	binDir := fl.String("bin-dir", binDefault, "where the binary was installed")
	keepBinary := fl.Bool("keep-binary", false, "leave the binary in place")
	pos, err := cli.ParseFlags(fl, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return cli.Fail(cli.ExitUsage, "uninstall takes no arguments")
	}

	var steps []step
	legacy, err := legacySkillDir()
	if err != nil {
		return err
	}
	skillDirs := []string{legacy}
	for _, a := range agents {
		dir, err := a.skillsDir()
		if err != nil {
			return err
		}
		skillDirs = append(skillDirs, filepath.Join(dir, "atlassian"))
	}
	seen := map[string]bool{}
	for _, dir := range skillDirs {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if status := removePath(dir); status != "absent" {
			steps = append(steps, step{"skill", dir, status})
		}
	}

	for _, a := range agents {
		path, err := a.instructions()
		if err != nil {
			return err
		}
		status, err := uninstallInstructions(path)
		if err != nil {
			return err
		}
		if status != "absent" && status != "no atl section" {
			steps = append(steps, step{a.name, path, status})
		}
	}

	if *keepBinary {
		steps = append(steps, step{"binary", "", "kept (--keep-binary)"})
	} else {
		bin := filepath.Join(*binDir, binaryName())
		status := removePath(bin)
		if strings.HasPrefix(status, "error") {
			return cli.Fail(cli.ExitAPI, "removing %s: %s", bin, status)
		}
		steps = append(steps, step{"binary", bin, status})
	}
	writeSteps(stdout, "Uninstalled atl:", steps)
	fmt.Fprintf(stdout, "\nLeft in place: %s, and any JIRA_*/CONFLUENCE_* variables. Restart your agents.\n", atlassian.ConfigPath())
	return nil
}

func removePath(path string) string {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return "absent"
	}
	if err := os.RemoveAll(path); err != nil {
		return "error: " + err.Error()
	}
	return "removed"
}

func uninstallInstructions(path string) (string, error) {
	old, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", cli.Fail(cli.ExitAPI, "reading %s: %v", path, err)
	}
	doc, changed, err := removeBlock(string(old))
	if err != nil {
		return "", cli.Fail(cli.ExitConfig, "%s: %v; remove the atl section by hand", path, err)
	}
	if !changed {
		return "no atl section", nil
	}
	if strings.TrimSpace(doc) == "" {
		if err := os.Remove(path); err != nil {
			return "", cli.Fail(cli.ExitAPI, "removing %s: %v", path, err)
		}
		return "removed (it held only the atl section)", nil
	}
	if _, err := writeIfChanged(path, []byte(doc), 0o644, false); err != nil {
		return "", cli.Fail(cli.ExitAPI, "writing %s: %v", path, err)
	}
	return "section removed", nil
}
