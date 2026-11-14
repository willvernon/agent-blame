package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"agent-blame/internal/sources/claudecode"
)

// hookEvents are the Claude Code events agent-blame listens to.
var hookEvents = []struct {
	event, matcher string
}{
	{"UserPromptSubmit", ""},
	{"PostToolUse", "Read|Edit|Write|MultiEdit|NotebookEdit|Bash"},
	{"PostToolUseFailure", "Edit|Write|MultiEdit|NotebookEdit|Bash"},
}

const hookMarker = " hook claude-code"

func runInstall(env Env, args []string) error {
	fs := newFlags("install", env)
	uninstall := fs.Bool("uninstall", false, "remove the hooks instead")
	settings := fs.String("settings", "", "settings file to change (default: Claude Code's user settings)")
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: agent-blame install claude-code [--uninstall]")
	}
	if args[0] != "claude-code" {
		return fmt.Errorf("unknown target %q, only claude-code is supported", args[0])
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	path := *settings
	if path == "" {
		path = userSettings()
	}
	cfg, err := readSettings(path)
	if err != nil {
		return err
	}
	hooks, _ := cfg["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	command := hookCommand()
	for _, h := range hookEvents {
		list, _ := hooks[h.event].([]any)
		list = withoutOurs(list)
		if !*uninstall {
			entry := map[string]any{
				"hooks": []any{map[string]any{"type": "command", "command": command, "async": true, "timeout": 10}},
			}
			if h.matcher != "" {
				entry["matcher"] = h.matcher
			}
			list = append(list, entry)
		}
		if len(list) == 0 {
			delete(hooks, h.event)
		} else {
			hooks[h.event] = list
		}
	}
	if len(hooks) == 0 {
		delete(cfg, "hooks")
	} else {
		cfg["hooks"] = hooks
	}
	if err := writeSettings(path, cfg); err != nil {
		return err
	}
	if *uninstall {
		fmt.Fprintf(env.Stdout, "Removed the agent-blame hooks from %s\n", claudecode.ShortenHome(path))
		return nil
	}
	fmt.Fprintf(env.Stdout, "Added hooks to %s that run:\n  %s\n", claudecode.ShortenHome(path), command)
	fmt.Fprintln(env.Stdout, "They run in the background and only record repositories where you have opened agent-blame or run agent-blame sync.")
	return nil
}

func userSettings() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// hookCommand prefers the bare name when the binary on PATH is this one,
// so moving the binary later does not break the hook.
func hookCommand() string {
	self, err := os.Executable()
	if err != nil {
		return "agent-blame" + hookMarker
	}
	if found, err := exec.LookPath("agent-blame"); err == nil {
		a, _ := filepath.EvalSymlinks(found)
		b, _ := filepath.EvalSymlinks(self)
		if a == b {
			return "agent-blame" + hookMarker
		}
	}
	return shellQuote(self) + hookMarker
}

func shellQuote(s string) string {
	if !strings.ContainsAny(s, " '\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func withoutOurs(list []any) []any {
	var out []any
	for _, item := range list {
		entry, _ := item.(map[string]any)
		inner, _ := entry["hooks"].([]any)
		var kept []any
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); strings.HasSuffix(cmd, hookMarker) {
				continue
			}
			kept = append(kept, h)
		}
		if len(kept) == 0 && len(inner) > 0 {
			continue
		}
		if entry != nil && len(inner) > 0 {
			entry["hooks"] = kept
		}
		out = append(out, item)
	}
	return out
}

// HooksInstalled reports whether the settings file runs the agent-blame hook.
func HooksInstalled(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), hookMarker+`"`)
}

func readSettings(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	cfg := map[string]any{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON, so it was left alone: %w", path, err)
	}
	return cfg, nil
}

func writeSettings(path string, cfg map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil {
		backup := path + ".agent-blame.bak"
		if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(backup, old, 0o600); err != nil {
				return err
			}
		}
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
