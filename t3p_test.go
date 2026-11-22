package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// world is a fake machine: a home with folders, a fake tr3x that lists the
// projects in $T3P_FAKE/projects.json, and a fake t3 that logs its arguments
// to $T3P_FAKE/t3.log and adds projects.
type world struct {
	t    *testing.T
	home string
	fake string
}

func newWorld(t *testing.T) *world {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tools are shell scripts")
	}
	w := &world{t: t, home: t.TempDir(), fake: t.TempDir()}
	t.Setenv("HOME", w.home)
	t.Setenv("T3P_CONFIG_DIR", filepath.Join(w.home, ".config", "t3p"))
	t.Setenv("T3P_STATE_DIR", filepath.Join(w.home, ".local", "state", "t3p"))
	w.script("tr3x", `case "$1" in
  projects) cat "`+w.fake+`/projects.json" ;;
  envs) printf 'Mac  this Mac  ok\notherbox  over SSH  ok\n' ;;
  *) exit 1 ;;
esac`)
	w.script("t3", `echo "$*" >> "`+w.fake+`/t3.log"
if [ "$1 $2" = "project add" ]; then
  for last; do :; done
  if grep -q "\"$last\"" "`+w.fake+`/exists" 2>/dev/null; then echo "[1] ERROR (#2): ProjectAlreadyExistsError: An active project already exists" >&2; exit 1; fi
  echo "Added project new1 (x) at $last."
  printf '[{"id":"envA/new1","title":"x","subtitle":"%s · Mac","detail":"just now"}]' "$last" > "`+w.fake+`/projects.json"
  exit 0
fi
if [ "$1 $2" = "project remove" ] && [ "$3" = busy ]; then echo "[1] ERROR (#2): ProjectNotEmptyError: Project busy is not empty." >&2; exit 1; fi
if [ "$1 $2" = "project remove" ]; then echo "Removed project $3."; exit 0; fi
exit 1`)
	t.Setenv("T3P_TR3X", filepath.Join(w.fake, "tr3x"))
	t.Setenv("T3P_T3", filepath.Join(w.fake, "t3"))
	w.projects(`[]`)
	return w
}

