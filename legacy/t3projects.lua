-- t3-projects: start a T3 Code thread in a project, from Rex, through tr3x.
--
-- The flow:
--   1. Pick a project: one T3 already has (on any machine Rex follows), a
--      repository under your roots that T3 doesn't have yet, or any folder
--      through Add Folder….
--   2. A folder T3 doesn't have is added to T3 first (`t3 project add`).
--   3. Pick where the thread works: the project folder, a worktree it has, or
--      a new worktree. T3 makes and owns worktrees, so this lists T3's own
--      choices (`tr3x workspaces`). The last one used is listed first.
-- Then tr3x's composer opens as a new tab in the T3 Code session, for the
-- first message.
--
-- Install: put this file next to init.lua, after require("tr3x"):
--
--     require("t3projects")
--
-- Optional settings:
--
--     require("t3projects").setup{
--       key   = "alt+cmd+t",       -- false binds nothing
--       roots = { "~/Projects" },  -- repositories here are listed even
--                                  -- before T3 has them
--       tr3x  = nil,               -- path to tr3x; default: what tr3x install wrote
--       t3    = nil,               -- path to the t3 CLI; default: found on this machine
--     }
--
-- Written for Lua 5.1 (what Rex embeds) and for init.lua, which runs
-- unsandboxed on the server. Every command runs through rex.exec with an
-- argument list, never a shell.

local M = {}

-- Servers before control API revision 17 have no rex.ui.
local has_ui, ui = pcall(function() return rex.ui end)
if not has_ui or ui == nil then
  return M
end

---------------------------------------------------------------------------
-- Settings and platform
---------------------------------------------------------------------------

local config = {
  key = "alt+cmd+t",
  roots = {},
  tr3x = nil,
  t3 = nil,
  state_file = nil, -- nil means <config dir>/rex/t3-projects.json
  tr3x_config = nil, -- nil means <config dir>/rex/tr3x.lua
}
local bound_key = nil

local is_windows = (package and package.config or "/"):sub(1, 1) == "\\"

local function env(name)
  if os == nil or os.getenv == nil then return nil end
  local value = os.getenv(name)
  if value == nil or value == "" then return nil end
  return value
end

local home = env("HOME") or env("USERPROFILE") or ""

local function slashes(path) return (path:gsub("\\", "/")) end

local function trim(s) return (s:gsub("^%s+", ""):gsub("%s+$", "")) end

local function expand(path)
  path = trim(path)
  path = path:gsub('^"(.*)"$', "%1"):gsub("^'(.*)'$", "%1")
  path = slashes(path)
  if path == "~" then return slashes(home) end
  if path:sub(1, 2) == "~/" then return slashes(home) .. path:sub(2) end
  return path
end

