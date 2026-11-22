package main

// T3: which projects it has (through tr3x, which sees every machine Rex
// follows), and adding or removing one (through the t3 CLI, on the host
// whose folder it is).

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type T3Project struct {
	ID       string `json:"id"` // "env/project"
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Detail   string `json:"detail"` // "2h ago"
	Path     string `json:"-"`      // as tr3x prints it: "~/Projects/x"
	Machine  string `json:"-"`
	Order    int    `json:"-"` // tr3x's order: worked in last first
}

func (p T3Project) projectID() string {
	_, id, _ := strings.Cut(p.ID, "/")
	return id
}

type T3 struct {
	tr3x string // "" when there is no tr3x
	t3   string // the CLI on this machine
	me   string // what tr3x calls this machine
	list []T3Project
	err  error
	read bool
}

func findT3() *T3 {
	t := &T3{tr3x: findTool("T3P_TR3X", "tr3x", []string{"~/.local/bin/tr3x"})}
	t.t3 = findTool("T3P_T3", "t3", []string{"/opt/homebrew/bin/t3", "/usr/local/bin/t3",
		"~/.local/bin/t3", "~/.t3/bin/t3", "%APPDATA%/npm/t3.cmd"})
	return t
}

// findTool is $env, else the first candidate that exists, else name on the
// PATH, else "".
func findTool(envName, name string, candidates []string) string {
	if p := os.Getenv(envName); p != "" {
		return p
	}
	for _, c := range candidates {
		c = strings.Replace(c, "%APPDATA%", os.Getenv("APPDATA"), 1)
		c = localHost{}.Expand(c)
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// Projects is every project on every machine, read once.
func (t *T3) Projects() ([]T3Project, error) {
	if t.read {
		return t.list, t.err
	}
	t.read = true
	if t.tr3x == "" {
		t.err = fmt.Errorf("no tr3x here, so t3p can't list T3's projects")
		return nil, t.err
	}
	out, err := runProcess(30*time.Second, t.tr3x, "projects")
	if err != nil {
		t.err = fmt.Errorf("tr3x projects: %w", err)
		return nil, t.err
	}
	if err := json.Unmarshal([]byte(out), &t.list); err != nil {
		t.err = fmt.Errorf("tr3x projects: %w", err)
		return nil, t.err
	}
	for i := range t.list {
		p := &t.list[i]
		p.Order = i
		p.Path, p.Machine = p.Subtitle, ""
		if a, b, ok := cutLast(p.Subtitle, " · "); ok {
			p.Path, p.Machine = a, b
		}
	}
	return t.list, nil
}

// Me is what T3 calls this machine: the row of `tr3x envs` that says "this".
func (t *T3) Me() string {
	if t.me != "" || t.tr3x == "" {
		return t.me
	}
	out, _ := runProcess(15*time.Second, t.tr3x, "envs")
	re := regexp.MustCompile(`^(.+?)\s{2,}this `)
	for _, line := range strings.Split(out, "\n") {
		if m := re.FindStringSubmatch(line); m != nil {
			t.me = strings.TrimSpace(m[1])
			break
		}
	}
	return t.me
}

// t3argv is how to run the t3 CLI on h.
func (t *T3) t3argv(h Host, args ...string) []string {
	if _, ok := h.(localHost); ok {
		bin := t.t3
		if bin == "" {
			bin = "t3"
		}
		return append([]string{bin}, args...)
	}
	return append([]string{"t3"}, args...) // the distro's login PATH finds it
}

var addedRe = regexp.MustCompile(`Added project (\S+)`)

// Add makes folder a T3 project on h, tracking the extra folders, and returns
// its project id. A folder that already is one returns its id too.
func (t *T3) Add(h Host, machine, folder string, track []string) (string, error) {
	args := []string{"project", "add"}
	for _, tr := range track {
		args = append(args, "--track", tr)
	}
	args = append(args, folder)
	out, err := h.Run(60*time.Second, t.t3argv(h, args...)...)
	if m := addedRe.FindStringSubmatch(out); m != nil {
		t.read = false // the list has changed
		return strings.TrimSuffix(m[1], "."), nil
	}
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return "", fmt.Errorf("t3 project add: %s", cleanNodeError(err.Error()))
	}
	return t.IDFor(h, machine, folder)
}

// IDFor finds the T3 project whose folder is exactly folder. tr3x knows the
// paths; without it, a thread in the folder names its project, though a
// folder inside another project's would name that one, so the path must match.
// Paths match only on the same machine: another may have its own ~/Projects/x.
func (t *T3) IDFor(h Host, machine, folder string) (string, error) {
	if list, err := t.Projects(); err == nil {
		for _, p := range list {
			if machine != "" && p.Machine == machine && h.Expand(p.Path) == folder {
				return p.projectID(), nil
			}
		}
	}
	out, err := h.Run(30*time.Second, t.t3argv(h, "thread", "list", "--project", folder, "--json")...)
	if err == nil {
		var threads []struct {
			ProjectID string `json:"projectId"`
		}
		if json.Unmarshal([]byte(out), &threads) == nil && len(threads) > 0 && t.tr3x == "" {
			return threads[0].ProjectID, nil
		}
	}
	return "", fmt.Errorf("T3 has %s, but t3p can't find its id yet; try again in a moment", h.Display(folder))
}

// Remove takes a project off T3's list. Never --force: T3 refuses when
// that would lose something, and that refusal is shown.
func (t *T3) Remove(h Host, projectID string) error {
	_, err := h.Run(60*time.Second, t.t3argv(h, "project", "remove", projectID)...)
	if err != nil {
		return fmt.Errorf("t3 project remove: %s", cleanNodeError(err.Error()))
	}
	t.read = false
	return nil
}

// cleanNodeError keeps the message of a line like
// "[12:28:28.588] ERROR (#2): ProjectAlreadyExistsError: An active project …".
func cleanNodeError(s string) string {
	s = regexp.MustCompile(`^\[[^\]]*\]\s*`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`^ERROR\s*\(#\d+\):\s*`).ReplaceAllString(s, "")
	return regexp.MustCompile(`^\w+Error:\s*`).ReplaceAllString(s, "")
}

func cutLast(s, sep string) (string, string, bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// t3Ago reads tr3x's "2h ago" back into a time, zero when it can't.
func t3Ago(s string, now time.Time) time.Time {
	s = strings.TrimSpace(s)
	if s == "just now" {
		return now
	}
	m := regexp.MustCompile(`^(\d+)\s*(s|m|h|d|w|mo|y)\s+ago$`).FindStringSubmatch(s)
	if m == nil {
		return time.Time{}
	}
	var n time.Duration
	fmt.Sscan(m[1], &n)
	unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour,
		"d": 24 * time.Hour, "w": 7 * 24 * time.Hour, "mo": 30 * 24 * time.Hour, "y": 365 * 24 * time.Hour}[m[2]]
	return now.Add(-n * unit)
}

// exeName is name with .exe on Windows.
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
