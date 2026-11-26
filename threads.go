package main

// Threads: agent conversations (Claude Code, Codex) running in Rex tabs.
//
// Nothing here is stored. Each call reconciles what's running from two
// sources that already know:
//
//   - Rex, on each host: sessions are topics, tabs are threads, and a tab's
//     resume record says which agent runs there, in which folder, and its
//     conversation id (Rex servers from preview.25; older ones only say
//     which program is in front).
//   - Atuin threads: the title, last activity and size of each conversation,
//     by the same conversation id, synced across machines.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

type Thread struct {
	Topic     string    `json:"topic"`      // the Rex session's label
	Host      string    `json:"host"`       // the Rex host label; "" for this machine's server
	Machine   string    `json:"machine"`    // what to call it: the host label, or this machine's name
	SessionID string    `json:"session_id"` //
	WindowID  string    `json:"window_id"`  // the tab
	BlockID   string    `json:"block_id"`   // the pane the agent runs in
	Tab       string    `json:"tab,omitempty"`
	Agent     string    `json:"agent"`             // claude-code, codex, opencode
	Conv      string    `json:"conv,omitempty"`    // the agent's conversation id
	Cwd       string    `json:"cwd,omitempty"`     //
	Title     string    `json:"title,omitempty"`   // from Atuin
	Updated   time.Time `json:"updated,omitempty"` // from Atuin
	Messages  int       `json:"messages,omitempty"`
	AtuinID   string    `json:"atuin_id,omitempty"`
	Focused   bool      `json:"focused,omitempty"` // the focused pane of its tab
}

// rexBin finds the rex CLI: on the PATH, else where a Rex pane says it is,
// else the Mac app's copy.
func rexBin() string {
	var candidates []string
	if d := os.Getenv("REX_BIN_DIR"); d != "" {
		candidates = append(candidates, filepath.Join(d, "rex"))
	}
	candidates = append(candidates,
		"~/Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS/rex",
		"/Applications/Rex Beta.app/Contents/Helpers/Rex Server.rex/Contents/MacOS/rex",
		"/Applications/Rex.app/Contents/Helpers/Rex Server.rex/Contents/MacOS/rex")
	return findTool("T3P_REX", "rex", candidates)
}

func atuinBin() string {
	return findTool("T3P_ATUIN", "atuin", []string{filepath.Join(homeDir(), ".atuin", "bin", "atuin")})
}

// rexJSON runs rex against a host ("" for this machine's server) and decodes
// its JSON.
func rexJSON(host string, out any, args ...string) error {
	argv := []string{rexBin()}
	if host != "" {
		argv = append(argv, "-S", host)
	}
	argv = append(argv, args...)
	s, err := runProcess(20*time.Second, argv...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(s), out)
}

type rexSession struct {
	SessionID string `json:"session_id"`
	Label     string `json:"label"`
}

type rexWindow struct {
	WindowID       string `json:"window_id"`
	Label          string `json:"label"`
	FocusedBlockID string `json:"focused_block_id"`
	Layers         []struct {
		Blocks []struct {
			BlockID string `json:"block_id"`
		} `json:"blocks"`
	} `json:"layers"`
}

type resumeRecord struct {
	Program string   `json:"program"`
	Cwd     string   `json:"cwd"`
	Chosen  bool     `json:"chosen"`
	Command []string `json:"command"`
}

type procInfo struct {
	Foreground struct {
		Name  string `json:"name"`
		Argv0 string `json:"argv0"`
		Path  string `json:"path"`
		Cwd   string `json:"cwd"`
	} `json:"foreground"`
}

// Claude Code names its process after its version, such as 2.1.332.
var versionRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// agentOf says which agent a program name is, or "".
func agentOf(name string) string {
	switch n := strings.ToLower(filepath.Base(name)); {
	case n == "claude" || versionRE.MatchString(n):
		return "claude-code"
	case n == "codex":
		return "codex"
	case n == "opencode":
		return "opencode"
	}
	return ""
}

