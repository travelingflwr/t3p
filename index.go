package main

// The index: every place of every kind, merged with what T3 has and what t3p
// remembers. A place is a folder of some kind, a T3 project, or both.

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type GitInfo struct {
	Branch     string     `json:"branch,omitempty"`
	Dirty      int        `json:"dirty"`
	Ahead      int        `json:"ahead,omitempty"`
	Behind     int        `json:"behind,omitempty"`
	LastCommit *time.Time `json:"last_commit,omitempty"`
}

type Place struct {
	Key      string     `json:"key"`
	Name     string     `json:"name"`
	Kind     string     `json:"kind,omitempty"`
	Host     string     `json:"host,omitempty"`
	Path     string     `json:"path,omitempty"`
	Display  string     `json:"display,omitempty"`
	Machine  string     `json:"machine,omitempty"`
	T3       string     `json:"t3,omitempty"`       // "env/project"
	Activity string     `json:"activity,omitempty"` // tr3x's "2h ago"
	Exists   bool       `json:"exists"`
	Remote   bool       `json:"remote,omitempty"` // another machine's: listed, not looked into
	Git      *GitInfo   `json:"git,omitempty"`
	Desc     string     `json:"desc,omitempty"`   // from the README
	Thread   string     `json:"thread,omitempty"` // the newest thread's title, when there's no note or README line
	Last     *time.Time `json:"last,omitempty"`   // the latest of T3 activity and the last commit
	Due      *time.Time `json:"due,omitempty"`
	Overdue  bool       `json:"overdue,omitempty"`
	Scratch  bool       `json:"scratch,omitempty"` // T3's own place for threads with no project
	Owner    string     `json:"owner,omitempty"`   // the machine whose t3p owns it, when not this one
	Offline  bool       `json:"offline,omitempty"` // that machine couldn't be reached: its last list
	Meta

	owner     *Machine
	remoteKey string // its key on the machine that owns it
	kind      *Kind
	host      Host
	t3Order   int
}

func (p *Place) status() string {
	if p.Status == "" {
		return "open"
	}
	return p.Status
}

type Index struct {
	cfg    *Config
	t3     *T3
	store  *Store
	now    time.Time
	Places []*Place
	T3Err  error // T3's list couldn't be read: places show without it

	exports     map[string]*Export // each other machine's own list, by name
	MachineErrs map[string]error   // the machines that couldn't be reached
}

func placeKey(h Host, p string) string {
	if h == nil || h.ID() == "local" {
		return p
	}
	return h.ID() + ":" + p
}

// machineOf is what T3 calls the machine a kind's host is, "" when unknown.
func (ix *Index) machineOf(k *Kind) string {
	if k.Machine != "" {
		return k.Machine
	}
	if k.host.ID() == "local" {
		return ix.t3.Me()
	}
	return ""
}