local function short(path)
  local h = slashes(home)
  if h ~= "" and path:sub(1, #h + 1) == h .. "/" then return "~" .. path:sub(#h + 1) end
  return path
end

local function basename(path)
  return (path:gsub("/+$", ""):match("([^/]+)$")) or path
end

local function config_dir()
  local base
  if is_windows then
    base = env("APPDATA")
  else
    base = env("XDG_CONFIG_HOME") or (home ~= "" and (home .. "/.config") or nil)
  end
  return base and (slashes(base) .. "/rex") or nil
end

local function read_file(path)
  if io == nil or io.open == nil or path == nil then return nil end
  local f = io.open(path, "r")
  if f == nil then return nil end
  local text = f:read("*a")
  f:close()
  return text
end

local function is_file(path)
  if is_windows then
    local r = rex.exec({ "cmd", "/c", "if", "exist", (path:gsub("/", "\\")), "echo", "yes" })
    return r ~= nil and r.ok and r.stdout:find("yes", 1, true) ~= nil
  end
  local r = rex.exec({ "test", "-x", path })
  return r ~= nil and r.ok
end

local function is_folder(path)
  if is_windows then
    local native = path:gsub("/", "\\")
    local r = rex.exec({ "cmd", "/c", "if", "exist", native .. "\\NUL", "echo", "yes" })
    return r ~= nil and r.ok and r.stdout:find("yes", 1, true) ~= nil
  end
  local r = rex.exec({ "test", "-d", path })
  return r ~= nil and r.ok
end

---------------------------------------------------------------------------
-- tr3x and t3
---------------------------------------------------------------------------

-- tr3x install writes the binary's path and the environment it found (where
-- Rex, T3 and tr3x's state live) into tr3x.lua as locals. Read them from
-- there, so this script runs tr3x exactly as tr3x's own actions do.
local tool = nil

local function find_tools()
  if tool ~= nil then return tool end
  tool = { tr3x = nil, env = {}, t3 = nil }
  local text = read_file(config.tr3x_config and expand(config.tr3x_config)
    or (config_dir() and (config_dir() .. "/tr3x.lua")))
  if text then
    tool.tr3x = text:match('local%s+TR3X%s*=%s*"([^"]+)"')
    local block = text:match("local%s+ENV%s*=%s*(%b{})")
    if block then
      for k, v in block:gmatch('([%w_]+)%s*=%s*"([^"]*)"') do tool.env[k] = v end
    end
  end
  if config.tr3x then tool.tr3x = expand(config.tr3x) end
  if tool.tr3x == nil then
    local guess = slashes(home) .. (is_windows and "/.local/bin/tr3x.exe" or "/.local/bin/tr3x")
    tool.tr3x = is_file(guess) and guess or "tr3x"
  end
  -- Rex's server rarely has Homebrew on its PATH, so look in the usual places.
  if config.t3 then
    tool.t3 = expand(config.t3)
  else
    local candidates = is_windows and {} or {
      "/opt/homebrew/bin/t3", "/usr/local/bin/t3", slashes(home) .. "/.local/bin/t3",
      slashes(home) .. "/.t3/bin/t3",
    }
    for _, c in ipairs(candidates) do
      if is_file(c) then tool.t3 = c; break end
    end
    tool.t3 = tool.t3 or "t3"
  end
  return tool
end

-- run runs argv with tr3x's environment. It returns stdout, or nil and a
-- one-line message.
local function run(argv, timeout)
  local r, err = rex.exec(argv, { env = find_tools().env, timeout = timeout or 30 })
  if r == nil then return nil, tostring(err) end
  if not r.ok then
    local msg = trim(r.stderr ~= "" and r.stderr or r.stdout)
    -- A Node stack trace's first line is the part worth showing.
    msg = msg:match("^[^\n]*") or msg
    msg = msg:gsub("^%[[^%]]*%]%s*", ""):gsub("^ERROR%s*%(#%d+%):%s*", ""):gsub("^tr3x:%s*", "")
    return nil, msg ~= "" and msg or ("exited with status " .. tostring(r.code))
  end
  return r.stdout
end

local function tr3x(args, timeout)
  local argv = { find_tools().tr3x }
  for _, a in ipairs(args) do argv[#argv + 1] = a end
  return run(argv, timeout)
end

local function t3(args)
  local argv = { find_tools().t3 }
  for _, a in ipairs(args) do argv[#argv + 1] = a end
  return run(argv, 60)
end

local function fail(message) error(tostring(message), 0) end

-- t3_projects returns every project on every machine, as tr3x prints them:
-- { id = "env/project", title, subtitle = "~/path · Machine", ... }. Each
-- gets .path and .machine split out of its subtitle.
local function t3_projects()
  local out, err = tr3x({ "projects" })
  if out == nil then return nil, err end
  local ok, list = pcall(rex.json.decode, out)
  if not ok or type(list) ~= "table" then return nil, "tr3x printed no project list" end
  for _, p in ipairs(list) do
    local path, machine = (p.subtitle or ""):match("^(.-) · (.+)$")
    p.path, p.machine = path or p.subtitle, machine
  end
  return list
end

-- this_machine returns what T3 calls the machine Rex runs on, from the row of
-- `tr3x envs` that says "this Mac" (or "this machine"), or nil.
local this_machine_name = nil
local function this_machine()
  if this_machine_name ~= nil then return this_machine_name or nil end
  this_machine_name = false
  local out = tr3x({ "envs" }, 15)
  for line in (out or ""):gmatch("[^\r\n]+") do
    local name = line:match("^(.-)%s%s+this ")
    if name then this_machine_name = trim(name); break end
  end
  return this_machine_name or nil
end

-- is_local says whether a project from t3_projects is on this machine. When
-- the machine's name is unknown, every project counts, which only means a
-- folder is hidden from "Add to T3" a little more often.
local function is_local(p)
  local me = this_machine()
  return me == nil or p.machine == nil or p.machine == me
end

-- add_to_t3 makes folder a T3 project on this machine, or finds the one it
-- already is, and returns its "env/project" id.
local function add_to_t3(folder)
  local out, err = t3({ "project", "add", folder })
  local id = out and out:match("Added project (%S+)")
  if out == nil and not (err or ""):find("already exists", 1, true) then
    return nil, "t3 project add: " .. tostring(err)
  end
  local list, lerr = t3_projects()
  if list == nil then return nil, lerr end
  for _, p in ipairs(list) do
    if id and p.id:sub(-#id - 1) == "/" .. id then return p.id end
  end
  for _, p in ipairs(list) do
    if is_local(p) and expand(p.path) == folder then return p.id end
  end
  return nil, "T3 added " .. short(folder) .. " but tr3x doesn't list it yet; try again in a moment"
end

---------------------------------------------------------------------------
-- State: the workspace used last in each project
---------------------------------------------------------------------------

local state = nil

local function state_path()
  if config.state_file then return expand(config.state_file) end
  local dir = config_dir()
  return dir and (dir .. "/t3-projects.json") or nil
end

local function load_state()
  if state ~= nil then return state end
  state = { last = {} }
  local text = read_file(state_path())
  if text == nil then return state end
  local ok, decoded = pcall(rex.json.decode, text)
  if ok and type(decoded) == "table" and type(decoded.last) == "table" then
    for k, v in pairs(decoded.last) do
      if type(k) == "string" and type(v) == "string" then state.last[k] = v end
    end
  end
  return state
end

local function save_state()
  local path = state_path()
  if io == nil or io.open == nil or path == nil then return end
  local dir = path:match("^(.*)/[^/]+$")
  if dir then
    if is_windows then rex.exec({ "cmd", "/c", "mkdir", (dir:gsub("/", "\\")) })
    else rex.exec({ "mkdir", "-p", dir }) end
  end
  local f, err = io.open(path, "w")
  if f == nil then
    rex.log("warn", "t3projects: cannot save " .. path .. ": " .. tostring(err))
    return
  end
  f:write(rex.json.encode({ last = state.last }))
  f:close()
end

---------------------------------------------------------------------------
-- Folders
---------------------------------------------------------------------------

-- scan lists the repositories one level under each root.
local function scan()
  local found = {}
  for _, root in ipairs(config.roots) do
    root = expand(root)
    if is_windows then
      local r = rex.exec({ "cmd", "/c", "dir", "/b", "/ad", (root:gsub("/", "\\")) })
      if r ~= nil and r.ok then
        for name in r.stdout:gmatch("[^\r\n]+") do
          local path = root .. "/" .. name
          local g = rex.exec({ "git", "-C", path, "rev-parse", "--show-cdup" })
          if g ~= nil and g.ok and trim(g.stdout) == "" then found[#found + 1] = path end
        end
      end
    else
      local r = rex.exec({ "find", root, "-mindepth", "2", "-maxdepth", "2", "-name", ".git", "-print0" })
      if r ~= nil and r.ok then
        for hit in r.stdout:gmatch("[^%z]+") do
          local path = hit:match("^(.*)/%.git$")
          if path then found[#found + 1] = path end
        end
      end
    end
  end
  return found
end

local function subfolders(dir)
  local list = {}
  if is_windows then
    local r = rex.exec({ "cmd", "/c", "dir", "/b", "/ad", (dir:gsub("/", "\\")) })
    if r ~= nil and r.ok then
      for name in r.stdout:gmatch("[^\r\n]+") do
        if name:sub(1, 1) ~= "." then
          list[#list + 1] = { path = dir:gsub("/+$", "") .. "/" .. name, repo = false }
        end
      end
    end
  else
    local r = rex.exec({ "find", dir, "-mindepth", "1", "-maxdepth", "1", "-type", "d",
      "-not", "-name", ".*", "-print0" })
    local repos = {}
    local g = rex.exec({ "find", dir, "-mindepth", "2", "-maxdepth", "2", "-name", ".git", "-print0" })
    if g ~= nil and g.ok then
      for hit in g.stdout:gmatch("[^%z]+") do
        local path = hit:match("^(.*)/%.git$")
        if path then repos[path] = true end
      end
    end
    if r ~= nil and r.ok then
      for path in r.stdout:gmatch("[^%z]+") do
        list[#list + 1] = { path = path, repo = repos[path] == true }
      end
    end
  end
  table.sort(list, function(a, b) return basename(a.path):lower() < basename(b.path):lower() end)
  return list
end

local function parent_of(dir)
  local parent = dir:match("^(.*)/[^/]+/?$")
  if parent == nil or parent == "" then
    if dir:match("^/[^/]+/?$") then return "/" end
    return nil
  end
  if parent:match("^%a:$") then return parent .. "/" end
  return parent
end

-- browse is a folder browser in the palette. Return on a repository picks
-- it; on any other folder it opens it. The first row picks the folder being
-- browsed. A typed path jumps there. It returns a folder, or nil and why.
local PICK_HERE, UP = "\0here", "\0up"
local function browse()
  local dir = config.roots[1] and expand(config.roots[1]) or slashes(home)
  if dir == "" or not is_folder(dir) then dir = "/" end
  for _ = 1, 200 do
    dir = dir:gsub("/+$", "")
    if dir == "" then dir = "/" end
    local rows = {
      { id = PICK_HERE, title = "Add " .. (dir == "/" and "/" or basename(dir)) .. " to T3",
        subtitle = short(dir), icon = "plus" },
    }
    if parent_of(dir) ~= nil then
      rows[#rows + 1] = { id = UP, title = "..", subtitle = short(parent_of(dir)), icon = "folder" }
    end
    local children = {}
    for _, child in ipairs(subfolders(dir)) do
      children[child.path] = child
      rows[#rows + 1] = { id = child.path, title = basename(child.path),
        subtitle = child.repo and "repository" or nil, icon = child.repo and "branch" or "folder" }
    end
    local picked, why = rex.ui.pick{
      title = "Add Folder to T3",
      placeholder = short(dir) .. "/  (type a path to jump)",
      allow_other = true,
      items = rows,
    }
    if picked == nil then return nil, why end
    if type(picked) == "table" then
      local typed = expand(picked.text or "")
      if typed == "" then return nil, "cancelled" end
      if not is_folder(typed) then return nil, "not a folder: " .. typed end
      dir = typed
    elseif picked == PICK_HERE then
      return dir
    elseif picked == UP then
      dir = parent_of(dir)
    elseif children[picked] and children[picked].repo then
      return picked
    else
      dir = picked
    end
  end
  return nil, "too many steps"
end

---------------------------------------------------------------------------
-- The pickers
---------------------------------------------------------------------------

local ADD_FOLDER = "\0add"
local FOLDER = "dir:" -- prefix for a folder T3 doesn't have yet

-- project_rows lists T3's projects (in tr3x's order: worked in last first),
-- then repositories under the roots that T3 doesn't have, then Add Folder….
local function project_rows()
  local list, err = t3_projects()
  if list == nil then return nil, err end
  local rows, known = {}, {}
  for _, p in ipairs(list) do
    rows[#rows + 1] = p
    if is_local(p) and p.path then known[expand(p.path)] = true end
  end
  local extra = {}
  for _, path in ipairs(scan()) do
    if not known[path] then extra[#extra + 1] = path; known[path] = true end
  end
  table.sort(extra, function(a, b) return basename(a):lower() < basename(b):lower() end)
  for _, path in ipairs(extra) do
    rows[#rows + 1] = { id = FOLDER .. path, title = basename(path),
      subtitle = short(path), detail = "add to T3", icon = "plus" }
  end
  rows[#rows + 1] = { id = ADD_FOLDER, title = "Add Folder…", subtitle = "Any folder, made a T3 project",
    icon = "plus" }
  return rows
end

-- workspace_rows lists where a thread in project can work, the one used last
-- first.
local function workspace_rows(project)
  local out, err = tr3x({ "workspaces", project })
  if out == nil then return nil, err end
  local ok, rows = pcall(rex.json.decode, out)
  if not ok or type(rows) ~= "table" or #rows == 0 then return nil, "tr3x listed nowhere to work" end
  local last = load_state().last[project]
  if last ~= nil then
    for i, row in ipairs(rows) do
      if row.id == last and i > 1 then
        table.remove(rows, i)
        table.insert(rows, 1, row)
        break
      end
    end
  end
  return rows
end

-- compose opens tr3x's composer in a new tab of the T3 Code session, on args.
-- The same as tr3x's own New Thread, which this script can't call directly.
local function compose(args)
  local tools = find_tools()
  local out, err = tr3x({ "inbox" }, 10)
  if out == nil then fail("tr3x isn't syncing (" .. tostring(err) .. "); run tr3x doctor") end
  local sid = rex.json.decode(out).home
  if sid == nil then fail("tr3x has no T3 Code session yet; run tr3x doctor") end
  -- A pane runs with the Rex server's environment, so tr3x's goes on the line.
  local argv = { tools.tr3x }
  for k, v in pairs(tools.env) do
    argv[#argv + 1] = "--set-env"
    argv[#argv + 1] = k .. "=" .. v
  end
  argv[#argv + 1] = "new"
  for _, a in ipairs(args) do argv[#argv + 1] = a end
  local w, werr = rex.session.new_window{
    session_id = sid,
    window_label = "New Thread",
    focus = true,
    layout = { block = { flavor = "com.superlogical.terminal.shell", label = "New Thread",
      options = { shell = "none", login = false, command = argv } } },
  }
  if werr then fail(werr) end
  rex.session.disown_block{ session_id = sid }
  rex.client.queue("session.select", { session_id = sid, window_id = w.window_id })
end

---------------------------------------------------------------------------
-- The actions
---------------------------------------------------------------------------

local function stop(why)
  if why ~= nil and why ~= "cancelled" and why ~= "timeout" then
    rex.log("warn", "t3projects: " .. tostring(why))
  end
end

local function new_thread(ctx)
  if ctx ~= nil and ctx.server ~= nil then
    fail("the session is on " .. ctx.server .. "; folders are added on this machine")
  end
  local rows, err = project_rows()
  if rows == nil then fail(err) end

  -- 1. The project.
  local picked, why = rex.ui.pick{ title = "New T3 Thread", placeholder = "Choose a project",
    confirm = "Next", items = rows }
  if picked == nil then return stop(why) end

  -- 2. A folder T3 doesn't have yet becomes a project.
  local folder
  if picked == ADD_FOLDER then
    folder, why = browse()
    if folder == nil then return stop(why) end
  elseif picked:sub(1, #FOLDER) == FOLDER then
    folder = picked:sub(#FOLDER + 1)
  end
  local project, name = picked, nil
  if folder then
    project, err = add_to_t3(folder)
    if project == nil then fail(err) end
    name = basename(folder)
  else
    for _, p in ipairs(rows) do if p.id == picked then name = p.title end end
  end

  -- 3. Where the thread works.
  local places
  places, err = workspace_rows(project)
  if places == nil then fail(err) end
  local place = places[1].id
  if #places > 1 then
    place, why = rex.ui.pick{ title = name or "Project", placeholder = "Choose where to work",
      confirm = "New Thread", items = places }
    if place == nil then return stop(why) end
  end
  load_state().last[project] = place
  save_state()

  local env_id, id = project:match("^([^/]+)/(.+)$")
  compose({ "--env", env_id, "--project", id, "--workspace", place })
end

local function remove_project()
  local list, err = t3_projects()
  if list == nil then fail(err) end
  local items = {}
  for _, p in ipairs(list) do
    -- T3's own scratch project ("No project") isn't one to remove.
    if is_local(p) and p.path and not p.path:find("/.t3/scratch", 1, true) then items[#items + 1] = p end
  end
  if #items == 0 then return end
  local picked, why = rex.ui.pick{ title = "Remove T3 Project",
    placeholder = "Choose a project (its folder stays)", items = items }
  if picked == nil then return stop(why) end
  local chosen
  for _, p in ipairs(items) do if p.id == picked then chosen = p end end
  local sure
  sure, why = rex.ui.pick{ title = "Remove " .. chosen.title .. " from T3?", confirm = "Choose", items = {
    { id = "keep", title = "Keep it" },
    { id = "remove", title = "Remove " .. chosen.title, subtitle = "From T3's list; the folder and its threads stay" },
  } }
  if sure ~= "remove" then return stop(why) end
  local _, rerr = t3({ "project", "remove", picked:match("^[^/]+/(.+)$") })
  if rerr then fail("t3 project remove: " .. rerr) end
  load_state().last[picked] = nil
  save_state()
end

rex.action{
  name = "t3_project_thread",
  title = "New T3 Thread in Project…",
  description = "Choose a T3 project, or add a folder to T3, then where to work, and start a thread",
  category = "T3 Code",
  keywords = "t3 project folder repo worktree branch new thread add start",
  run = new_thread,
}

rex.action{
  name = "t3_project_remove",
  title = "Remove T3 Project…",
  description = "Take a project off T3's list on this machine; its folder stays",
  category = "T3 Code",
  keywords = "t3 project forget remove delete list",
  run = remove_project,
}

---------------------------------------------------------------------------
-- Setup
---------------------------------------------------------------------------

local function bind_key(key)
  if bound_key ~= nil then pcall(rex.unbind, bound_key) end
  bound_key = nil
  if key then
    rex.bind(key, "t3_project_thread")
    bound_key = key
  end
end

function M.setup(opts)
  opts = opts or {}
  if opts.roots ~= nil then config.roots = opts.roots end
  if opts.tr3x ~= nil then config.tr3x = opts.tr3x; tool = nil end
  if opts.t3 ~= nil then config.t3 = opts.t3; tool = nil end
  if opts.tr3x_config ~= nil then config.tr3x_config = opts.tr3x_config; tool = nil end
  if opts.state_file ~= nil then config.state_file = opts.state_file; state = nil end
  if opts.key ~= nil then bind_key(opts.key) end
  return M
end

bind_key(config.key)

M._internal = {
  expand = expand, short = short, basename = basename, parent_of = parent_of,
  find_tools = find_tools, t3_projects = t3_projects, this_machine = this_machine,
  add_to_t3 = add_to_t3, project_rows = project_rows, workspace_rows = workspace_rows,
  new_thread = new_thread, remove_project = remove_project, load_state = load_state,
  config = config,
  reset = function() state = nil; tool = nil; this_machine_name = nil end,
}

return M
