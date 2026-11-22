package main

// A host is where a kind's folders are: this machine, or a WSL distro seen
// from Windows. Everything for a WSL kind runs inside the distro through
// wsl.exe, the way T3's own desktop app reaches its WSL backend, so t3p never
// walks the slow \\wsl.localhost share.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type Folder struct {
	Path string
	Git  bool
}

type Host interface {
	ID() string             // "local", "wsl", "wsl:Ubuntu"
	Home() string           // the home folder there
	Expand(p string) string // ~ and cleaning, in that host's spelling
	Run(timeout time.Duration, argv ...string) (string, error)
	Folders(root string) ([]Folder, error) // the folders directly inside root, hidden ones left out
	IsDir(p string) bool
	Reachable(p string, timeout time.Duration) bool // IsDir, given up on after timeout
	ReadHead(p string, max int) string              // the start of a file, "" when unreadable
	Copy(from, to string) error                     // a folder, or an empty one when from is ""
	Display(p string) string                        // how a person here reads the path
}

func newHost(spec string) (Host, error) {
	spec = strings.TrimSpace(spec)
	switch {
	case spec == "" || spec == "local":
		return localHost{}, nil
	case spec == "wsl":
		return &wslHost{run: runProcess}, nil
	case strings.HasPrefix(spec, "wsl:"):
		return &wslHost{distro: strings.TrimPrefix(spec, "wsl:"), run: runProcess}, nil
	}
	return nil, fmt.Errorf("host is local, wsl or wsl:<distro>, not %q", spec)
}

// runProcess runs argv and returns its output, or an error carrying the first
// line it printed to stderr.
func runProcess(timeout time.Duration, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return out.String(), fmt.Errorf("%s: timed out after %s", filepath.Base(argv[0]), timeout)
	}
	if err != nil {
		msg := firstLine(errb.String())
		if msg == "" {
			msg = firstLine(out.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New(msg)
	}
	return out.String(), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// ---- this machine ----------------------------------------------------------

type localHost struct{}

func (localHost) ID() string   { return "local" }
func (localHost) Home() string { return homeDir() }

func (localHost) Expand(p string) string {
	p = trimQuotes(p)
	if p == "~" {
		return homeDir()
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = filepath.Join(homeDir(), p[2:])
	}
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

func (localHost) Run(timeout time.Duration, argv ...string) (string, error) {
	return runProcess(timeout, argv...)
}

func (localHost) Folders(root string) ([]Folder, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []Folder
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(root, e.Name())
		if !e.IsDir() {
			// A symlink to a folder counts.
			if e.Type()&fs.ModeSymlink == 0 {
				continue
			}
			if st, err := os.Stat(full); err != nil || !st.IsDir() {
				continue
			}
		}
		_, gerr := os.Stat(filepath.Join(full, ".git"))
		out = append(out, Folder{Path: full, Git: gerr == nil})
	}
	return out, nil
}

func (localHost) IsDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func (h localHost) Reachable(p string, timeout time.Duration) bool {
	done := make(chan bool, 1)
	go func() { done <- h.IsDir(p) }()
	select {
	case ok := <-done:
		return ok
	case <-time.After(timeout):
		return false
	}
}

func (localHost) ReadHead(p string, max int) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, max)
	n, _ := f.Read(buf)
	return string(buf[:n])
}

func (localHost) Copy(from, to string) error {
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("%s already exists", to)
	}
	if from == "" {
		return os.MkdirAll(to, 0o755)
	}
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, _ := d.Info()
		mode := fs.FileMode(0o644)
		if info != nil {
			mode = info.Mode().Perm()
		}
		return os.WriteFile(dest, data, mode)
	})
}

func (localHost) Display(p string) string { return shortHome(p, homeDir(), string(filepath.Separator)) }

// ---- a WSL distro, from Windows ------------------------------------------

type wslHost struct {
	distro string // "" is WSL's default distro
	home   string
	run    func(time.Duration, ...string) (string, error)
}

func (h *wslHost) ID() string {
	if h.distro == "" {
		return "wsl"
	}
	return "wsl:" + h.distro
}

// argv runs a program in the distro with its login shell's PATH, which is
// where t3 and git are. The program and its arguments go as arguments to
// `exec "$@"`, never into the script, so nothing needs quoting.
func (h *wslHost) argv(args ...string) []string {
	a := []string{"wsl.exe"}
	if h.distro != "" {
		a = append(a, "-d", h.distro)
	}
	a = append(a, "--", "bash", "-lc", `exec "$@"`, "t3p")
	return append(a, args...)
}