// buildIndex lists every place. local leaves the other machines out, which
// is how another t3p asks this one for its own.
func buildIndex(cfg *Config, t3 *T3, store *Store, details, local bool) *Index {
	ix := &Index{cfg: cfg, t3: t3, store: store, now: time.Now(),
		exports: map[string]*Export{}, MachineErrs: map[string]error{}}
	byKey := map[string]*Place{}
	add := func(p *Place) *Place {
		if old := byKey[p.Key]; old != nil {
			return old
		}
		byKey[p.Key] = p
		ix.Places = append(ix.Places, p)
		return p
	}

	// 1. The folders of each kind.
	for _, k := range cfg.Kinds {
		for _, root := range k.Roots {
			root = k.host.Expand(root)
			folders, err := k.host.Folders(root)
			if err != nil {
				continue
			}
			for _, f := range folders {
				if !k.accepts(f) {
					continue
				}
				add(&Place{Key: placeKey(k.host, f.Path), Name: baseName(f.Path), Kind: k.Name,
					Host: k.host.ID(), Path: f.Path, Exists: true, kind: k, host: k.host, t3Order: -1})
			}
		}
	}

	// 1b. Each other machine's places, from its own t3p, asked in parallel.
	remoteByPath := map[string]map[string]*Place{}
	if !local {
		type got struct {
			m       *Machine
			ex      *Export
			offline bool
			err     error
		}
		results := make(chan got, len(cfg.Machines))
		for _, m := range cfg.Machines {
			go func(m *Machine) {
				ex, offline, err := m.fetch()
				results <- got{m, ex, offline, err}
			}(m)
		}
		for range cfg.Machines {
			g := <-results
			if g.err != nil {
				ix.MachineErrs[g.m.Name] = g.err
			}
			if g.ex == nil {
				continue
			}
			ix.exports[g.m.Name] = g.ex
			remoteByPath[g.m.Name] = map[string]*Place{}
			for _, q := range g.ex.Places {
				p := *q
				p.remoteKey, p.Key = q.Key, "m:"+g.m.Name+":"+q.Key
				p.Owner, p.Machine, p.owner, p.Offline = g.m.Name, g.m.Name, g.m, g.offline
				p.T3, p.Activity, p.t3Order = "", "", -1 // joined below, from tr3x
				add(&p)
				remoteByPath[g.m.Name][p.Path] = byKey[p.Key]
			}
		}
	}

	// 2. T3's projects, joined to those folders by machine and exact path.
	projects, err := t3.Projects()
	ix.T3Err = err
	me := t3.Me()
	for _, tp := range projects {
		var hit *Place
		if ex := ix.exports[tp.Machine]; ex != nil {
			hit = remoteByPath[tp.Machine][ex.expand(tp.Path)]
		}
		for _, k := range cfg.Kinds {
			if hit != nil {
				break
			}
			m := ix.machineOf(k)
			if m == "" || m != tp.Machine {
				continue
			}
			full := k.host.Expand(tp.Path)
			if p := byKey[placeKey(k.host, full)]; p != nil {
				hit = p
				break
			}
			// A T3 project directly in a root is that kind even when it
			// isn't what the kind detects (a plain folder in a git kind).
			if k.inRoot(full) {
				hit = add(&Place{Key: placeKey(k.host, full), Name: baseName(full), Kind: k.Name,
					Host: k.host.ID(), Path: full, Exists: k.host.IsDir(full), kind: k, host: k.host})
				break
			}
		}
		if hit == nil {
			if tp.Machine != "" && tp.Machine == me {
				full := localHost{}.Expand(tp.Path)
				hit = add(&Place{Key: full, Name: tp.Title, Host: "local", Path: full,
					Exists: localHost{}.IsDir(full), host: localHost{}})
			} else {
				hit = add(&Place{Key: "t3:" + tp.ID, Name: tp.Title, Path: tp.Path, Machine: tp.Machine,
					Exists: true, Remote: true})
			}
		}
		if hit.T3 == "" {
			hit.T3, hit.Activity, hit.t3Order = tp.ID, tp.Detail, tp.Order
			if tp.Machine != me {
				hit.Machine = tp.Machine
			}
			if hit.kind == nil && tp.Title != "" {
				hit.Name = tp.Title
			}
		}
	}

	// 3. What t3p remembers, and what follows from it.
	for _, p := range ix.Places {
		if isScratch(p.Path) {
			p.Scratch, p.Name = true, "No project"
		}
		if p.owner != nil { // its machine worked these out; add T3's activity
			if t := t3Ago(p.Activity, ix.now); !t.IsZero() && (p.Last == nil || t.After(*p.Last)) {
				p.Last = &t
			}
			continue
		}
		p.Meta = store.peek(p.Key)
		if p.host != nil {
			p.Display = p.host.Display(p.Path)
		} else {
			p.Display = p.Path
		}
		if t := t3Ago(p.Activity, ix.now); !t.IsZero() {
			p.Last = &t
		}
		ix.due(p)
	}

	// 4. Git and the README, looked at in parallel.
	if details {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for _, p := range ix.Places {
			if p.Remote || !p.Exists || p.host == nil {
				continue
			}
			wg.Add(1)
			go func(p *Place) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				p.Git = gitInfo(p.host, p.Path)
				p.Desc = readmeLine(p.host, p.Path)
				if p.Note == "" && p.Desc == "" && p.T3 != "" {
					_, id, _ := strings.Cut(p.T3, "/")
					p.Thread = ix.t3.LatestThread(p.host, p.Path, id)
				}
				if p.Git != nil && p.Git.LastCommit != nil && (p.Last == nil || p.Git.LastCommit.After(*p.Last)) {
					p.Last = p.Git.LastCommit
				}
			}(p)
		}
		wg.Wait()
	}

	ix.sort()
	return ix
}

