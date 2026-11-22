package main

// Other machines. Each machine runs its own t3p, which owns the places whose
// folders are there: their notes, statuses, due dates. To show everything,
// this t3p asks each machine's t3p for its places in one SSH call and
// merges them in, the way tr3x asks each machine's T3 for its threads. A
// change to a place goes to the machine that owns it. Nothing is copied
// between machines, so there's nothing to keep in sync, and work that lives
// on one machine stays there.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Machine struct {
	Name string `toml:"name"` // what T3 calls it, as tr3x envs shows
	SSH  string `toml:"ssh"`  // the ssh destination; default Name
	T3P  string `toml:"t3p"`  // t3p there; default ~/.local/bin/t3p
}

// Export is what `t3p export` prints: a machine's own view, for another t3p.
type Export struct {
	Machine string     `json:"machine,omitempty"`
	Home    string     `json:"home"`
	Version string     `json:"version"`
	Kinds   []KindInfo `json:"kinds"`
	Places  []*Place   `json:"places"`
	At      time.Time  `json:"at"`
}

type KindInfo struct {
	Name       string `json:"name"`
	Plural     string `json:"plural"`
	Host       string `json:"host"`
	Machine    string `json:"machine,omitempty"` // set when the kind is another machine's
	Create     bool   `json:"create"`
	Worktrees  bool   `json:"worktrees"`
	Severities []int  `json:"severities,omitempty"`
	Match      string `json:"match,omitempty"`
}

func (cfg *Config) kindInfos() []KindInfo {
	var out []KindInfo
	for _, k := range cfg.Kinds {
		out = append(out, KindInfo{k.Name, k.Plural, k.host.ID(), "", k.Create, k.worktrees(), k.severities(), k.Match})
	}
	return out
}

func (cfg *Config) machine(name string) *Machine {
	for _, m := range cfg.Machines {
		if strings.EqualFold(m.Name, name) {
			return m
		}
	}
	return nil
}

// shQuote quotes s for the remote shell, which ssh hands the command line to.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// command is the remote command line: t3p there and its arguments, each
// quoted, so nothing in a name or a note is read by the shell.
func (m *Machine) command(args ...string) string {
	bin := orDefault(m.T3P, "~/.local/bin/t3p")
	var prog string
	if strings.HasPrefix(bin, "~/") {
		prog = `"$HOME"/` + shQuote(bin[2:])
	} else {
		prog = shQuote(bin)
	}
	parts := []string{prog}
	for _, a := range args {
		parts = append(parts, shQuote(a))
	}
	return strings.Join(parts, " ")
}

// sshRun runs t3p on m. A var, so tests can stand in for ssh.
var sshRun = func(m *Machine, timeout time.Duration, args ...string) (string, error) {
	return runProcess(timeout, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8",
		orDefault(m.SSH, m.Name), m.command(args...))
}

// run runs a t3p command on m. --local goes right after the command, before
// any "--", so it's never read as text (a note's words).
func (m *Machine) run(timeout time.Duration, args ...string) (string, error) {
	if len(args) > 0 {
		args = append([]string{args[0], "--local"}, args[1:]...)
	}
	out, err := sshRun(m, timeout, args...)
	if err != nil {
		return out, fmt.Errorf("%s: %s", m.Name, strings.TrimPrefix(err.Error(), "t3p: "))
	}
	return out, nil
}

func cachePath(name string) string {
	safe := regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(name, "_")
	return filepath.Join(stateDir(), "machines", safe+".json")
}

// fetch asks m for its places, keeping a copy for when it can't be reached.
// offline is true when the copy is all there is.
func (m *Machine) fetch() (ex *Export, offline bool, err error) {
	out, err := m.run(45*time.Second, "export")
	if err == nil {
		ex = &Export{}
		if jerr := json.Unmarshal([]byte(out), ex); jerr != nil {
			err = fmt.Errorf("%s: t3p export: %w", m.Name, jerr)
		} else {
			if data, merr := json.Marshal(ex); merr == nil {
				os.MkdirAll(filepath.Dir(cachePath(m.Name)), 0o755)
				os.WriteFile(cachePath(m.Name), data, 0o644)
			}
			return ex, false, nil
		}
	}
	if cached := m.cached(); cached != nil {
		return cached, true, err
	}
	return nil, true, err
}

func (m *Machine) cached() *Export {
	data, err := os.ReadFile(cachePath(m.Name))
	if err != nil {
		return nil
	}
	ex := &Export{}
	if json.Unmarshal(data, ex) != nil {
		return nil
	}
	return ex
}

// expand writes a leading ~ as the machine's home, for joining tr3x's paths.
func (ex *Export) expand(p string) string {
	if p == "~" {
		return ex.Home
	}
	if strings.HasPrefix(p, "~/") {
		return strings.TrimRight(ex.Home, "/") + p[1:]
	}
	return p
}

// forwarded runs a command for a place another machine owns, there, on its
// own key, and returns what it printed.
func (ix *Index) forward(p *Place, args ...string) (string, error) {
	if p.owner == nil {
		return "", fmt.Errorf("%s isn't another machine's", p.Name)
	}
	if p.Offline {
		return "", fmt.Errorf("%s is offline; %s can change when it's back", p.Owner, p.Name)
	}
	out := []string{}
	for _, a := range args {
		if a == "\x00key" {
			a = p.remoteKey
		}
		out = append(out, a)
	}
	return p.owner.run(90*time.Second, out...)
}

// adopt turns what another machine's resolve or new printed into this
// machine's view: its project id, with the environment tr3x knows it by.
func (ix *Index) adopt(m *Machine, out string) (*Resolved, error) {
	r := &Resolved{}
	if err := json.Unmarshal([]byte(out), r); err != nil {
		return nil, fmt.Errorf("%s: %w", m.Name, err)
	}
	r.Key = "m:" + m.Name + ":" + r.Key
	if r.Project != "" && r.T3 == "" {
		ix.t3.read = false
		if list, err := ix.t3.Projects(); err == nil {
			for _, tp := range list {
				if tp.Machine == m.Name && tp.projectID() == r.Project {
					r.T3 = tp.ID
				}
			}
		}
	}
	if r.T3 != "" {
		r.Last = ix.store.Last[r.T3]
	}
	return r, nil
}