// script runs a small sh script in the distro, with args as $1, $2, ….
func (h *wslHost) script(timeout time.Duration, body string, args ...string) (string, error) {
	a := []string{"wsl.exe"}
	if h.distro != "" {
		a = append(a, "-d", h.distro)
	}
	a = append(a, "--exec", "sh", "-c", body, "t3p")
	return h.run(timeout, append(a, args...)...)
}

func (h *wslHost) Home() string {
	if h.home == "" {
		out, err := h.script(15*time.Second, `printf %s "$HOME"`)
		if err == nil {
			h.home = strings.TrimSpace(out)
		}
	}
	return h.home
}

func (h *wslHost) Expand(p string) string {
	p = strings.ReplaceAll(trimQuotes(p), `\`, "/")
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = h.Home() + p[1:]
	}
	if p == "" {
		return ""
	}
	return path.Clean(p)
}

func (h *wslHost) Run(timeout time.Duration, argv ...string) (string, error) {
	return h.run(timeout, h.argv(argv...)...)
}

func (h *wslHost) Folders(root string) ([]Folder, error) {
	// One call lists every folder and says which are repositories: one
	// wsl.exe start costs more than the whole listing.
	out, err := h.script(30*time.Second, `cd "$1" || exit 1
for d in */; do
  [ -d "$d" ] || continue
  d=${d%/}
  if [ -e "$d/.git" ]; then g=1; else g=0; fi
  printf '%s\t%s\n' "$g" "$d"
done`, root)
	if err != nil {
		return nil, err
	}
	var list []Folder
	for _, line := range strings.Split(out, "\n") {
		g, name, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if !ok || name == "" {
			continue
		}
		list = append(list, Folder{Path: path.Join(root, name), Git: g == "1"})
	}
	return list, nil
}

func (h *wslHost) IsDir(p string) bool {
	_, err := h.script(15*time.Second, `test -d "$1"`, p)
	return err == nil
}

func (h *wslHost) Reachable(p string, timeout time.Duration) bool {
	_, err := h.script(timeout+5*time.Second, `timeout "$2" test -d "$1"`, p,
		fmt.Sprintf("%d", int(timeout.Seconds()+0.5)))
	return err == nil
}

func (h *wslHost) ReadHead(p string, max int) string {
	out, err := h.script(15*time.Second, `head -c "$2" "$1"`, p, fmt.Sprint(max))
	if err != nil {
		return ""
	}
	return out
}

func (h *wslHost) Copy(from, to string) error {
	if from == "" {
		_, err := h.script(15*time.Second, `[ ! -e "$1" ] || { echo "$1 already exists" >&2; exit 1; }; mkdir -p "$1"`, to)
		return err
	}
	_, err := h.script(60*time.Second, `[ ! -e "$2" ] || { echo "$2 already exists" >&2; exit 1; }
mkdir -p "$(dirname "$2")" && cp -R "$1" "$2"`, from, to)
	return err
}

// Display is the path as Windows reaches it, which Explorer and editors open.
func (h *wslHost) Display(p string) string {
	if h.distro == "" {
		return "wsl:" + shortHome(p, h.home, "/")
	}
	return `\\wsl.localhost\` + h.distro + strings.ReplaceAll(p, "/", `\`)
}

// ---- helpers ---------------------------------------------------------------

func trimQuotes(p string) string {
	p = strings.TrimSpace(p)
	if len(p) >= 2 && (p[0] == '"' && p[len(p)-1] == '"' || p[0] == '\'' && p[len(p)-1] == '\'') {
		p = p[1 : len(p)-1]
	}
	return p
}

// shortHome writes home at the start of p as ~.
func shortHome(p, home, sep string) string {
	if home != "" && strings.HasPrefix(p, home+sep) {
		return "~/" + strings.ReplaceAll(p[len(home)+1:], `\`, "/")
	}
	if p == home && home != "" {
		return "~"
	}
	return p
}

// within says whether p is root or inside it.
func within(p, root string) bool {
	if p == root {
		return true
	}
	sep := "/"
	if strings.Contains(root, `\`) {
		sep = `\`
	}
	return strings.HasPrefix(p, strings.TrimSuffix(root, sep)+sep)
}
