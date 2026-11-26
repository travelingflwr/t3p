package main

// The config: the kinds of work this machine has. A kind is what the person
// calls their work product ("project", "case", "engagement"), where its
// folders live, and what's special about it. Nothing about a kind is built in:
// with no config file, there is one kind, "project", under ~/Projects.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Kind struct {
	Name    string   `toml:"name"`    // singular: "case"
	Plural  string   `toml:"plural"`  // "cases"; default Name+"s"
	Host    string   `toml:"host"`    // "" for this machine, "wsl" or "wsl:<distro>"
	Machine string   `toml:"machine"` // what T3 calls that host; default: this machine
	Roots   []string `toml:"roots"`   // folders whose children are this kind
	Detect  string   `toml:"detect"`  // "git" (a repository, the default) or "any" folder
	Match   string   `toml:"match"`   // a regexp a folder's name must match
	// Creating one: `t3p new <kind> <name>` copies Template into the first root.
	Create   bool   `toml:"create"`
	Template string `toml:"template"`
	// Folders outside the place it also works in, given to T3 with --track.
	// {name} is the folder's name, {number} its leading digits.
	Track   []string `toml:"track"`
	Related string   `toml:"related"` // a file in the folder listing related names, whose tracks apply too
	// How often each severity needs an update, as Go durations or "5d".
	// A kind with a cadence has due dates.
	Cadence   map[string]string `toml:"cadence"`
	Worktrees *bool             `toml:"worktrees"` // offer T3's worktrees; default: true for git

	re      *regexp.Regexp
	cadence map[int]time.Duration
	host    Host
}

type Config struct {
	StaleAfter string     `toml:"stale_after"` // default 90d
	Kinds      []*Kind    `toml:"kind"`
	Machines   []*Machine `toml:"machine"` // other machines whose t3p owns their places
	// Rex hosts (labels from `rex hosts`) whose tabs `t3p threads` looks at,
	// besides this machine's server. Unset: every host Rex lists.
	RexHosts []string `toml:"rex_hosts"`

	stale time.Duration
	Path  string // the file it was read from
	Found bool   // false: the defaults
}

// configDir is where config.toml lives.
func configDir() string {
	if d := os.Getenv("T3P_CONFIG_DIR"); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("APPDATA"); d != "" {
			return filepath.Join(d, "t3p")
		}
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "t3p")
	}
	return filepath.Join(homeDir(), ".config", "t3p")
}

// stateDir is where t3p keeps what it learns: notes, statuses, due dates.
func stateDir() string {
	if d := os.Getenv("T3P_STATE_DIR"); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "t3p")
		}
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "t3p")
	}
	return filepath.Join(homeDir(), ".local", "state", "t3p")
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" && runtime.GOOS != "windows" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func loadConfig() (*Config, error) {
	cfg := &Config{Path: filepath.Join(configDir(), "config.toml")}
	data, err := os.ReadFile(cfg.Path)
	switch {
	case err == nil:
		if _, err := toml.Decode(string(data), cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", cfg.Path, err)
		}
		cfg.Found = true
	case os.IsNotExist(err):
	default:
		return nil, err
	}
	if len(cfg.Kinds) == 0 {
		cfg.Kinds = []*Kind{{Name: "project", Roots: []string{"~/Projects"}}}
	}
	return cfg, cfg.prepare()
}

