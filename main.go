// t3p keeps track of your work in T3 Code: the projects, cases or whatever
// kinds of work you define, which of them T3 has, what each one is, and
// what state it's in. Rex's palette (t3p.lua) is its main face; everything
// it shows comes from here, as JSON.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const version = "0.1.0"

const usage = `t3p: your work in T3 Code, by kind.

  t3p ls [--kind K] [--all] [--json]     everything, most pressing first
  t3p rows [--kind K] [--all]            the same, as Rex palette rows
  t3p kinds [--json]                     the kinds of work this machine has
  t3p resolve [--kind K] <place|folder>  make it a T3 project if needed; print how to open it
  t3p new <kind> <name> [--sev N]        create one from the kind's template, add it to T3
  t3p note <place> [text…] [--clear]     a one-line note: what is this?
  t3p tag <place> +tag -tag …
  t3p park | open | close <place>        close: out of the picker, off T3's list if it has no
                                         threads. Nothing is ever deleted; --force is never used
  t3p sev <place> <N>                    a severity, for kinds with a cadence
  t3p updated <place>                    you just sent an update: the next one is due from now
  t3p check [--json] [--reach]           what needs tidying up (--reach also checks shares)
  t3p ignore <place> <check>             don't raise that check for it again
  t3p browse [--kind K] [dir]            folders, for the palette's folder browser
  t3p mkdir [--kind K] [--git] <dir> <name>  a new folder in dir (--git: a repository)
  t3p last <env/project> [workspace]     the worktree a project was last opened in
  t3p config [init]                      where the config is; init writes an example
  t3p doctor                             check the setup

A <place> is a name, a path, or a key from ls --json.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "t3p:", err)
		os.Exit(1)
	}
}

// args splits flags from the rest, in any order. bools are the flags that
// take no value.
type args struct {
	pos   []string
	flags map[string]string
}

func parseArgs(in []string, bools ...string) args {
	a := args{flags: map[string]string{}}
	isBool := map[string]bool{}
	for _, b := range bools {
		isBool[b] = true
	}
	for i := 0; i < len(in); i++ {
		s := in[i]
		if s == "--" {
			a.pos = append(a.pos, in[i+1:]...)
			break
		}
		if strings.HasPrefix(s, "--") && len(s) > 2 {
			name, val, hasVal := strings.Cut(s[2:], "=")
			if !hasVal && !isBool[name] && i+1 < len(in) {
				i++
				val = in[i]
			}
			if isBool[name] && !hasVal {
				val = "true"
			}
			a.flags[name] = val
			continue
		}
		a.pos = append(a.pos, s)
	}
	return a
}

func (a args) has(name string) bool { return a.flags[name] != "" && a.flags[name] != "false" }

type env struct {
	cfg   *Config
	t3    *T3
	store *Store
}

func setup() (*env, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	store, err := loadStore()
	if err != nil {
		return nil, err
	}
	return &env{cfg: cfg, t3: findT3(), store: store}, nil
}

func (e *env) index(details bool) *Index { return buildIndex(e.cfg, e.t3, e.store, details) }

// one finds exactly one place for q.
func (ix *Index) one(q string) (*Place, error) {
	found := ix.find(q)
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no place called %q (t3p ls lists them)", q)
	case 1:
		return found[0], nil
	}
	var keys []string
	for _, p := range found {
		keys = append(keys, p.Key)
	}
	return nil, fmt.Errorf("%q could be any of: %s", q, strings.Join(keys, ", "))
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func run(argv []string) error {
	if len(argv) == 0 || argv[0] == "help" || argv[0] == "-h" || argv[0] == "--help" {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := argv[0], argv[1:]
	if cmd == "version" || cmd == "--version" {
		fmt.Println("t3p", version)
		return nil
	}
	if cmd == "config" {
		return configCmd(rest)
	}
	e, err := setup()
	if err != nil {
		return err
	}
	a := parseArgs(rest, "json", "all", "clear", "reach", "git")

	need := func(n int, what string) error {
		if len(a.pos) < n {
			return fmt.Errorf("usage: t3p %s %s", cmd, what)
		}
		return nil
	}
	// mutate runs f on one place and saves.
	mutate := func(f func(ix *Index, p *Place) error) error {
		if err := need(1, "<place>"); err != nil {
			return err
		}
		ix := e.index(false)
		p, err := ix.one(a.pos[0])
		if err != nil {
			return err
		}
		if err := f(ix, p); err != nil {
			return err
		}
		return e.store.save()
	}

	switch cmd {
	case "ls":
		ix := e.index(true)
		places := filterPlaces(ix, a.flags["kind"], a.has("all"))
		if a.has("json") {
			return printJSON(places)
		}
		printTable(ix, places)
		return nil

	case "rows":
		ix := e.index(true)
		return printJSON(rows(ix, filterPlaces(ix, a.flags["kind"], a.has("all"))))

	case "kinds":
		type kindOut struct {
			Name       string `json:"name"`
			Plural     string `json:"plural"`
			Host       string `json:"host"`
			Create     bool   `json:"create"`
			Worktrees  bool   `json:"worktrees"`
			Severities []int  `json:"severities,omitempty"`
			Match      string `json:"match,omitempty"`
		}
		var out []kindOut
		for _, k := range e.cfg.Kinds {
			out = append(out, kindOut{k.Name, k.Plural, k.host.ID(), k.Create, k.worktrees(), k.severities(), k.Match})
		}
		if a.has("json") {
			return printJSON(out)
		}
		for _, k := range out {
			fmt.Printf("%s (%s) on %s\n", k.Name, k.Plural, k.Host)
		}
		return nil

	case "resolve":
		if err := need(1, "<place>"); err != nil {
			return err
		}
		ix := e.index(true)
		p, err := ix.one(a.pos[0])
		if err != nil {
			// Any folder can become a T3 project (Add Folder…): on the
			// --kind's host, as that kind, else here with no kind.
			var k *Kind
			var h Host = localHost{}
			if name := a.flags["kind"]; name != "" {
				if k = e.cfg.kind(name); k == nil {
					return fmt.Errorf("no kind called %q", name)
				}
				h = k.host
			}
			dir := h.Expand(a.pos[0])
			if !h.IsDir(dir) {
				return err
			}
			p = &Place{Key: placeKey(h, dir), Name: baseName(dir), Host: h.ID(), Path: dir,
				Exists: true, host: h, kind: k}
			if k != nil {
				p.Kind = k.Name
			}
			p.Git = gitInfo(h, dir)
		}
		r, err := ix.resolve(p)
		if err != nil {
			return err
		}
		if err := e.store.save(); err != nil {
			return err
		}
		return printJSON(r)

	case "new":
		if err := need(2, "<kind> <name> [--sev N]"); err != nil {
			return err
		}
		k := e.cfg.kind(a.pos[0])
		if k == nil {
			return fmt.Errorf("no kind called %q", a.pos[0])
		}
		sev := 0
		if s := a.flags["sev"]; s != "" {
			if sev, err = strconv.Atoi(s); err != nil {
				return fmt.Errorf("--sev is a number")
			}
		}
		ix := e.index(false)
		r, err := ix.create(k, strings.Join(a.pos[1:], " "), sev)
		if serr := e.store.save(); err == nil {
			err = serr
		}
		if err != nil {
			return err
		}
		return printJSON(r)

	case "note":
		return mutate(func(ix *Index, p *Place) error {
			m := e.store.meta(p.Key)
			switch {
			case a.has("clear"):
				m.Note = ""
			case len(a.pos) > 1:
				m.Note = strings.TrimSpace(strings.Join(a.pos[1:], " "))
			default:
				fmt.Println(m.Note)
			}
			return nil
		})

	case "tag":
		return mutate(func(ix *Index, p *Place) error {
			m := e.store.meta(p.Key)
			for _, t := range a.pos[1:] {
				switch {
				case strings.HasPrefix(t, "-"):
					m.Tags = without(m.Tags, t[1:])
				default:
					t = strings.TrimPrefix(t, "+")
					m.Tags = append(without(m.Tags, t), t)
				}
			}
			fmt.Println(strings.Join(m.Tags, " "))
			return nil
		})

	case "park":
		return mutate(func(ix *Index, p *Place) error { e.store.meta(p.Key).Status = "parked"; return nil })
	case "open":
		return mutate(func(ix *Index, p *Place) error { e.store.meta(p.Key).Status = ""; return nil })
	case "close":
		return mutate(func(ix *Index, p *Place) error {
			msg, err := ix.close(p)
			if err == nil {
				fmt.Println(msg)
			}
			return err
		})

	case "sev":
		if err := need(2, "<place> <N>"); err != nil {
			return err
		}
		n, err := strconv.Atoi(a.pos[1])
		if err != nil || n < 0 {
			return fmt.Errorf("a severity is a number")
		}
		return mutate(func(ix *Index, p *Place) error {
			if p.kind == nil || len(p.kind.cadence) == 0 {
				return fmt.Errorf("%s's kind has no cadence, so no severities", p.Name)
			}
			m := e.store.meta(p.Key)
			m.Sev = n
			if m.LastUpdate == nil && m.Created == nil {
				now := time.Now()
				m.LastUpdate = &now // due dates count from when it got a severity
			}
			return nil
		})

	case "updated":
		return mutate(func(ix *Index, p *Place) error {
			now := time.Now()
			e.store.meta(p.Key).LastUpdate = &now
			return nil
		})

	case "check":
		ix := e.index(true)
		found := ix.checks(a.has("reach"))
		if a.has("json") {
			if found == nil {
				found = []Finding{}
			}
			return printJSON(found)
		}
		if len(found) == 0 {
			fmt.Println("Nothing to tidy up.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, f := range found {
			fmt.Fprintf(w, "%s\t%s\t%s\n", f.Check, f.Name, f.Text)
		}
		return w.Flush()

	case "ignore":
		if err := need(2, "<place> <check>"); err != nil {
			return err
		}
		return mutate(func(ix *Index, p *Place) error {
			m := e.store.meta(p.Key)
			m.Ignore = append(without(m.Ignore, a.pos[1]), a.pos[1])
			return nil
		})

	case "last":
		if err := need(1, "<env/project> [workspace]"); err != nil {
			return err
		}
		if len(a.pos) == 1 {
			fmt.Println(e.store.Last[a.pos[0]])
			return nil
		}
		e.store.Last[a.pos[0]] = a.pos[1]
		return e.store.save()

	case "mkdir":
		if err := need(2, "[--kind K] [--git] <dir> <name>"); err != nil {
			return err
		}
		return mkdirCmd(e, a)

	case "browse":
		return browse(e, a)

	case "doctor":
		return doctor(e)
	}
	return fmt.Errorf("no command %q (t3p help lists them)", cmd)
}

func without(list []string, s string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

func filterPlaces(ix *Index, kind string, all bool) []*Place {
	out := []*Place{}
	var k *Kind
	if kind != "" {
		k = ix.cfg.kind(kind)
	}
	for _, p := range ix.Places {
		if !all && p.status() == "closed" {
			continue
		}
		if kind != "" && (k == nil || p.Kind != k.Name) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ---- output --------------------------------------------------------------

type Row struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Icon     string `json:"icon,omitempty"`
}

// facts are the short things the palette shows beside a place's name.
func facts(ix *Index, p *Place) []string {
	var f []string
	if len(ix.cfg.Kinds) > 1 && p.Kind != "" {
		f = append(f, p.Kind)
	}
	if p.Status == "parked" || p.Status == "closed" {
		f = append(f, p.Status)
	}
	if p.Sev > 0 {
		f = append(f, fmt.Sprintf("Sev %d", p.Sev))
	}
	if p.Due != nil {
		if p.Overdue {
			f = append(f, "overdue "+span(ix.now.Sub(*p.Due)))
		} else {
			f = append(f, "due in "+span(p.Due.Sub(ix.now)))
		}
	}
	if g := p.Git; g != nil && g.Branch != "" {
		b := g.Branch
		if g.Dirty > 0 {
			b += fmt.Sprintf(" ±%d", g.Dirty)
		}
		if g.Ahead > 0 {
			b += fmt.Sprintf(" ↑%d", g.Ahead)
		}
		if g.Behind > 0 {
			b += fmt.Sprintf(" ↓%d", g.Behind)
		}
		f = append(f, b)
	}
	if p.T3 == "" {
		f = append(f, "add to T3")
	} else if !p.Exists {
		f = append(f, "folder gone")
	} else if p.Last != nil {
		f = append(f, ago(ix.now.Sub(*p.Last)))
	}
	return f
}

// whatItIs is the best answer to "what was this?": a note, the README's
// first line, the newest thread's title, or at least where it is.
func whatItIs(p *Place) string {
	switch {
	case p.Note != "":
		return p.Note
	case p.Desc != "":
		return p.Desc
	case p.Thread != "":
		return "Last thread: " + clip(p.Thread, 70)
	}
	return p.Display
}

func rows(ix *Index, places []*Place) []Row {
	out := []Row{}
	for _, p := range places {
		sub := whatItIs(p)
		if p.Machine != "" {
			sub += " · " + p.Machine
		}
		if len(p.Tags) > 0 {
			sub += " · #" + strings.Join(p.Tags, " #")
		}
		icon := "folder"
		if p.T3 == "" {
			icon = "plus"
		} else if p.Git != nil {
			icon = "branch"
		}
		out = append(out, Row{ID: p.Key, Title: p.Name, Subtitle: sub, Detail: strings.Join(facts(ix, p), " · "), Icon: icon})
	}
	return out
}

func printTable(ix *Index, places []*Place) {
	if ix.T3Err != nil {
		fmt.Fprintln(os.Stderr, "t3p: without T3's list:", ix.T3Err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tFACTS\tWHAT IT IS")
	for _, p := range places {
		what := whatItIs(p)
		if p.Machine != "" {
			what += " · " + p.Machine
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, strings.Join(facts(ix, p), " · "), what)
	}
	w.Flush()
}

// ---- config, browse, doctor --------------------------------------------

func configCmd(rest []string) error {
	cfg, err := loadConfig()
	path := configPath()
	if len(rest) > 0 && rest[0] == "init" {
		if _, serr := os.Stat(path); serr == nil {
			return fmt.Errorf("%s already exists", path)
		}
		if err := os.MkdirAll(configDir(), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(exampleConfig), 0o644); err != nil {
			return err
		}
		fmt.Println("Wrote", path)
		return nil
	}
	if err != nil {
		return err
	}
	if cfg.Found {
		fmt.Println(path)
	} else {
		fmt.Println(path, "(none yet: one kind, project, in ~/Projects; t3p config init writes an example)")
	}
	return nil
}

func configPath() string { return configDir() + string(os.PathSeparator) + "config.toml" }

// mkdirCmd makes a new folder for Add Folder…, on the --kind's host, and
// prints its path. --git makes it a repository.
func mkdirCmd(e *env, a args) error {
	var h Host = localHost{}
	if name := a.flags["kind"]; name != "" {
		k := e.cfg.kind(name)
		if k == nil {
			return fmt.Errorf("no kind called %q", name)
		}
		h = k.host
	}
	dir := h.Expand(a.pos[0])
	name := strings.TrimSpace(strings.Join(a.pos[1:], " "))
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") || badName.MatchString(name) {
		return fmt.Errorf("%q can't be a folder name", name)
	}
	if !h.IsDir(dir) {
		return fmt.Errorf("not a folder: %s", h.Display(dir))
	}
	dest := joinOn(h, dir, name)
	if err := h.Copy("", dest); err != nil {
		return err
	}
	if a.has("git") {
		if _, err := h.Run(30*time.Second, "git", "init", "-q", dest); err != nil {
			return fmt.Errorf("made %s, but git init failed: %w", h.Display(dest), err)
		}
	}
	return printJSON(map[string]string{"path": dest, "display": h.Display(dest)})
}

func browse(e *env, a args) error {
	var h Host = localHost{}
	var k *Kind
	if name := a.flags["kind"]; name != "" {
		if k = e.cfg.kind(name); k == nil {
			return fmt.Errorf("no kind called %q", name)
		}
		h = k.host
	}
	dir := ""
	if len(a.pos) > 0 {
		dir = h.Expand(a.pos[0])
	} else if k != nil && len(k.Roots) > 0 {
		dir = h.Expand(k.Roots[0])
	} else {
		dir = h.Home()
	}
	if !h.IsDir(dir) {
		return fmt.Errorf("not a folder: %s", dir)
	}
	folders, err := h.Folders(dir)
	if err != nil {
		return err
	}
	sort.Slice(folders, func(i, j int) bool {
		return strings.ToLower(baseName(folders[i].Path)) < strings.ToLower(baseName(folders[j].Path))
	})
	type item struct {
		Path string `json:"path"`
		Name string `json:"name"`
		Git  bool   `json:"git"`
	}
	out := struct {
		Dir     string `json:"dir"`
		Display string `json:"display"`
		Parent  string `json:"parent,omitempty"`
		Items   []item `json:"items"`
	}{Dir: dir, Display: h.Display(dir), Items: []item{}}
	if parent := dirName(dir); parent != dir && parent != "." {
		out.Parent = parent
	}
	for _, f := range folders {
		out.Items = append(out.Items, item{f.Path, baseName(f.Path), f.Git})
	}
	return printJSON(out)
}

func doctor(e *env) error {
	bad := 0
	say := func(ok bool, what, fix string) {
		mark := "✓"
		if !ok {
			mark, bad = "✗", bad+1
		}
		fmt.Printf("%s %s\n", mark, what)
		if !ok && fix != "" {
			fmt.Printf("    %s\n", fix)
		}
	}
	if e.cfg.Found {
		say(true, "config: "+e.cfg.Path, "")
	} else {
		say(true, "config: none, so one kind (project, in ~/Projects)", "")
	}
	say(e.t3.tr3x != "", "tr3x: "+orDefault(e.t3.tr3x, "not found"),
		"Install tr3x, or set T3P_TR3X. Without it t3p can add folders to T3 but not list T3's projects.")
	say(e.t3.t3 != "", "t3 CLI: "+orDefault(e.t3.t3, "not found"), "Install T3 Code's CLI, or set T3P_T3.")
	if e.t3.tr3x != "" {
		me := e.t3.Me()
		say(me != "", "this machine in T3: "+orDefault(me, "unknown"), "Run tr3x doctor.")
		_, err := e.t3.Projects()
		msg := "T3's projects"
		if err != nil {
			msg += ": " + err.Error()
		} else {
			msg += fmt.Sprintf(": %d", len(e.t3.list))
		}
		say(err == nil, msg, "Run tr3x doctor.")
	}
	for _, k := range e.cfg.Kinds {
		for _, r := range k.Roots {
			full := k.host.Expand(r)
			say(k.host.IsDir(full), fmt.Sprintf("%s root on %s: %s", k.Name, k.host.ID(), k.host.Display(full)),
				"Make the folder, or change roots in "+configPath())
		}
		if k.Template != "" {
			t := k.host.Expand(k.Template)
			say(k.host.IsDir(t), k.Name+" template: "+k.host.Display(t), "New ones start empty until it exists.")
		}
		if k.host.ID() != "local" && k.Machine == "" {
			say(false, k.Name+": which T3 environment is "+k.host.ID()+"?",
				`Set machine = "<name>" for the kind, as T3 names that environment, so its projects join up.`)
		}
	}
	say(true, "state: "+e.store.path, "")
	if bad > 0 {
		return errors.New("some checks failed")
	}
	return nil
}