// fromRecord reads a tab's resume record: the agent, its folder and its
// conversation id. ok is false when the server can't say (an older Rex).
func fromRecord(host, block string) (agent, cwd, conv string, ok bool) {
	var r struct {
		Records []resumeRecord `json:"records"`
	}
	if err := rexJSON(host, &r, "block", "call", "-b", block, "program_resume_records"); err != nil {
		return "", "", "", false
	}
	for _, rec := range r.Records {
		if !rec.Chosen {
			continue
		}
		agent = agentOf(rec.Program)
		cwd = rec.Cwd
		// claude --resume <id> · codex resume <id> · opencode --session <id>
		for i, a := range rec.Command {
			if (a == "--resume" || a == "resume" || a == "--session") && i+1 < len(rec.Command) {
				conv = rec.Command[i+1]
			}
		}
	}
	return agent, cwd, conv, true
}

// fromProcess is the fallback for servers without resume records: which
// agent is in front, and where. The conversation isn't known.
func fromProcess(host, block string) (agent, cwd string) {
	var p procInfo
	if err := rexJSON(host, &p, "block", "call", "-b", block, "process"); err != nil {
		return "", ""
	}
	for _, n := range []string{p.Foreground.Name, p.Foreground.Argv0, p.Foreground.Path} {
		if a := agentOf(n); a != "" {
			return a, p.Foreground.Cwd
		}
	}
	return "", ""
}

// hostThreads lists the agent tabs on one Rex host.
func hostThreads(host string) ([]*Thread, error) {
	var ls struct {
		Sessions []rexSession `json:"sessions"`
	}
	if err := rexJSON(host, &ls, "ls", "--json"); err != nil {
		return nil, err
	}
	machine := host
	if machine == "" {
		machine = localMachineName()
	}
	var out []*Thread
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, s := range ls.Sessions {
		var view struct {
			Windows []rexWindow `json:"windows"`
		}
		if err := rexJSON(host, &view, "session", "inspect", s.SessionID, "--json"); err != nil {
			continue
		}
		for _, w := range view.Windows {
			for _, l := range w.Layers {
				for _, b := range l.Blocks {
					wg.Add(1)
					go func(s rexSession, w rexWindow, block string) {
						defer wg.Done()
						agent, cwd, conv, ok := fromRecord(host, block)
						if !ok {
							agent, cwd = fromProcess(host, block)
						}
						if agent == "" {
							return
						}
						mu.Lock()
						out = append(out, &Thread{Topic: s.Label, Host: host, Machine: machine,
							SessionID: s.SessionID, WindowID: w.WindowID, BlockID: block, Tab: w.Label,
							Agent: agent, Conv: conv, Cwd: cwd, Focused: block == w.FocusedBlockID})
						mu.Unlock()
					}(s, w, b.BlockID)
				}
			}
		}
	}
	wg.Wait()
	return out, nil
}

// localMachineName is what this machine is called in Rex: its server's
// label for itself isn't exposed, so the short host name.
func localMachineName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "this machine"
	}
	h, _, _ = strings.Cut(h, ".")
	return h
}

// atuinThread is one line of `atuin threads list --style ndjson`.
type atuinThread struct {
	Harness   string    `json:"harness"`
	SessionID string    `json:"session_id"`
	AtuinID   string    `json:"atuin_id"`
	Cwd       string    `json:"cwd"`
	Title     string    `json:"title"`
	Updated   time.Time `json:"updated_at"`
	Messages  int       `json:"message_count"`
}

func atuinThreads() map[string]atuinThread {
	out := map[string]atuinThread{}
	bin := atuinBin()
	if bin == "" {
		return out
	}
	s, err := runProcess(20*time.Second, bin, "threads", "list", "--style", "ndjson")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(s, "\n") {
		var t atuinThread
		if json.Unmarshal([]byte(line), &t) == nil && t.SessionID != "" {
			out[t.SessionID] = t
		}
	}
	return out
}