func (cfg *Config) prepare() error {
	var err error
	if cfg.stale, err = parseDuration(orDefault(cfg.StaleAfter, "90d")); err != nil {
		return fmt.Errorf("stale_after: %w", err)
	}
	machines := map[string]bool{}
	for _, m := range cfg.Machines {
		m.Name = strings.TrimSpace(m.Name)
		if m.Name == "" {
			return fmt.Errorf("a [[machine]] has no name")
		}
		if machines[strings.ToLower(m.Name)] {
			return fmt.Errorf("two machines are called %q", m.Name)
		}
		machines[strings.ToLower(m.Name)] = true
	}
	seen := map[string]bool{}
	for _, k := range cfg.Kinds {
		k.Name = strings.TrimSpace(k.Name)
		if k.Name == "" {
			return fmt.Errorf("a [[kind]] has no name")
		}
		if seen[k.Name] {
			return fmt.Errorf("two kinds are called %q", k.Name)
		}
		seen[k.Name] = true
		if k.Plural == "" {
			k.Plural = k.Name + "s"
		}
		if k.Detect == "" {
			k.Detect = "git"
			if k.Match != "" {
				k.Detect = "any"
			}
		}
		if k.Detect != "git" && k.Detect != "any" {
			return fmt.Errorf("kind %s: detect is git or any, not %q", k.Name, k.Detect)
		}
		if k.Match != "" {
			if k.re, err = regexp.Compile(k.Match); err != nil {
				return fmt.Errorf("kind %s: match: %w", k.Name, err)
			}
		}
		k.cadence = map[int]time.Duration{}
		for sev, d := range k.Cadence {
			n, err := strconv.Atoi(sev)
			if err != nil {
				return fmt.Errorf("kind %s: cadence keys are severities (1, 2, …), not %q", k.Name, sev)
			}
			if k.cadence[n], err = parseDuration(d); err != nil {
				return fmt.Errorf("kind %s: cadence %s: %w", k.Name, sev, err)
			}
		}
		if k.host, err = newHost(k.Host); err != nil {
			return fmt.Errorf("kind %s: %w", k.Name, err)
		}
	}
	return nil
}

// worktrees says whether a new thread in this kind picks among T3's worktrees.
func (k *Kind) worktrees() bool {
	if k.Worktrees != nil {
		return *k.Worktrees
	}
	return k.Detect == "git"
}

// severities lists the kind's severities, lowest number first.
func (k *Kind) severities() []int {
	var out []int
	for s := range k.cadence {
		out = append(out, s)
	}
	sort.Ints(out)
	return out
}

func (cfg *Config) kind(name string) *Kind {
	for _, k := range cfg.Kinds {
		if strings.EqualFold(k.Name, name) || strings.EqualFold(k.Plural, name) {
			return k
		}
	}
	return nil
}

// parseDuration takes Go durations plus days and weeks: "36h", "5d", "2w".
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if m := regexp.MustCompile(`^(\d+)\s*([dw])$`).FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		day := 24 * time.Hour
		if m[2] == "w" {
			day *= 7
		}
		return time.Duration(n) * day, nil
	}
	return time.ParseDuration(s)
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// exampleConfig is what `t3p config init` writes.
const exampleConfig = `# t3p: the kinds of work on this machine. Each [[kind]] is a work product,
# named however you like. Folders directly inside a kind's roots are that kind.

stale_after = "90d"   # no thread or commit for this long: Tidy Up asks about it

[[kind]]
name  = "project"
roots = ["~/Projects"]
detect = "git"        # git: only repositories. any: every folder.

# Other machines, each running its own t3p (and the t3 CLI). Their places are
# listed here and changed there; nothing is copied between machines.
#
# [[machine]]
# name = "buildbox"        # as T3 names it (tr3x envs)
# ssh  = "buildbox"        # the ssh destination; default name
# t3p  = "~/.local/bin/t3p"

# A kind you create from a template, with due dates by severity, that also
# works in a share outside its folder. Uncomment and adjust.
#
# [[kind]]
# name     = "case"
# host     = "wsl"                        # this machine's WSL (or "wsl:Ubuntu"); omit for here
# machine  = ""                           # what T3 calls that host, if not this machine
# roots    = ["~/Claude_Work"]
# detect   = "any"
# match    = '^\d{6,}'                    # folder names start with the case number
# create   = true
# template = "~/Claude_Work/.template"
# track    = ["/mnt/t/upload/ftp/{number}"]
# related  = "RELATED"                    # a file listing related case numbers
# cadence  = { 1 = "24h", 2 = "48h", 3 = "5d", 4 = "7d" }
# worktrees = false
`
