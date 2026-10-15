package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed skill/atlassian/SKILL.md
var skillMD []byte

//go:embed skill/agents-section.md
var agentsSection string

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
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fail(exitConfig, "cannot find the home directory: %v", err)
	}
	return filepath.Join(home, ".config", "opencode"), nil
}

func defaultBinDir() (string, error) {
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "Programs", "atl"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fail(exitConfig, "cannot find the home directory: %v", err)
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

func installAgents(dir string, force bool) (string, string, error) {
	path := filepath.Join(dir, "AGENTS.md")
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return path, "", err
	}
	doc, err := upsertBlock(string(old), agentsSection, force)
	if err != nil {
		return path, "", fail(exitConfig, "%s: %v; fix it by hand or rerun with --force", path, err)
	}
	status, err := writeIfChanged(path, []byte(doc), 0o644, false)
	return path, status, err
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
			fmt.Fprintf(w, "  %-7s %s\n", s.what, s.status)
			continue
		}
		fmt.Fprintf(w, "  %-7s %s (%s)\n", s.what, s.path, s.status)
	}
}

func cmdInstall(args []string, stdout, stderr io.Writer) error {
	fl := newFlags("install", "", "Install atl for opencode: copy this binary to --bin-dir, write the atlassian\nskill, and add a short section to opencode's AGENTS.md. Safe to rerun.", stderr)
	binDefault, err := defaultBinDir()
	if err != nil {
		return err
	}
	binDir := fl.String("bin-dir", binDefault, "where to copy the binary")
	noBinary := fl.Bool("no-binary", false, "do not copy the binary")
	noAgents := fl.Bool("no-agents", false, "do not touch AGENTS.md")
	force := fl.Bool("force", false, "replace a damaged atl block in AGENTS.md: from the first atl marker to the last end marker, or to the end of the file")
	pos, err := parseFlags(fl, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fail(exitUsage, "install takes no arguments")
	}
	dir, err := opencodeDir()
	if err != nil {
		return err
	}

	var steps []step
	if *noBinary {
		steps = append(steps, step{"binary", "", "skipped (--no-binary)"})
	} else {
		path, status, binErr := installBinary(*binDir)
		if binErr != nil {
			return fail(exitAPI, "installing the binary to %s: %v", path, binErr)
		}
		steps = append(steps, step{"binary", path, status})
	}

	skillPath := filepath.Join(dir, "skills", "atlassian", "SKILL.md")
	status, err := writeIfChanged(skillPath, skillMD, 0o644, false)
	if err != nil {
		return fail(exitAPI, "writing the skill: %v", err)
	}
	steps = append(steps, step{"skill", skillPath, status})

	if *noAgents {
		steps = append(steps, step{"agents", "", "skipped (--no-agents)"})
	} else {
		path, status, err := installAgents(dir, *force)
		if err != nil {
			if _, ok := errors.AsType[*atlError](err); ok {
				return err
			}
			return fail(exitAPI, "writing %s: %v", path, err)
		}
		steps = append(steps, step{"agents", path, status})
	}

	writeSteps(stdout, "Installed atl "+version+":", steps)
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
  3. Restart opencode so it loads the skill.
`, credentialVars("jira"), credentialVars("confluence"), configPath())
	return nil
}

func cmdUninstall(args []string, stdout, stderr io.Writer) error {
	fl := newFlags("uninstall", "", "Remove what `atl install` added: the binary in --bin-dir, the atlassian skill,\nand the atl section of opencode's AGENTS.md. The config file is left alone.", stderr)
	binDefault, err := defaultBinDir()
	if err != nil {
		return err
	}
	binDir := fl.String("bin-dir", binDefault, "where the binary was installed")
	keepBinary := fl.Bool("keep-binary", false, "leave the binary in place")
	pos, err := parseFlags(fl, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fail(exitUsage, "uninstall takes no arguments")
	}
	dir, err := opencodeDir()
	if err != nil {
		return err
	}

	var steps []step
	skillDir := filepath.Join(dir, "skills", "atlassian")
	steps = append(steps, step{"skill", skillDir, removePath(skillDir)})

	agentsPath := filepath.Join(dir, "AGENTS.md")
	status, err := uninstallAgents(agentsPath)
	if err != nil {
		return err
	}
	steps = append(steps, step{"agents", agentsPath, status})

	if *keepBinary {
		steps = append(steps, step{"binary", "", "kept (--keep-binary)"})
	} else {
		bin := filepath.Join(*binDir, binaryName())
		status := removePath(bin)
		if strings.HasPrefix(status, "error") {
			return fail(exitAPI, "removing %s: %s", bin, status)
		}
		steps = append(steps, step{"binary", bin, status})
	}
	writeSteps(stdout, "Uninstalled atl:", steps)
	fmt.Fprintf(stdout, "\nLeft in place: %s, and any JIRA_*/CONFLUENCE_* variables. Restart opencode.\n", configPath())
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

func uninstallAgents(path string) (string, error) {
	old, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", fail(exitAPI, "reading %s: %v", path, err)
	}
	doc, changed, err := removeBlock(string(old))
	if err != nil {
		return "", fail(exitConfig, "%s: %v; remove the atl section by hand", path, err)
	}
	if !changed {
		return "no atl section", nil
	}
	if strings.TrimSpace(doc) == "" {
		if err := os.Remove(path); err != nil {
			return "", fail(exitAPI, "removing %s: %v", path, err)
		}
		return "removed (it held only the atl section)", nil
	}
	if _, err := writeIfChanged(path, []byte(doc), 0o644, false); err != nil {
		return "", fail(exitAPI, "writing %s: %v", path, err)
	}
	return "section removed", nil
}