// listThreads reconciles every host's agent tabs with Atuin. Hosts that don't
// answer are reported, not fatal.
func listThreads(hosts []string) ([]*Thread, []string) {
	all := append([]string{""}, hosts...)
	results := make([][]*Thread, len(all))
	errs := make([]error, len(all))
	var wg sync.WaitGroup
	for i, h := range all {
		wg.Add(1)
		go func(i int, h string) {
			defer wg.Done()
			results[i], errs[i] = hostThreads(h)
		}(i, h)
	}
	var meta map[string]atuinThread
	wg.Add(1)
	go func() { defer wg.Done(); meta = atuinThreads() }()
	wg.Wait()

	var threads []*Thread
	var warnings []string
	for i, r := range results {
		if errs[i] != nil {
			name := all[i]
			if name == "" {
				name = localMachineName()
			}
			warnings = append(warnings, fmt.Sprintf("%s: %s", name, firstLine(errs[i].Error())))
		}
		threads = append(threads, r...)
	}
	for _, t := range threads {
		if m, ok := meta[t.Conv]; ok && t.Conv != "" {
			t.Title, t.Updated, t.Messages, t.AtuinID = m.Title, m.Updated, m.Messages, m.AtuinID
		}
	}
	sort.SliceStable(threads, func(i, j int) bool {
		a, b := threads[i], threads[j]
		if !a.Updated.Equal(b.Updated) {
			return a.Updated.After(b.Updated)
		}
		if a.Topic != b.Topic {
			return a.Topic < b.Topic
		}
		return a.Machine < b.Machine
	})
	return threads, warnings
}

// threadHosts are the Rex hosts to look at besides this machine's server:
// rex_hosts in the config, or every host `rex hosts` lists.
func threadHosts(cfg *Config) []string {
	if cfg != nil && cfg.RexHosts != nil {
		return cfg.RexHosts
	}
	s, err := runProcess(10*time.Second, rexBin(), "hosts")
	if err != nil {
		return nil
	}
	var hosts []string
	for i, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) == 0 {
			continue // the header
		}
		hosts = append(hosts, f[0])
	}
	return hosts
}

func since(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// threadTitle is what a thread is called: Atuin's title, else its folder.
func threadTitle(t *Thread) string {
	if t.Title != "" {
		return clip(t.Title, 70)
	}
	if t.Cwd != "" {
		return baseName(t.Cwd)
	}
	return t.Tab
}

func threadRows(threads []*Thread) []Row {
	rows := make([]Row, 0, len(threads))
	for _, t := range threads {
		facts := []string{t.Topic, t.Machine}
		if t.Agent != "claude-code" {
			facts = append(facts, t.Agent)
		}
		if s := since(t.Updated); s != "" {
			facts = append(facts, s)
		}
		detail := ""
		if t.Cwd != "" {
			detail = shortHome(t.Cwd, homeDir(), "/")
		}
		id, _ := json.Marshal(map[string]string{"host": t.Host, "session_id": t.SessionID, "window_id": t.WindowID})
		rows = append(rows, Row{ID: string(id), Title: threadTitle(t), Subtitle: strings.Join(facts, " · "),
			Detail: detail, Icon: "terminal"})
	}
	return rows
}

// goTo shows a thread's tab in a Rex app. A Rex app only finds a session
// through the server that has it, so for another host the request goes
// through that host's server, to the app by its label (as `rex hosts`
// and each server's client list name it, such as "iPad").
func goTo(host, client, session, window string) error {
	if client == "" || session == "" {
		return fmt.Errorf("usage: t3p threads go --client <app> --session <id> [--window <id>] [--host <host>]")
	}
	argv := []string{rexBin()}
	if host != "" {
		argv = append(argv, "-S", host)
	}
	argv = append(argv, "do", "-C", client, "session.select", "session_id="+session)
	if window != "" {
		argv = append(argv, "window_id="+window)
	}
	_, err := runProcess(20*time.Second, argv...)
	return err
}

func threadsCmd(rest []string) error {
	if len(rest) > 0 && rest[0] == "go" {
		a := parseArgs(rest[1:])
		return goTo(a.flags["host"], a.flags["client"], a.flags["session"], a.flags["window"])
	}
	a := parseArgs(rest, "json", "rows")
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	threads, warnings := listThreads(threadHosts(cfg))
	switch {
	case a.has("json"):
		return printJSON(map[string]any{"threads": threads, "warnings": warnings})
	case a.has("rows"):
		return printJSON(map[string]any{"rows": threadRows(threads), "warnings": warnings})
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TOPIC\tMACHINE\tAGENT\tUPDATED\tTITLE")
	for _, t := range threads {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.Topic, t.Machine, t.Agent, since(t.Updated), threadTitle(t))
	}
	w.Flush()
	for _, s := range warnings {
		fmt.Fprintln(os.Stderr, "t3p:", s)
	}
	return nil
}