func (w *world) script(name, body string) {
	p := filepath.Join(w.fake, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) projects(json string) {
	os.WriteFile(filepath.Join(w.fake, "projects.json"), []byte(json), 0o644)
}

func (w *world) dir(rel string, git bool) string {
	p := filepath.Join(w.home, rel)
	os.MkdirAll(p, 0o755)
	if git {
		os.MkdirAll(filepath.Join(p, ".git"), 0o755)
	}
	return p
}

func (w *world) config(toml string) {
	os.MkdirAll(filepath.Join(w.home, ".config", "t3p"), 0o755)
	os.WriteFile(filepath.Join(w.home, ".config", "t3p", "config.toml"), []byte(toml), 0o644)
}

func (w *world) t3log() string {
	b, _ := os.ReadFile(filepath.Join(w.fake, "t3.log"))
	return string(b)
}

func (w *world) index() (*env, *Index) {
	e, err := setup()
	if err != nil {
		w.t.Fatal(err)
	}
	return e, e.index(false)
}

func names(ps []*Place) string {
	var n []string
	for _, p := range ps {
		n = append(n, p.Name)
	}
	return strings.Join(n, ",")
}

// ---------------------------------------------------------------------------

func TestDefaultsToOneProjectKind(t *testing.T) {
	newWorld(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Found || len(cfg.Kinds) != 1 || cfg.Kinds[0].Name != "project" || cfg.Kinds[0].Plural != "projects" {
		t.Fatalf("defaults: %+v", cfg.Kinds[0])
	}
	if !cfg.Kinds[0].worktrees() {
		t.Fatal("a git kind offers worktrees")
	}
}

func TestConfigKinds(t *testing.T) {
	w := newWorld(t)
	w.config(`
stale_after = "2w"
[[kind]]
name = "case"
roots = ["~/Cases"]
match = '^\d{6,}'
cadence = { 1 = "24h", 3 = "5d" }
[[kind]]
name = "engagement"
plural = "engagements"
host = "wsl:Ubuntu"
roots = ["~/eng"]
`)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.kind("cases")
	if c == nil || c.Detect != "any" || c.worktrees() || c.cadence[3] != 5*24*time.Hour {
		t.Fatalf("case kind: %+v", c)
	}
	if got := c.severities(); len(got) != 2 || got[0] != 1 {
		t.Fatalf("severities %v", got)
	}
	if cfg.kind("engagement").host.ID() != "wsl:Ubuntu" || cfg.stale != 14*24*time.Hour {
		t.Fatal("host or stale_after")
	}
	w.config("[[kind]]\nname = \"x\"\nhost = \"vm\"\n")
	if _, err := loadConfig(); err == nil {
		t.Fatal("a bad host is refused")
	}
}

func TestDurationsAndAgo(t *testing.T) {
	for in, want := range map[string]time.Duration{"5d": 120 * time.Hour, "2w": 336 * time.Hour, "90m": 90 * time.Minute} {
		if got, err := parseDuration(in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	now := time.Now()
	if got := now.Sub(t3Ago("3w ago", now)); got != 21*24*time.Hour {
		t.Errorf("3w ago: %v", got)
	}
	if !t3Ago("", now).IsZero() || t3Ago("just now", now) != now {
		t.Error("empty and just now")
	}
}

func TestWSLHost(t *testing.T) {
	var calls [][]string
	h := &wslHost{distro: "Ubuntu", run: func(_ time.Duration, argv ...string) (string, error) {
		calls = append(calls, argv)
		if strings.Contains(strings.Join(argv, " "), `printf %s "$HOME"`) {
			return "/home/amy", nil
		}
		return "1\tapp\n0\tnotes\n", nil
	}}
	if got := h.Expand("~/Cases"); got != "/home/amy/Cases" {
		t.Fatalf("expand: %s", got)
	}
	folders, _ := h.Folders("/home/amy/Cases")
	if len(folders) != 2 || folders[0].Path != "/home/amy/Cases/app" || !folders[0].Git || folders[1].Git {
		t.Fatalf("folders: %+v", folders)
	}
	last := calls[len(calls)-1]
	if last[0] != "wsl.exe" || last[1] != "-d" || last[2] != "Ubuntu" || last[len(last)-1] != "/home/amy/Cases" {
		t.Fatalf("folders argv: %q", last)
	}
	// A program runs with its arguments as arguments, never in the script.
	argv := h.argv("t3", "project", "add", "/home/amy/it's here")
	if strings.Join(argv[:8], " ") != `wsl.exe -d Ubuntu -- bash -lc exec "$@" t3p` || argv[len(argv)-1] != "/home/amy/it's here" {
		t.Fatalf("run argv: %q", argv)
	}
	if got := h.Display("/home/amy/Cases/app"); got != `\\wsl.localhost\Ubuntu\home\amy\Cases\app` {
		t.Fatalf("display: %s", got)
	}
}

func TestIndexJoinsT3ByMachineAndExactPath(t *testing.T) {
	w := newWorld(t)
	w.dir("Projects/known", true)
	w.dir("Projects/fresh", true)
	w.dir("Projects/plain", false)
	w.projects(`[
	 {"id":"envA/p1","title":"known","subtitle":"~/Projects/known · Mac","detail":"1h ago"},
	 {"id":"envA/p0","title":"Projects","subtitle":"~/Projects · Mac","detail":"2h ago"},
	 {"id":"envB/r1","title":"fresh","subtitle":"~/Projects/fresh · otherbox","detail":"5m ago"},
	 {"id":"envA/gone","title":"gone","subtitle":"~/Projects/gone · Mac","detail":"9mo ago"}]`)
	_, ix := w.index()
	// Latest activity first (otherbox's fresh, 5m ago); T3's before the rest.
	if got := names(ix.Places); got != "fresh,known,Projects,gone,fresh" {
		t.Fatalf("order: %s", got)
	}
	byKey := map[string]*Place{}
	for _, p := range ix.Places {
		byKey[p.Key] = p
	}
	local := byKey[filepath.Join(w.home, "Projects/fresh")]
	if local == nil || local.T3 != "" {
		t.Fatal("otherbox's ~/Projects/fresh must not join this machine's")
	}
	if r := byKey["t3:envB/r1"]; r == nil || !r.Remote || r.Machine != "otherbox" {
		t.Fatalf("remote: %+v", r)
	}
	gone := byKey[filepath.Join(w.home, "Projects/gone")]
	if gone == nil || gone.Exists || gone.Kind != "project" {
		t.Fatalf("a T3 project in a root whose folder is gone: %+v", gone)
	}
}

func TestResolveAddsAFolderWithItsTracks(t *testing.T) {
	w := newWorld(t)
	share := w.dir("share", false)
	w.dir("share/123456", false)
	w.dir("share/777777", false)
	c := w.dir("Cases/123456_acme", false)
	os.WriteFile(filepath.Join(c, "RELATED"), []byte("# related\n777777\n888888\n"), 0o644)
	w.config(`[[kind]]
name = "case"
roots = ["~/Cases"]
match = '^\d{6}'
track = ["` + share + `/{number}"]
related = "RELATED"
`)
	e, ix := w.index()
	p, err := ix.one("123456_acme")
	if err != nil {
		t.Fatal(err)
	}
	r, err := ix.resolve(p)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Added || r.T3 != "envA/new1" || r.Worktrees {
		t.Fatalf("resolved: %+v", r)
	}
	if len(r.Tracked) != 2 || len(r.Skipped) != 1 || !strings.HasSuffix(r.Skipped[0], "888888") {
		t.Fatalf("tracks: %v skipped %v", r.Tracked, r.Skipped)
	}
	if !strings.Contains(w.t3log(), "project add --track "+share+"/123456 --track "+share+"/777777 "+c) {
		t.Fatalf("t3 args: %s", w.t3log())
	}
	_ = e
}

func TestResolveFindsAnExistingProject(t *testing.T) {
	w := newWorld(t)
	dir := w.dir("Projects/known", true)
	os.WriteFile(filepath.Join(w.fake, "exists"), []byte(`"`+dir+`"`), 0o644)
	// tr3x lists it only after a moment; until then resolve can't name it.
	_, ix := w.index()
	p, _ := ix.one("known")
	if _, err := ix.resolve(p); err == nil || !strings.Contains(err.Error(), "can't find its id") {
		t.Fatalf("want a retry message, got %v", err)
	}
	w.projects(`[{"id":"envA/p1","title":"known","subtitle":"~/Projects/known · Mac"}]`)
	_, ix = w.index()
	p, _ = ix.one("known")
	r, err := ix.resolve(p)
	if err != nil || r.Added || r.T3 != "envA/p1" || !r.Worktrees {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestCreateFromTemplateAndDue(t *testing.T) {
	w := newWorld(t)
	tpl := w.dir("Cases/.template", false)
	os.WriteFile(filepath.Join(tpl, "CLAUDE.md"), []byte("# Case\n"), 0o644)
	w.config(`[[kind]]
name = "case"
roots = ["~/Cases"]
match = '^\d{6}'
create = true
template = "~/Cases/.template"
cadence = { 1 = "24h", 2 = "48h" }
`)
	e, ix := w.index()
	k := e.cfg.kind("case")
	if _, err := ix.create(k, "notacase", 1); err == nil {
		t.Fatal("a name that doesn't match is refused")
	}
	if _, err := ix.create(k, "123456", 9); err == nil {
		t.Fatal("a severity the cadence doesn't name is refused")
	}
	r, err := ix.create(k, "123456", 2)
	if err != nil || !r.Added {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(w.home, "Cases/123456/CLAUDE.md")); err != nil {
		t.Fatal("the template is copied")
	}
	if _, err := ix.create(k, "123456", 2); err == nil {
		t.Fatal("an existing folder is refused")
	}
	e.store.save()

	// Two days later, a Sev 2 case is due; a bit later, overdue.
	e, ix = w.index()
	p, _ := ix.one("123456")
	if p.Due == nil || p.Overdue || p.Due.Sub(*p.Created) != 48*time.Hour {
		t.Fatalf("due: %v overdue %v", p.Due, p.Overdue)
	}
	ix.now = ix.now.Add(49 * time.Hour)
	ix.due(p)
	if !p.Overdue {
		t.Fatal("overdue after 49h")
	}
	if f := ix.checks(false); len(f) == 0 || f[0].Check != "overdue" {
		t.Fatalf("overdue comes first: %+v", f)
	}
}

func TestChecks(t *testing.T) {
	w := newWorld(t)
	w.dir("Projects/a", true)
	w.dir("Projects/b", true)
	w.projects(`[
	 {"id":"envA/root","title":"Projects","subtitle":"~/Projects · Mac","detail":"1h ago"},
	 {"id":"envA/a","title":"a","subtitle":"~/Projects/a · Mac","detail":"200d ago"},
	 {"id":"envA/gone","title":"gone","subtitle":"~/Projects/gone · Mac","detail":"1d ago"}]`)
	e, ix := w.index()
	got := map[string]string{}
	for _, f := range ix.checks(false) {
		got[f.Check+":"+f.Name] = f.Text
	}
	for _, want := range []string{"orphan:gone", "nested:Projects", "stale:a", "untracked:b"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if !strings.Contains(got["nested:Projects"], "holding 1 other;") {
		t.Errorf("nested counts the T3 projects inside: %q", got["nested:Projects"])
	}
	// ignore keeps a check quiet; close takes it off T3's list.
	p, _ := ix.one("b")
	e.store.meta(p.Key).Ignore = []string{"untracked"}
	gone, _ := ix.one("gone")
	if _, err := ix.close(gone); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.t3log(), "project remove gone") {
		t.Fatal("close runs t3 project remove")
	}
	e.store.save()
	_, ix = w.index()
	for _, f := range ix.checks(false) {
		if f.Check == "untracked" && f.Name == "b" {
			t.Fatal("ignored check came back")
		}
	}
}

func TestRemoteProjectsAreNotClosedHere(t *testing.T) {
	w := newWorld(t)
	w.projects(`[{"id":"envB/r","title":"far","subtitle":"~/x · otherbox"}]`)
	_, ix := w.index()
	p, _ := ix.one("far")
	if _, err := ix.close(p); err == nil || !strings.Contains(err.Error(), "otherbox") {
		t.Fatalf("close on another machine: %v", err)
	}
}

func TestReadmeLine(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Title\n\n[![badge](x)](y)\n<p>html</p>\n\nA **terminal** [control room](z) for cases.\n"), 0o644)
	if got := readmeLine(localHost{}, dir); got != "A terminal control room for cases." {
		t.Fatalf("%q", got)
	}
	if got := clip(strings.Repeat("word ", 40), 20); len([]rune(got)) > 21 || !strings.HasSuffix(got, "…") {
		t.Fatalf("clip: %q", got)
	}
}

func TestArgs(t *testing.T) {
	a := parseArgs([]string{"x", "--kind", "case", "--json", "y", "--sev=2"}, "json")
	if strings.Join(a.pos, ",") != "x,y" || a.flags["kind"] != "case" || !a.has("json") || a.flags["sev"] != "2" {
		t.Fatalf("%+v", a)
	}
}

func TestMkdir(t *testing.T) {
	w := newWorld(t)
	parent := w.dir("Projects", false)
	e, _ := w.index()
	if err := mkdirCmd(e, parseArgs([]string{"--git", parent, "fresh idea"}, "git")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(parent, "fresh idea", ".git")); err != nil {
		t.Fatal("--git makes a repository")
	}
	if err := mkdirCmd(e, parseArgs([]string{parent, "fresh idea"})); err == nil {
		t.Fatal("an existing folder is refused")
	}
	for _, bad := range []string{".hidden", "a/b", "..", " "} {
		if err := mkdirCmd(e, parseArgs([]string{parent, bad})); err == nil {
			t.Errorf("%q is refused", bad)
		}
	}
	if err := mkdirCmd(e, parseArgs([]string{parent, "plain"})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(parent, "plain", ".git")); err == nil {
		t.Fatal("without --git, a plain folder")
	}
}

// A project with threads: T3 refuses to remove it without --force, which t3p
// never passes. t3p closes it in its own list and says T3 kept it.
func TestCloseNeverForcesAndNeverDeletes(t *testing.T) {
	w := newWorld(t)
	dir := w.dir("Projects/busy", true)
	os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644)
	w.projects(`[{"id":"envA/busy","title":"busy","subtitle":"~/Projects/busy · Mac","detail":"1d ago"}]`)
	e, ix := w.index()
	p, _ := ix.one("busy")
	msg, err := ix.close(p)
	if err != nil || !strings.Contains(msg, "T3 keeps the project") {
		t.Fatalf("%q %v", msg, err)
	}
	if strings.Contains(w.t3log(), "--force") {
		t.Fatal("never --force")
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatal("the folder is untouched")
	}
	e.store.save()
	_, ix = w.index()
	p, _ = ix.one("busy")
	if p.status() != "closed" || len(filterPlaces(ix, "", false)) != 0 {
		t.Fatal("closed in t3p, out of the picker")
	}
	for _, f := range ix.checks(false) {
		if f.Name == "busy" {
			t.Fatalf("a closed project T3 kept isn't nagged about: %+v", f)
		}
	}
}

func TestLatestThreadFallback(t *testing.T) {
	w := newWorld(t)
	w.dir("Projects/quiet", true)
	w.dir("Projects/told", true)
	os.WriteFile(filepath.Join(w.home, "Projects/told/README.md"), []byte("Has a README.\n"), 0o644)
	w.projects(`[{"id":"envA/q","title":"quiet","subtitle":"~/Projects/quiet · Mac"},
	 {"id":"envA/t","title":"told","subtitle":"~/Projects/told · Mac"}]`)
	// Newest first: system text, a parent project's thread, then the real one.
	w.script("t3", `echo "$*" >> "`+w.fake+`/t3.log"
echo '[{"projectId":"q","title":"<recommended_plugins>"},{"projectId":"parent","title":"Not this one"},{"projectId":"q","title":"Jeep Repair Planning"}]'`)
	e, _ := w.index()
	ix := e.index(true)
	q, _ := ix.one("quiet")
	if q.Thread != "Jeep Repair Planning" || whatItIs(q) != "Last thread: Jeep Repair Planning" {
		t.Fatalf("thread %q, what %q", q.Thread, whatItIs(q))
	}
	told, _ := ix.one("told")
	if told.Thread != "" || whatItIs(told) != "Has a README." {
		t.Fatal("a README line wins, and saves the call")
	}
	if strings.Count(w.t3log(), "thread list") != 1 {
		t.Fatalf("only places with nothing to say are asked: %s", w.t3log())
	}
}

func TestGitOnlyForARepositorysTopFolder(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	top := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", top).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	inner := filepath.Join(top, "cases", "123")
	os.MkdirAll(inner, 0o755)
	if g := gitInfo(localHost{}, top); g == nil {
		t.Fatal("the top folder has git state")
	}
	if g := gitInfo(localHost{}, inner); g != nil {
		t.Fatalf("a folder inside it doesn't: %+v", g)
	}
	if g := gitInfo(localHost{}, t.TempDir()); g != nil {
		t.Fatal("nor a folder outside any repository")
	}
}
