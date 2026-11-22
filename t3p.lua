-- t3p in Rex: your work in T3 Code, by kind, in the palette.
--
-- The work happens in the t3p binary (which kinds of work this machine has,
-- what T3 has, git, notes, due dates). This file only asks and shows:
--
--   New T3 Thread…          (⌥⌘T) pick anything, or add a folder, then where
--                           to work; tr3x's composer opens for the first message
--   New <Kind>…             for each kind with create = true (a case, say)
--   Tidy Up Work…           what's stale, untracked, orphaned, overdue
--   Manage Work…            describe, tag, park, close, reopen, severity
--   Open Shell in…          a plain shell session in a place's folder
--
-- Install: put this file next to init.lua, after require("tr3x"):
--
--     require("t3p")
--
-- Optional: require("t3p").setup{ key = "alt+cmd+t", bin = "~/.local/bin/t3p" }
-- Written for Lua 5.1 (what Rex embeds). Every command runs through rex.exec
-- with an argument list, never a shell.

local M = {}

local has_ui, ui = pcall(function() return rex.ui end)
if not has_ui or ui == nil then return M end

local config = { key = "alt+cmd+t", bin = nil, tr3x_config = nil }
local bound_key = nil

local is_windows = (package and package.config or "/"):sub(1, 1) == "\\"

local function getenv(name)
  if os == nil or os.getenv == nil then return nil end
  local v = os.getenv(name)
  if v == nil or v == "" then return nil end
  return v
end

local home = (getenv("HOME") or getenv("USERPROFILE") or ""):gsub("\\", "/")

local function trim(s) return (s:gsub("^%s+", ""):gsub("%s+$", "")) end

local function expand(p)
  p = trim(p):gsub("\\", "/")
  if p == "~" then return home end
  if p:sub(1, 2) == "~/" then return home .. p:sub(2) end
  return p
end

local function config_dir()
  local base = is_windows and getenv("APPDATA") or getenv("XDG_CONFIG_HOME") or (home .. "/.config")
  return (base:gsub("\\", "/")) .. "/rex"
end

local function read_file(path)
  if io == nil or io.open == nil then return nil end
  local f = io.open(path, "r")
  if f == nil then return nil end
  local t = f:read("*a")
  f:close()
  return t
end

---------------------------------------------------------------------------
-- t3p and tr3x
---------------------------------------------------------------------------

-- tr3x install writes its binary's path and environment into tr3x.lua.
-- t3p runs with that environment too, so it finds T3 exactly as tr3x does.
local tools = nil
local function find_tools()
  if tools then return tools end
  tools = { env = {}, tr3x = nil, t3p = nil }
  local text = read_file(config.tr3x_config and expand(config.tr3x_config) or (config_dir() .. "/tr3x.lua"))
  if text then
    tools.tr3x = text:match('local%s+TR3X%s*=%s*"([^"]+)"')
    local block = text:match("local%s+ENV%s*=%s*(%b{})")
    if block then
      for k, v in block:gmatch('([%w_]+)%s*=%s*"([^"]*)"') do tools.env[k] = v end
    end
  end
  if tools.tr3x then tools.env.T3P_TR3X = tools.tr3x end
  tools.t3p = config.bin and expand(config.bin) or (home .. (is_windows and "/.local/bin/t3p.exe" or "/.local/bin/t3p"))
  return tools
end

local function fail(msg) error(tostring(msg), 0) end

