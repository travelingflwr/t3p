package main

// What t3p does to a place: make it a T3 project, create one from a
// template, park, close and reopen it, and the checks Tidy Up raises.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Resolved struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind,omitempty"`
	T3        string   `json:"t3,omitempty"`      // "env/project", for tr3x
	Project   string   `json:"project,omitempty"` // the project id alone
	Added     bool     `json:"added,omitempty"`   // t3p just added it to T3
	Worktrees bool     `json:"worktrees"`         // pick among T3's worktrees
	Last      string   `json:"last,omitempty"`    // the workspace used last
	Tracked   []string `json:"tracked,omitempty"`
	Skipped   []string `json:"skipped,omitempty"` // tracks that couldn't be reached
}

var leadingDigits = regexp.MustCompile(`^\d+`)

// tracks lists the folders outside p that T3 should also work in: the
// kind's track patterns for p, and for each name in its related file. Only
// the reachable ones are returned; the rest are skipped, never walked.
func (ix *Index) tracks(p *Place) (ok, skipped []string) {
	k := p.kind
	if k == nil || len(k.Track) == 0 {
		return nil, nil
	}
	names := []string{p.Name}
	if k.Related != "" {
		text := p.host.ReadHead(joinOn(p.host, p.Path, k.Related), 8192)
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(strings.TrimRight(line, "\r"))
			if line != "" && !strings.HasPrefix(line, "#") {
				names = append(names, strings.Fields(line)[0])
			}
		}
	}
	seen := map[string]bool{}
	for _, name := range names {
		number := leadingDigits.FindString(name)
		if number == "" {
			number = name
		}
		for _, pattern := range k.Track {
			t := strings.NewReplacer("{name}", name, "{number}", number, "{case}", number).Replace(pattern)
			t = p.host.Expand(t)
			if seen[t] {
				continue
			}
			seen[t] = true
			if p.host.Reachable(t, 3*time.Second) {
				ok = append(ok, t)
			} else {
				skipped = append(skipped, t)
			}
		}
	}
	return ok, skipped
}

