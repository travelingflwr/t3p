# t3p

Your work in [T3 Code](https://github.com/pingdotgg/t3code), by kind, from [Rex](https://www.superlogical.com/rex)'s palette.

You decide what your work is called. One machine might have **projects** (repositories under `~/Projects`), and another **cases** (folders named by case number, made from a template, with due dates by severity and a customer upload share they also work in). t3p lists all of them together. For each one it shows what it is and where it stands:

- **Its note, or failing that the first line of its README.** That's the "what was this again?" answer.
- **Git:** branch, uncommitted changes, ahead and behind.
- **Last activity:** whichever is newer, the latest T3 thread or the latest commit.
- **Status and due dates:** open, parked or closed, plus severity and when the next update is due, for kinds that track those.

It also offers to fix what's drifted: projects T3 doesn't have, T3 projects whose folder is gone, projects nobody has touched in months, a project that swallows the projects inside it, and cases that are overdue for an update.

t3p started as a T3-flavoured [rex-projects](https://github.com/almonk/rex-projects) and is meant to replace it. It also takes in the case ideas from casedeck and caseload.

## In Rex

| Action | |
|---|---|
| **New T3 Thread…** `⌥⌘T` | Pick anything: what T3 has (on every machine tr3x follows), what it doesn't yet, or **Add Folder…**. A folder T3 doesn't have is added first. Then pick where to work (the folder, a worktree, or a new worktree; the last one used is listed first), and tr3x's composer opens for the first message. |
| **New *Kind*…** | For each kind with `create = true`: name it, pick a severity, and it's created from the template, added to T3 with its shares, and opened. |
| **Tidy Up Work…** | Each finding comes with its fixes: add to T3, park, close, mark an update as sent, or don't ask again. |
| **Manage Work…** | Describe, tag, park, close, reopen, or set the severity. Closing takes it out of the picker, and off T3's list only if it has no threads. **t3p never deletes anything:** it never touches folders and never passes `--force` to T3, so T3 refuses to remove a project with threads, and t3p says so. |
| **Open Shell in…** | A plain shell session in the folder, which is what rex-projects did. |

## Install

```sh
go build -o ~/.local/bin/t3p .
ln -s "$PWD/t3p.lua" ~/.config/rex/t3p.lua
```

Then add this line to `~/.config/rex/init.lua`, *after* `require("tr3x")`:

```lua
pcall(require, "t3p")
```

`t3p doctor` checks the setup. t3p takes the tr3x path and environment the Lua file passes it, read from `tr3x.lua`, so it reaches T3 the same way tr3x does.

## Configure

You don't need a config to start: with none, there's one kind, `project`, in `~/Projects`. `t3p config init` writes an example to `~/.config/t3p/config.toml` (`%APPDATA%\t3p` on Windows):

```toml
stale_after = "90d"

[[kind]]
name  = "project"
roots = ["~/Projects"]
detect = "git"                       # git: repositories only. any: every folder.

[[kind]]
name     = "case"
host     = "wsl"                     # in this machine's WSL ("wsl:Ubuntu" for a named distro)
machine  = "…"                       # what T3 calls that environment
roots    = ["~/Claude_Work"]
detect   = "any"
match    = '^\d{6,}'
create   = true
template = "~/Claude_Work/.template"
track    = ["/mnt/t/upload/ftp/{number}"]   # given to T3 with --track; {name} and {number}
related  = "RELATED"                 # case numbers in this file get their shares tracked too
cadence  = { 1 = "24h", 2 = "48h", 3 = "5d", 4 = "7d" }
worktrees = false
```

Notes, tags, statuses and due dates are kept in `~/.local/state/t3p/places.json` (`%LOCALAPPDATA%\t3p` on Windows). That file stays on the machine, and nothing in it goes to T3.

## Other machines

Each machine runs its own t3p, and that t3p **owns the places whose folders are there**: their notes, statuses and due dates. Name the other machines in your config:

```toml
[[machine]]
name = "mallard"            # as T3 names it (tr3x envs)
ssh  = "mallard"            # the ssh destination; default: name
t3p  = "~/.local/bin/t3p"   # t3p there; this is the default
```

To list everything, your t3p asks each machine's t3p for its places in one SSH call (`t3p export`) and merges them with what tr3x knows about that machine's T3. Changes are sent to the machine that owns the place, and that machine's t3p makes them: a note, park or close, adding a folder to T3, or `t3p new --machine mallard case 123`.

- **Nothing is copied between machines,** so nothing needs syncing, and work data stays on the machine it belongs to.
- **A machine that can't be reached** shows the last list it sent, marked *offline*. It's read-only until that machine is back.
- **Each other machine needs** t3p, the `t3` CLI, and `ssh <name>` working without a password. tr3x is only needed on the machine you work from. Each machine's own `~/.config/t3p/config.toml` names its kinds.

Install on a Linux machine from here:

```sh
GOOS=linux GOARCH=amd64 go build -o t3p-linux . && scp t3p-linux mallard:.local/bin/t3p
t3p doctor        # checks each machine too
```

## Mac, Linux and Windows

- **Mac and Linux:** every kind runs on the local machine unless its config says otherwise.
- **Windows:** t3p is a native Windows binary, like T3's and Rex's Windows apps. A kind with `host = "wsl"` is reached the way T3's desktop app reaches its WSL backend. Every command for it (folder listing, git, `t3 project add`) runs inside the distro through `wsl.exe -d <distro> -- bash -lc …`, with arguments passed as arguments and never pasted into a script. t3p never walks the slow `\\wsl.localhost` share. Paths are shown the way Windows opens them (`\\wsl.localhost\Ubuntu\home\…`).
- **Shares:** t3p checks whether a tracked share can be reached, with a timeout. It never lists what's in it.

On Windows, WSL works from the tests and the cross-compile only; it hasn't been run on a real VDI. tr3x is Mac and Linux only today, and t3p uses it to list T3's projects and open the composer. Without tr3x, t3p can still list folders and add them to T3, using the `t3` CLI.

## Command line

```
t3p ls [--kind K] [--all] [--json]     everything, most pressing first
t3p check [--json] [--reach]           what needs tidying up
t3p resolve [--kind K] <place|folder>  make it a T3 project if needed
t3p new <kind> <name> [--sev N]
t3p note <place> [text…]   t3p tag <place> +a -b
t3p park | open | close <place>        t3p sev <place> N   t3p updated <place>
t3p kinds   t3p browse   t3p config [init]   t3p doctor
```

## Tests

```sh
go test ./...                               # fake tr3x and t3, temporary home folders
GOOS=windows GOARCH=amd64 go build -o /dev/null .
luajit legacy/run_test.lua                  # the first Lua-only version, kept for reference
```

## License

MIT. See [LICENSE](LICENSE).
