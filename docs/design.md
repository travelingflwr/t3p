# t3p, next: topics, threads and projects in Rex

Status: draft for discussion · 2026-11-26

## Why

tr3x mirrors T3 Code into one Rex session. It looks good, but it breaks in ways that are expensive to fix:

- state goes wrong after a wake, a server restart or an update;
- fixes go through someone else's repo and PR process;
- everything lands in a single session.

Rex now resumes Claude Code and Codex by itself after a restart (the [Program Resume Protocol](https://www.superlogical.com/rex/docs/build/program-resume) and its built-in `claude` and `codex` resume rules). The agents' own interfaces in a Rex tab are good enough. What T3 still adds is managing projects and threads. t3p already does half of that and is our own code, so t3p grows into the whole thing, and T3 and tr3x leave this setup.

## Goals

- **iPad and Mac are equal.** Same look and same flow. The iPad gets extra care for streamlining.
- **Survive a restart, sleep or update with nothing to clean up.**
- **Session per topic,** with 2–3 long-lived topics running at once.
- **Projects and threads are easy to start, find, move and tidy.**
- **Move a thread** to another machine, and later to another agent.
- **Small and fully ours.** Rex will keep changing, so lean on what Rex provides and keep our layer thin.

## Not in scope

- Work Claude, which stays on T3 Code.
- A custom conversation renderer. Claude Code's interface is used as it is.
- The iPhone.

## Concepts

| Term | What it is | In Rex |
|---|---|---|
| **Topic** | A long-lived area of work, such as Admin, Rex or Mac. Holds any mix of agents and tabs. | A session with the topic's name, on each machine where it has threads. |
| **Thread** | One agent conversation (Claude Code or Codex) started in a project folder. | One tab in the topic's session. |
| **Project** | A folder of work: a repository, a case, a scratch area. | t3p's existing places and kinds. |
| **Machine** | Where a thread runs. **Nova** is home and **mallard** is overflow. | A Rex host. |

A topic can have threads on both machines. On each machine they sit in a session with the same name, so the topic "mirrors" across machines.

## How it fits together

```
iPad / Mac Rex app
   │  palette actions (t3p.lua)            Composer → types into the agent
   ▼
Rex server on Nova ───────────── Rex server on mallard
   │  sessions = topics                    │  sessions = topics (mirrors)
   │  tabs = threads running claude/codex  │
   │  OSC 7577 resume after restarts       │
   ▼                                       ▼
t3p on Nova (home: registry)  ◄── ssh ──  t3p on mallard (owns its places)
```

- **Rex** owns sessions, tabs and resuming. We never re-create what Rex restores.
- **The agents** own their conversations: Claude Code in `~/.claude/projects/<folder>/<id>.jsonl`, Codex in its own store.
- **t3p** owns the registry: which topics exist, which threads belong to them, each thread's project, machine, agent and conversation ID, and its status (active, idle or archived). The registry is a cache rebuilt by **reconciling** against Rex (`rex ls` on each host) and the agents' conversation files. It is never the source of truth for what's running, which is what tr3x got wrong.
- **t3p on Nova is the home registry.** Each machine's t3p keeps owning its own places, as today, and answers Nova over SSH.

## What it does

| Action | First version | What happens |
|---|---|---|
| **New Thread…** | ✓ | Pick a topic (or a new one), a project, an agent and a machine (Nova by default). Opens a tab in the topic's session there, running the agent in the project folder, and records it. |
| **Go to Thread…** | ✓ | Lists threads across topics and machines with status and last activity, and jumps to one. |
| **Resume** | ✓ (Rex does it) | After a restart, Rex reruns `claude --resume <id>` in the same tab. t3p reconciles and changes nothing. |
| **Move Thread…** | ✓ | Another machine: open a tab in the topic's session there running `atuin resume` for the thread, then archive the original. Fallback if Atuin can't: copy the conversation file and run `claude --resume`. |
| **Tidy Up…** | ✓ | t3p's existing tidy-up, extended to threads. See below. |
| **Hand Off…** | later, maybe early | Another agent: `atuin resume` forked into Codex (or back) in a new tab of the same topic. It might come almost free with Move. |
| **Map** | later | A picture of topics → threads → projects (Mermaid-style, drawn with kitty graphics) to see what's out there. |
| **Overflow** | later | Suggest or pick mallard when Nova is busy, based on load. |
| **Drafts** | later | Keep an unsent reply. See Atuin below. |

## Cleanup: archive, never delete

A thread is a candidate when any of these hold, with the thresholds configurable:

- no activity for N days (t3p already has `stale_after`);
- its branch is merged, or its worktree is gone;
- its project is closed or parked;
- its tab was closed while the conversation stayed.

**Archiving** closes the tab and keeps everything: the conversation file and the registry entry. An archived thread can be reopened with `--resume`. Nothing is ever deleted unless you ask.

**Needed now:** Claude Code deletes transcripts after `cleanupPeriodDays`, 30 days by default. For archiving to mean anything, raise that in `~/.claude/settings.json` on each machine, and check whether Codex prunes its sessions too.

## Atuin: use it for moving threads

Since 18.24, Atuin has **Threads** ([launch post](https://blog.atuin.sh/threads-agent-sessions/), Oct 2026) as well as shell history:

- **Capture.** It captures Claude Code, Codex and OpenCode sessions. Capture is opt-in.
- **`atuin resume`.** Resumes a session on another machine, or forks it into a different agent "with all context intact".
- **Sync.** End-to-end encrypted, through Atuin's server or a self-hosted one, or kept entirely local.
- **`atuin mcp`.** Lets agents search past sessions, including the reasoning behind decisions.

That is **Move Thread** and **Hand Off**, already built and maintained by someone else. So t3p shouldn't build its own:

- **Move Thread** and **Hand Off** call `atuin resume` in a new tab of the topic's session on the target machine. t3p keeps only the topic, project and status around it.
- **Archive** gets a second safety net. A thread captured by Atuin outlives Claude Code's own 30-day cleanup.
- **Search** across all threads, for you and for agents, comes from Atuin, not from t3p.
- **Sync.** Self-hosted on Nova, or Atuin's server. The data is end-to-end encrypted either way.

Learned by installing 18.25.1 on Nova and mallard (2026-11-26):

- **Capture** uses agent hooks (`atuin hook install claude`, which adds a `PreToolUse`/`PostToolUse` hook to `~/.claude/settings.json`), and `atuin threads import` brings in older sessions. Nova imported 803 threads, mallard 23.
- **Scriptable:** `atuin resume <id or unique prefix>` resumes without the picker. `--print` gives the command instead of running it, which suits a Rex tab.
- **Cross-agent:** `--in codex` (or `claude-code`, and others) writes the thread out as a new session of that agent with the full history. Tool calls carry over as that agent's own where it has a similar tool, with notes where they don't map.
- **Branches:** a thread continued separately on two machines becomes branches. `--branch`, `--switch` and `--fork` choose between them.
- `atuin threads list | show | search | tail` covers what Go to Thread and search need.

- **Sync works** (Atuin's hosted server, account on both machines): mallard lists the same 798 threads as Nova.
- **Dry run, Nova → mallard:** `atuin resume <id> --print` on mallard rebuilt the Claude Code session from sync and printed `cd -- … && claude --resume <session id>`. The project folder `/Users/amyde/…` didn't exist there, so Atuin fell back to the current directory. **t3p maps the folder** (`/Users/amyde/X` → `/home/amyde/X`, cloning or creating it if missing) and runs the resume from inside it.

Still to test: a full move with a real turn on mallard, and `--in codex`.

Drafts aren't covered by Atuin. They stay in "later".

## Moving off tr3x

1. Build the first version next to tr3x. Both can run at once.
2. Move one topic at a time. For an existing T3 thread you want to keep, open it in T3 one last time and start a fresh thread with a handoff summary.
3. When nothing is left in T3, remove tr3x (`tr3x uninstall`) and the plain rex-projects `projects.lua`, so there's one "new session in project" action.
4. Update T3 on its own schedule, or stop updating it, since this setup no longer depends on it.

## Clearing out the cruft

What Rex and Atuin now do natively, remove instead of maintaining:

- **rexurrect** (cemcatik's session save and restore). The Rex server now restores sessions, tabs and panes after a restart, and resumes Claude Code. rexurrect's `auto-restore` also recreates sessions on top of that, which probably caused yesterday's "copy of the session made after Rex restarted". Remove:
  1. `rexurrect autosave uninstall` (the launchd agent saving every 15 minutes);
  2. the `rexurrect auto-restore` line at the top of `~/.config/rex/init.lua`, and the `rexurrect report claude` hook in `~/.claude/settings.json` (Rex's own `rex __internal session-start` hook stays, since it's what reports Claude Code for resuming);
  3. `brew uninstall rexurrect`, keeping `~/.local/state/rexurrect` as an archive.
- **tr3x** and **rex-projects** (`projects.lua`), as in *Moving off tr3x* above.
- **Old `init.lua` copies** in `~/.config/rex` (`.bak-*`, `.before-*`): move them into one archive folder.
- **Everything else `init.lua` loads** (lazyrex, heypad, resh, rexpad, Glance and others): review it once. Keep what you use, archive the rest.

Do this as its own step, with `rex config check` and a Rex restart test after each removal. That way a broken `init.lua` can't silently turn off resuming: Rex trusts nothing when the config fails to load.

## Risks and open questions

- **Go to Session across hosts:** Rex can't yet select a session on another host (a feature request in the Rex feedback). Jumping to a mallard thread from Nova may need the sessions bar for now.
- **Two sessions with the same name:** the iPad's sessions bar shows topics per host. Do mirrored topics read as one topic, or as clutter? Try it with one topic first.
- **A turn running when the server restarts is stopped.** The conversation resumes, but the work in progress doesn't continue.
- **Path differences** between Nova (macOS, `/Users/amyde`) and mallard (Linux, `/home/amyde`): Claude Code files conversations by folder path, so a move has to map one home folder to the other. Check whether `atuin resume` handles this.
- **Depending on Atuin:** a second moving part we don't own. Keep the calls to it in one place in t3p, so switching to the file-copy fallback stays easy.
- **Rex keeps changing:** keep each Rex-facing piece small, and keep it in `t3p.lua` or one Go file.

## Order of work

0. **Atuin spike.** ~~Install 18.24+ on Nova and mallard, turn on Threads, sync between the two, and resume one specific thread from a script.~~ Done 2026-11-26. Left: a full move to mallard with a real turn, and `--in codex`.
0b. **Clear out the cruft** (above). rexurrect was removed from Nova, mallard, vesta and nucleus on 2026-11-26: its autosave, `init.lua` line, Claude hook and zsh plugin are gone, the binary was uninstalled on Nova and archived on the Linux machines, and its snapshots are kept. The rest is still to do.
1. Registry and reconcile: read what's in Rex and the agents' files on both machines, then show it in **Go to Thread**.
2. **New Thread** with topics as sessions, Nova only.
3. Mallard as a target, and **Move Thread**.
4. **Tidy Up** for threads (archive) and the `cleanupPeriodDays` setting.
5. Later: Hand Off, Map, Overflow, Drafts.

Each step is small enough to try on the iPad before the next one.