// isScratch says whether p is T3's scratch folder, where T3 starts a thread
// that has no project: <T3 home>/scratch, ~/.t3/scratch by default, on any
// machine. It belongs to T3: never closed, flagged or counted as nesting.
func isScratch(p string) bool {
	p = strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
	if strings.HasSuffix(p, "/.t3/scratch") || p == "~/.t3/scratch" {
		return true
	}
	if h := os.Getenv("T3CODE_HOME"); h != "" {
		return p == strings.TrimRight(strings.ReplaceAll(h, `\`, "/"), "/")+"/scratch"
	}
	return false
}

func (k *Kind) accepts(f Folder) bool {
	if k.Detect == "git" && !f.Git {
		return false
	}
	return k.re == nil || k.re.MatchString(baseName(f.Path))
}

// inRoot says whether p is directly inside one of the kind's roots.
func (k *Kind) inRoot(p string) bool {
	for _, r := range k.Roots {
		r = k.host.Expand(r)
		if dirName(p) == r {
			return true
		}
	}
	return false
}

func baseName(p string) string {
	p = strings.TrimRight(p, `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func dirName(p string) string {
	if strings.Contains(p, `\`) && !strings.Contains(p, "/") {
		return filepath.Dir(p)
	}
	return path.Dir(strings.ReplaceAll(p, `\`, "/"))
}

// due works out when a place's next update is due: its kind has a cadence,
// it is open, and it has a severity.
func (ix *Index) due(p *Place) {
	if p.kind == nil || len(p.kind.cadence) == 0 || p.Sev == 0 || p.status() != "open" {
		return
	}
	base := p.LastUpdate
	if base == nil {
		base = p.Created
	}
	if base == nil {
		return
	}
	every, ok := p.kind.cadence[p.Sev]
	if !ok { // a severity the config doesn't name gets the slowest cadence
		for _, d := range p.kind.cadence {
			if d > every {
				every = d
			}
		}
	}
	d := base.Add(every)
	p.Due, p.Overdue = &d, ix.now.After(d)
}

// sort: open before parked before closed; overdue, then soonest due; then
// what T3 has before what it doesn't; then the latest activity; then by name.
func (ix *Index) sort() {
	rank := map[string]int{"open": 0, "parked": 1, "closed": 2}
	sort.SliceStable(ix.Places, func(i, j int) bool {
		a, b := ix.Places[i], ix.Places[j]
		if a.Scratch != b.Scratch { // no project first, as T3 lists it
			return a.Scratch
		}
		if a.Scratch && (a.Machine == "") != (b.Machine == "") { // this machine's first
			return a.Machine == ""
		}
		if ra, rb := rank[a.status()], rank[b.status()]; ra != rb {
			return ra < rb
		}
		if a.Overdue != b.Overdue {
			return a.Overdue
		}
		if (a.Due != nil) != (b.Due != nil) {
			return a.Due != nil
		}
		if a.Due != nil && !a.Due.Equal(*b.Due) {
			return a.Due.Before(*b.Due)
		}
		if (a.T3 != "") != (b.T3 != "") {
			return a.T3 != ""
		}
		// Latest activity first: a thread or a commit, whichever is newer.
		if (a.Last != nil) != (b.Last != nil) {
			return a.Last != nil
		}
		if a.Last != nil && !a.Last.Equal(*b.Last) {
			return a.Last.After(*b.Last)
		}
		if a.T3 != "" && a.t3Order != b.t3Order {
			return a.t3Order < b.t3Order
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

// find resolves what a person typed: a key, a path, a name, or a T3 id.
func (ix *Index) find(q string) []*Place {
	q = strings.TrimSpace(q)
	for _, p := range ix.Places {
		if p.Key == q {
			return []*Place{p}
		}
	}
	local := localHost{}.Expand(q)
	var byName []*Place
	for _, p := range ix.Places {
		if p.Path == local && p.Host == "local" || p.T3 == q || (p.T3 != "" && strings.HasSuffix(p.T3, "/"+q)) {
			return []*Place{p}
		}
		if strings.EqualFold(p.Name, q) {
			byName = append(byName, p)
		}
	}
	return byName
}

// ---- git and README ------------------------------------------------------

// gitInfo is the state of the repository whose top folder is dir, nil when
// dir isn't one: a plain folder inside a repository (a case inside ~/Work)
// would otherwise show that repository's branch and changes as its own.
func gitInfo(h Host, dir string) *GitInfo {
	if top, err := h.Run(10*time.Second, "git", "-C", dir, "rev-parse", "--show-cdup"); err != nil || strings.TrimSpace(top) != "" {
		return nil
	}
	out, err := h.Run(10*time.Second, "git", "-C", dir, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return nil
	}
	g := &GitInfo{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			g.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.ab "):
			f := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
			if len(f) == 2 {
				g.Ahead, _ = strconv.Atoi(strings.TrimPrefix(f[0], "+"))
				g.Behind, _ = strconv.Atoi(strings.TrimPrefix(f[1], "-"))
			}
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			g.Dirty++
		}
	}
	if out, err := h.Run(10*time.Second, "git", "-C", dir, "log", "-1", "--format=%ct"); err == nil {
		if sec, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64); err == nil {
			t := time.Unix(sec, 0)
			g.LastCommit = &t
		}
	}
	return g
}

var (
	mdLink  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdMarks = regexp.MustCompile("[*_`]+")
)

// readmeLine is the first line of prose in the folder's README: what the
// project says it is.
func readmeLine(h Host, dir string) string {
	sep := "/"
	if h.ID() == "local" {
		sep = string(filepath.Separator)
	}
	text := ""
	for _, name := range []string{"README.md", "readme.md", "README", "AGENTS.md", "CLAUDE.md"} {
		if text = h.ReadHead(dir+sep+name, 4096); text != "" {
			break
		}
	}
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence || line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "<") ||
			strings.HasPrefix(line, "!") || strings.HasPrefix(line, "[!") || strings.HasPrefix(line, "|") ||
			strings.HasPrefix(line, "---") || strings.HasPrefix(line, ">") {
			continue
		}
		line = strings.TrimLeft(line, "-* ")
		line = mdLink.ReplaceAllString(line, "$1")
		line = strings.TrimSpace(mdMarks.ReplaceAllString(line, ""))
		if line == "" {
			continue
		}
		return clip(line, 90)
	}
	return ""
}

// clip shortens s to n characters, ending at a word when it can.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:.") + "…"
}