-- t3p runs the binary and returns its output, or raises its message.
local function t3p(args, timeout)
  local t = find_tools()
  local argv = { t.t3p }
  for _, a in ipairs(args) do argv[#argv + 1] = a end
  local r, err = rex.exec(argv, { env = t.env, timeout = timeout or 60 })
  if r == nil then fail(err) end
  if not r.ok then
    local msg = trim(r.stderr ~= "" and r.stderr or r.stdout):gsub("^t3p:%s*", "")
    fail(msg ~= "" and msg or ("t3p exited with status " .. tostring(r.code)))
  end
  return r.stdout
end

local function t3p_json(args, timeout) return rex.json.decode(t3p(args, timeout)) end

local function tr3x(args, timeout)
  local t = find_tools()
  if not t.tr3x then fail("tr3x isn't installed; t3p opens threads through it") end
  local argv = { t.tr3x }
  for _, a in ipairs(args) do argv[#argv + 1] = a end
  local r, err = rex.exec(argv, { env = t.env, timeout = timeout or 30 })
  if r == nil then fail(err) end
  if not r.ok then fail(trim(r.stderr ~= "" and r.stderr or r.stdout):gsub("^tr3x:%s*", "")) end
  return r.stdout
end

-- tell shows a one-line result in the palette.
local function tell(msg)
  if msg == nil or msg == "" then return end
  rex.ui.pick{ title = msg, confirm = "OK", items = { { id = "ok", title = "OK" } } }
end

-- A person who cancels is not worth a log line.
local function stop(why)
  if why ~= nil and why ~= "cancelled" and why ~= "timeout" then rex.log("warn", "t3p: " .. tostring(why)) end
end

---------------------------------------------------------------------------
-- Opening a thread
---------------------------------------------------------------------------

-- compose opens tr3x's composer in a new tab of the T3 Code session, the
-- same as tr3x's own New Thread.
local function compose(args)
  local t = find_tools()
  local session = rex.json.decode(tr3x({ "inbox" }, 10)).home
  if session == nil then fail("tr3x has no T3 Code session yet; run tr3x doctor") end
  local argv = { t.tr3x }
  for k, v in pairs(t.env) do
    if k ~= "T3P_TR3X" then
      argv[#argv + 1] = "--set-env"
      argv[#argv + 1] = k .. "=" .. v
    end
  end
  argv[#argv + 1] = "new"
  for _, a in ipairs(args) do argv[#argv + 1] = a end
  local w, err = rex.session.new_window{
    session_id = session, window_label = "New Thread", focus = true,
    layout = { block = { flavor = "com.superlogical.terminal.shell", label = "New Thread",
      options = { shell = "none", login = false, command = argv } } },
  }
  if err then fail(err) end
  rex.session.disown_block{ session_id = session }
  rex.client.queue("session.select", { session_id = session, window_id = w.window_id })
end

-- open_thread takes what `t3p resolve` or `t3p new` printed: where the
-- thread works, then the composer.
local function open_thread(r)
  if r.t3 == nil or r.t3 == "" then
    fail((r.name or "It") .. " is in T3 now, but tr3x doesn't list it yet; try again in a moment")
  end
  if r.skipped and #r.skipped > 0 then
    rex.log("warn", "t3p: couldn't reach " .. table.concat(r.skipped, ", ") .. "; T3 won't see it")
  end
  local place = "local"
  if r.worktrees then
    local rows = rex.json.decode(tr3x({ "workspaces", r.t3 }))
    -- The one used last goes first.
    for i, row in ipairs(rows) do
      if r.last and row.id == r.last and i > 1 then
        table.remove(rows, i); table.insert(rows, 1, row); break
      end
    end
    place = rows[1] and rows[1].id or "local"
    if #rows > 1 then
      local why
      place, why = rex.ui.pick{ title = r.name, placeholder = "Choose where to work", confirm = "New Thread", items = rows }
      if place == nil then return stop(why) end
    end
    pcall(t3p, { "last", r.t3, place })
  end
  local env_id, id = r.t3:match("^([^/]+)/(.+)$")
  compose({ "--env", env_id, "--project", id, "--workspace", place })
end

---------------------------------------------------------------------------
-- Pickers
---------------------------------------------------------------------------

local ADD_FOLDER, PICK_HERE, UP, NEW_FOLDER = "\0add", "\0here", "\0up", "\0new"

-- new_folder asks for a name and whether it's a repository, makes it inside
-- dir, and returns its path, or nil and why.
local function new_folder(dir, display, kind)
  local name, why = rex.ui.input{ title = "New Folder in " .. display, placeholder = "Folder name", confirm = "Next" }
  if name == nil then return nil, why end
  name = trim(name)
  if name == "" then return nil, "cancelled" end
  local what
  what, why = rex.ui.pick{ title = name, confirm = "Create", items = {
    { id = "git", title = "Git repository", subtitle = "git init, so T3 can make worktrees", icon = "branch" },
    { id = "plain", title = "Plain folder", icon = "folder" },
  } }
  if what == nil then return nil, why end
  local args = { "mkdir" }
  if kind then args[#args + 1] = "--kind"; args[#args + 1] = kind end
  if what == "git" then args[#args + 1] = "--git" end
  args[#args + 1] = "--"
  args[#args + 1] = dir
  args[#args + 1] = name
  return t3p_json(args).path
end

-- browse is the folder browser: Return on a repository picks it, on any
-- other folder opens it; the first row picks the folder you're in; New
-- Folder… makes one there and picks it; typing a path jumps there. kind
-- (optional) browses that kind's host, such as WSL.
local function browse(kind)
  local dir = nil
  for _ = 1, 200 do
    local args = { "browse" }
    if kind then args[#args + 1] = "--kind"; args[#args + 1] = kind end
    if dir then args[#args + 1] = dir end
    local ok, listing = pcall(t3p_json, args)
    if not ok then return nil, listing end
    dir = listing.dir
    local rows = { { id = PICK_HERE, title = "Add " .. (listing.display:match("([^/\\]+)$") or listing.display) .. " to T3",
      subtitle = listing.display, icon = "plus" } }
    rows[#rows + 1] = { id = NEW_FOLDER, title = "New Folder…", subtitle = "Make one in " .. listing.display, icon = "plus" }
    if listing.parent then rows[#rows + 1] = { id = UP, title = "..", subtitle = listing.parent, icon = "folder" } end
    local repo = {}
    for _, item in ipairs(listing.items) do
      repo[item.path] = item.git
      rows[#rows + 1] = { id = item.path, title = item.name, subtitle = item.git and "repository" or nil,
        icon = item.git and "branch" or "folder" }
    end
    local picked, why = rex.ui.pick{ title = "Add Folder to T3", placeholder = listing.display .. "  (type a path to jump)",
      allow_other = true, items = rows }
    if picked == nil then return nil, why end
    if type(picked) == "table" then
      local typed = trim(picked.text or "")
      if typed == "" then return nil, "cancelled" end
      dir = typed
    elseif picked == PICK_HERE then return dir
    elseif picked == NEW_FOLDER then return new_folder(dir, listing.display, kind)
    elseif picked == UP then dir = listing.parent
    elseif repo[picked] then return picked
    else dir = picked end
  end
  return nil, "too many steps"
end

local function pick_place(title, args, extra)
  local rows = t3p_json(args or { "rows" })
  for _, row in ipairs(extra or {}) do rows[#rows + 1] = row end
  return rex.ui.pick{ title = title, placeholder = "Filter", confirm = "Next", items = rows }
end

---------------------------------------------------------------------------
-- Actions
---------------------------------------------------------------------------

local function new_thread(ctx)
  if ctx ~= nil and ctx.server ~= nil then
    fail("the session is on " .. ctx.server .. "; t3p works on this machine")
  end
  local key, why = pick_place("New T3 Thread", { "rows" },
    { { id = ADD_FOLDER, title = "Add Folder…", subtitle = "Any folder, made a T3 project", icon = "plus" } })
  if key == nil then return stop(why) end
  if key == ADD_FOLDER then
    key, why = browse(nil)
    if key == nil then return stop(why) end
  end
  open_thread(t3p_json({ "resolve", key }, 120))
end

local function new_of_kind(k)
  return function()
    local name, why = rex.ui.input{ title = "New " .. k.name,
      placeholder = k.match and ("Name (matches " .. k.match .. ")") or "Name", confirm = "Next" }
    if name == nil then return stop(why) end
    name = trim(name)
    if name == "" then return end
    local args = { "new", k.name, name }
    if k.machine then args[#args + 1] = "--machine"; args[#args + 1] = k.machine end
    if k.severities and #k.severities > 0 then
      local items = {}
      for _, s in ipairs(k.severities) do items[#items + 1] = { id = tostring(s), title = "Sev " .. s } end
      local sev
      sev, why = rex.ui.pick{ title = name, placeholder = "Severity", confirm = "Create", items = items }
      if sev == nil then return stop(why) end
      args[#args + 1] = "--sev"; args[#args + 1] = sev
    end
    open_thread(t3p_json(args, 120))
  end
end

-- What each fix runs. "add" opens a thread too, since that's why you add one.
local function apply_fix(fix, key, check)
  if fix == "add" then return open_thread(t3p_json({ "resolve", key }, 120)) end
  if fix == "ignore" then return t3p({ "ignore", key, check }) end
  if fix == "close" then return tell(trim(t3p({ "close", key }))) end
  if fix == "updated" or fix == "park" or fix == "open" then return t3p({ fix, key }) end
end

local function tidy()
  local findings = t3p_json({ "check", "--json" }, 120)
  if #findings == 0 then
    rex.ui.pick{ title = "Tidy Up", items = { { id = "ok", title = "Nothing to tidy up" } } }
    return
  end
  local rows, by = {}, {}
  for i, f in ipairs(findings) do
    local id = tostring(i)
    by[id] = f
    rows[#rows + 1] = { id = id, title = f.name, subtitle = f.text, detail = f.check,
      icon = f.check == "untracked" and "plus" or "folder" }
  end
  local picked, why = rex.ui.pick{ title = "Tidy Up · " .. #findings, placeholder = "Filter", confirm = "Next", items = rows }
  if picked == nil then return stop(why) end
  local f = by[picked]
  local items = {}
  for _, x in ipairs(f.fixes) do items[#items + 1] = { id = x.id, title = x.title } end
  local fix
  fix, why = rex.ui.pick{ title = f.name .. ": " .. f.text, confirm = "Do it", items = items }
  if fix == nil then return stop(why) end
  apply_fix(fix, f.key, f.check)
end

local function manage()
  local key, why = pick_place("Manage Work", { "rows", "--all" })
  if key == nil then return stop(why) end
  local items = {
    { id = "note", title = "Describe…", subtitle = "A one-line note: what is this?" },
    { id = "tag", title = "Tags…", subtitle = "+tag adds, -tag removes" },
    { id = "park", title = "Park", subtitle = "Keep it, out of the way" },
    { id = "open", title = "Reopen" },
    { id = "close", title = "Close", subtitle = "Out of the picker; nothing is deleted" },
    { id = "sev", title = "Severity…", subtitle = "For kinds with due dates" },
    { id = "updated", title = "I've just sent an update", subtitle = "The next one is due from now" },
  }
  local what
  what, why = rex.ui.pick{ title = "Manage", confirm = "Next", items = items }
  if what == nil then return stop(why) end
  if what == "note" then
    local current = trim(t3p({ "note", key }))
    local text
    text, why = rex.ui.input{ title = "Describe", value = current, placeholder = "What is this?", confirm = "Save" }
    if text == nil then return stop(why) end
    if trim(text) == "" then t3p({ "note", key, "--clear" }) else t3p({ "note", key, "--", trim(text) }) end
  elseif what == "tag" then
    local text
    text, why = rex.ui.input{ title = "Tags", placeholder = "+work -old", confirm = "Save" }
    if text == nil then return stop(why) end
    local args = { "tag", key, "--" }
    for t in text:gmatch("%S+") do args[#args + 1] = t end
    t3p(args)
  elseif what == "sev" then
    local n
    n, why = rex.ui.input{ title = "Severity", placeholder = "1, 2, 3…", confirm = "Save" }
    if n == nil then return stop(why) end
    t3p({ "sev", key, trim(n) })
  elseif what == "close" then
    local sure
    sure, why = rex.ui.pick{ title = "Close it?", confirm = "Choose", items = {
      { id = "keep", title = "Keep it open" },
      { id = "close", title = "Close it", subtitle = "Out of the picker, off T3's list if it has no threads. Nothing is deleted" } } }
    if sure ~= "close" then return stop(why) end
    tell(trim(t3p({ "close", key })))
  else
    t3p({ what, key })
  end
end

local function shell()
  local key, why = pick_place("Open Shell in")
  if key == nil then return stop(why) end
  local places = t3p_json({ "ls", "--json" })
  for _, p in ipairs(places) do
    if p.key == key then
      if p.remote or p.owner then fail(p.name .. " is on " .. (p.owner or p.machine or "another machine") .. "; a shell there isn't supported yet") end
      if p.host ~= "local" then fail("a shell in " .. p.host .. " isn't supported yet") end
      local s, err = rex.call("session.create", { label = p.name, initial_windows = { {
        window_label = "shell",
        layout = rex.layout.block{ flavor = "com.superlogical.terminal.shell", options = { cwd = p.path } },
      } } })
      if s == nil then fail(err) end
      rex.client.queue("session.select", { session_id = s.session_id })
      return
    end
  end
end

---------------------------------------------------------------------------
-- Registering
---------------------------------------------------------------------------

local function action(name, title, description, keywords, run)
  rex.action{ name = name, title = title, description = description, category = "T3 Code",
    keywords = keywords, run = run }
end

action("t3p_thread", "New T3 Thread…", "Pick any of your work, or add a folder to T3, and start a thread",
  "t3p t3 project case folder repo worktree new thread add start", new_thread)
action("t3p_tidy", "Tidy Up Work…", "What's stale, untracked, orphaned or overdue, with a fix for each",
  "t3p t3 project case clean stale orphan overdue untracked nested", tidy)
action("t3p_manage", "Manage Work…", "Describe, tag, park, close or reopen a project or case",
  "t3p t3 project case note describe tag park close reopen severity", manage)
action("t3p_shell", "Open Shell in…", "A shell session in a project's or case's folder",
  "t3p shell terminal session project case folder", shell)

-- A New <Kind>… action for each kind that's created from a template.
local ok, kinds = pcall(t3p_json, { "kinds", "--json" }, 20)
if ok and type(kinds) == "table" then
  for _, k in ipairs(kinds) do
    if k.create then
      -- gsub's results go into locals: a gsub inside a ".." expression has
      -- made Rex's Lua (gopher-lua) panic before.
      local id = k.name:gsub("%W", "_")
      local title = k.name:gsub("^%l", string.upper)
      local on = ""
      if k.machine then
        local mid = k.machine:gsub("%W", "_")
        id = mid .. "_" .. id
        on = " on " .. k.machine
      end
      action("t3p_new_" .. id, "New " .. title .. on .. "…",
        "Create a " .. k.name .. on .. " from its template, add it to T3 and start a thread",
        "t3p t3 new create " .. k.name .. " " .. k.plural, new_of_kind(k))
    end
  end
else
  rex.log("warn", "t3p: can't run t3p (" .. tostring(kinds) .. "); install it to " .. find_tools().t3p)
end

local function bind_key(key)
  if bound_key ~= nil then pcall(rex.unbind, bound_key) end
  bound_key = nil
  if key then rex.bind(key, "t3p_thread"); bound_key = key end
end

function M.setup(opts)
  opts = opts or {}
  if opts.bin ~= nil then config.bin = opts.bin; tools = nil end
  if opts.tr3x_config ~= nil then config.tr3x_config = opts.tr3x_config; tools = nil end
  if opts.key ~= nil then bind_key(opts.key) end
  return M
end

bind_key(config.key)

M._internal = { find_tools = find_tools, t3p = t3p, open_thread = open_thread, new_thread = new_thread,
  tidy = tidy, manage = manage, browse = browse, new_folder = new_folder, apply_fix = apply_fix,
  reset = function() tools = nil end }

return M