func joinOn(h Host, dir, name string) string {
	if h.ID() == "local" && strings.Contains(dir, `\`) {
		return dir + `\` + name
	}
	return strings.TrimRight(dir, "/") + "/" + name
}

// resolve makes p a T3 project if it isn't one, and says how to open a
// thread in it.
func (ix *Index) resolve(p *Place) (*Resolved, error) {
	if p.owner != nil && p.T3 == "" { // its machine adds it to T3 there
		out, err := ix.forward(p, "resolve", "\x00key")
		if err != nil {
			return nil, err
		}
		return ix.adopt(p.owner, out)
	}
	r := &Resolved{Key: p.Key, Name: p.Name, Kind: p.Kind, T3: p.T3}
	switch {
	case p.Scratch:
		r.Worktrees = false
	case p.kind != nil:
		r.Worktrees = p.kind.worktrees()
	default:
		r.Worktrees = p.Git != nil || p.Remote
	}
	if p.owner != nil { // already in T3: nothing to do there
		r.Last = ix.store.Last[p.T3]
		_, r.Project, _ = strings.Cut(p.T3, "/")
		return r, nil
	}
	if p.T3 == "" {
		if p.Remote || p.host == nil {
			return nil, fmt.Errorf("%s isn't on this machine", p.Name)
		}
		if !p.Exists {
			return nil, fmt.Errorf("%s doesn't exist", p.Display)
		}
		machine := ix.t3.Me()
		if p.kind != nil {
			machine = ix.machineOf(p.kind)
		}
		r.Tracked, r.Skipped = ix.tracks(p)
		id, err := ix.t3.Add(p.host, machine, p.Path, r.Tracked)
		if err != nil {
			return nil, err
		}
		r.Added, r.Project = true, id
		// tr3x names the environment too, which its composer needs.
		if list, err := ix.t3.Projects(); err == nil {
			for _, tp := range list {
				if tp.projectID() == id {
					r.T3 = tp.ID
				}
			}
		}
		p.T3 = r.T3
		if p.Status == "closed" {
			ix.store.meta(p.Key).Status = ""
		}
	}
	if r.T3 != "" {
		_, r.Project, _ = strings.Cut(r.T3, "/")
		r.Last = ix.store.Last[r.T3]
	}
	return r, nil
}

var badName = regexp.MustCompile(`[/\\:*?"<>|]`)

// create makes a new place of kind k called name, from the kind's template,
// and makes it a T3 project.
func (ix *Index) create(k *Kind, name string, sev int) (*Resolved, error) {
	name = strings.TrimSpace(name)
	switch {
	case !k.Create:
		return nil, fmt.Errorf("%s isn't a kind t3p creates (set create = true)", k.Plural)
	case len(k.Roots) == 0:
		return nil, fmt.Errorf("kind %s has no roots", k.Name)
	case name == "" || strings.HasPrefix(name, ".") || badName.MatchString(name):
		return nil, fmt.Errorf("%q can't be a folder name", name)
	case k.re != nil && !k.re.MatchString(name):
		return nil, fmt.Errorf("a %s's name matches %s; %q doesn't", k.Name, k.Match, name)
	}
	if sev != 0 && len(k.cadence) > 0 {
		if _, ok := k.cadence[sev]; !ok {
			return nil, fmt.Errorf("severity %d isn't one of %v", sev, k.severities())
		}
	}
	root := k.host.Expand(k.Roots[0])
	dest := joinOn(k.host, root, name)
	template := ""
	if k.Template != "" {
		template = k.host.Expand(k.Template)
		if !k.host.IsDir(template) {
			template = ""
		}
	}
	if err := k.host.Copy(template, dest); err != nil {
		return nil, err
	}
	now := ix.now
	m := ix.store.meta(placeKey(k.host, dest))
	m.Created, m.LastUpdate, m.Sev = &now, &now, sev
	p := &Place{Key: placeKey(k.host, dest), Name: name, Kind: k.Name, Host: k.host.ID(), Path: dest,
		Exists: true, kind: k, host: k.host, Meta: *m}
	ix.Places = append(ix.Places, p)
	return ix.resolve(p)
}

// close marks p closed, so it leaves the picker, and takes it off T3's list
// when T3 allows. Nothing is deleted: the folder is never touched, and t3p
// never passes --force, so T3 refuses to remove a project that has threads
// (archived ones too). Then T3 keeps it, and close says so.
func (ix *Index) close(p *Place) (string, error) {
	if p.Scratch {
		return "", fmt.Errorf("No project is T3's own place for threads without a project; it can't be closed")
	}
	if p.Remote || p.host == nil {
		if p.T3 != "" {
			return "", fmt.Errorf("%s is on %s; close it there", p.Name, p.Machine)
		}
		return "", fmt.Errorf("%s isn't on this machine", p.Name)
	}
	msg := "Closed " + p.Name + "."
	if p.T3 != "" {
		_, id, _ := strings.Cut(p.T3, "/")
		err := ix.t3.Remove(p.host, id)
		switch {
		case err == nil:
			msg = "Closed " + p.Name + " and took it off T3's list. Its folder is untouched."
			delete(ix.store.Last, p.T3)
		case strings.Contains(err.Error(), "is not empty"):
			msg = "Closed " + p.Name + " in t3p. T3 keeps the project because it has threads; nothing was removed."
		default:
			return "", err
		}
	}
	ix.store.meta(p.Key).Status = "closed"
	return msg, nil
}

// ---- Tidy Up -------------------------------------------------------------

type Fix struct {
	ID    string `json:"id"` // add, park, close, open, updated, ignore
	Title string `json:"title"`
}

type Finding struct {
	Check string `json:"check"`
	Key   string `json:"key"`
	Name  string `json:"name"`
	Text  string `json:"text"`
	Fixes []Fix  `json:"fixes"`
}

var fixTitles = map[string]string{
	"add":     "Add to T3",
	"park":    "Park it",
	"close":   "Close it (out of the picker; off T3's list if it has no threads; nothing is deleted)",
	"open":    "Reopen it",
	"updated": "I've just sent an update",
	"ignore":  "Leave it, and don't ask again",
}

func fixes(ids ...string) []Fix {
	out := make([]Fix, len(ids))
	for i, id := range ids {
		out[i] = Fix{ID: id, Title: fixTitles[id]}
	}
	return out
}

// checks are what Tidy Up looks for, most pressing first.
func (ix *Index) checks(reach bool) []Finding {
	var out []Finding
	raise := func(p *Place, check, text string, f []Fix) {
		if !p.ignores(check) {
			out = append(out, Finding{Check: check, Key: p.Key, Name: p.Name, Text: text, Fixes: f})
		}
	}
	local := []*Place{}
	for _, p := range ix.Places {
		if p.T3 != "" && !p.Remote && p.Exists {
			local = append(local, p)
		}
	}
	for _, p := range ix.Places {
		if p.Offline || p.Scratch {
			continue // an old list, or T3's own scratch: nothing to tidy
		}
		open := p.status() == "open"
		switch {
		case p.Overdue:
			raise(p, "overdue", fmt.Sprintf("update overdue by %s (Sev %d)", span(ix.now.Sub(*p.Due)), p.Sev),
				fixes("updated", "park"))
		case p.T3 != "" && !p.Remote && !p.Exists:
			raise(p, "orphan", "in T3, but its folder is gone: "+p.Display, fixes("close", "ignore"))
		}
		if p.Kind != "" && p.T3 == "" && p.Exists && open {
			raise(p, "untracked", "a "+p.Kind+" T3 doesn't have", fixes("add", "park", "ignore"))
		}
		if p.T3 != "" && open && p.Last != nil && ix.now.Sub(*p.Last) > ix.cfg.stale {
			raise(p, "stale", "nothing for "+span(ix.now.Sub(*p.Last)), fixes("park", "close", "ignore"))
		}
		if p.T3 != "" && !p.Remote && p.Exists {
			n := 0
			for _, q := range local {
				if q != p && !q.Scratch && q.Owner == p.Owner && q.Host == p.Host && within(q.Path, p.Path) {
					n++
				}
			}
			if n > 0 {
				others := "others"
				if n == 1 {
					others = "other"
				}
				raise(p, "nested", fmt.Sprintf("a T3 project holding %d %s; a thread in any folder inside it lands here", n, others),
					fixes("close", "ignore"))
			}
		}
		if reach && open && p.kind != nil && p.owner == nil && len(p.kind.Track) > 0 && p.T3 != "" {
			if _, skipped := ix.tracks(p); len(skipped) > 0 {
				raise(p, "unreachable", "can't reach "+strings.Join(skipped, ", "), fixes("ignore"))
			}
		}
	}
	order := map[string]int{"overdue": 0, "orphan": 1, "unreachable": 3, "nested": 4, "stale": 5, "untracked": 6}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].Check] < order[out[j].Check] })
	return out
}

func ago(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	return span(d) + " ago"
}

// span is a duration in words: "3h", "5d", "2mo".
func span(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	}
	return fmt.Sprintf("%dy", int(d.Hours()/24/365))
}
