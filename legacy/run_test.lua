-- Tests for t3projects.lua against a stand-in for the rex table, a fake tr3x
-- and a fake t3 (shell scripts that print canned output and log their args).
-- Run from the repository root:  luajit test/run.lua   (any Lua 5.1)

local function sh(s) return "'" .. s:gsub("'", "'\\''") .. "'" end
local tmp = os.tmpname(); os.remove(tmp); os.execute("mkdir -p " .. sh(tmp))
local function write(path, text) local f = assert(io.open(path, "w")); f:write(text); f:close() end
local function read(path) local f = io.open(path); if not f then return "" end local t = f:read("*a"); f:close(); return t end

local function capture(cmd)
  local p = io.popen(cmd .. " 2>" .. sh(tmp .. "/stderr") .. "; echo \"::$?\"")
  local out = p:read("*a"); p:close()
  local body, code = out:match("^(.*)::(%d+)\n$")
  return body, tonumber(code), read(tmp .. "/stderr")
end

local json = dofile("legacy/json.lua")

-- The fake tools. Each logs "<tool> <args>" to calls.log.
local log = tmp .. "/calls.log"
local HOME = tmp .. "/home"
os.execute("mkdir -p " .. sh(HOME .. "/Projects/known/.git") .. " " .. sh(HOME .. "/Projects/fresh/.git")
  .. " " .. sh(HOME .. "/Projects/plain") .. " " .. sh(tmp .. "/bin"))
local P1 = '{"id":"envA/p1","title":"known","subtitle":"~/Projects/known · Mac","detail":"1h ago","icon":"folder"}'
local P2 = '{"id":"envA/p2","title":"fresh","subtitle":"~/Projects/fresh · Mac","detail":"","icon":"folder"}'
local R1 = '{"id":"envB/r1","title":"far","subtitle":"~/Projects/plain · otherbox","detail":"","icon":"folder"}'
local added = tmp .. "/added"
write(tmp .. "/bin/tr3x", "#!/bin/sh\n"
  .. "echo \"tr3x $*\" >> " .. sh(log) .. "\n"
  .. "case \"$1\" in\n"
  .. "  projects) if [ -f " .. sh(added) .. " ]; then echo '[" .. P1 .. "," .. P2 .. "," .. R1 .. "]';"
  .. " else echo '[" .. P1 .. "," .. R1 .. "]'; fi ;;\n"
  .. "  envs) printf 'Mac  this Mac  ok\\notherbox  over SSH (otherbox)  ok\\n' ;;\n"
  .. "  workspaces) echo '[{\"id\":\"local\",\"title\":\"Project folder\"},{\"id\":\"new\",\"title\":\"New worktree\"}]' ;;\n"
  .. "  inbox) echo '{\"home\":\"sess-home\"}' ;;\n"
  .. "  *) exit 1 ;;\n"
  .. "esac\n")
write(tmp .. "/bin/t3", "#!/bin/sh\n"
  .. "echo \"t3 $*\" >> " .. sh(log) .. "\n"
  .. "if [ \"$1 $2\" = \"project add\" ]; then\n"
  .. "  if [ \"$3\" = " .. sh(HOME .. "/Projects/known") .. " ]; then\n"
  .. "    echo '[12:00:00.000] ERROR (#2): ProjectAlreadyExistsError: An active project already exists' >&2; exit 1\n"
  .. "  fi\n"
  .. "  touch " .. sh(added) .. "; echo \"Added project p2 (fresh) at $3.\"; exit 0\n"
  .. "fi\n"
  .. "if [ \"$1 $2\" = \"project remove\" ]; then echo \"Removed project $3.\"; exit 0; fi\n"
  .. "exit 1\n")
os.execute("chmod +x " .. sh(tmp .. "/bin/tr3x") .. " " .. sh(tmp .. "/bin/t3"))
write(tmp .. "/tr3x.lua", 'local TR3X = "' .. tmp .. '/bin/tr3x"\n'
  .. 'local ENV = { T3CODE_HOME = "/x/.t3", TR3X_STATE_DIR = "/x/state" }\n')

local answers, asked, windows, queued, binds, actions = {}, {}, {}, {}, {}, {}
local function answer(...) for _, a in ipairs({ ... }) do answers[#answers + 1] = a end end
rex = {
  json = json,
  log = function() end,
  exec = function(argv)
    local parts = {}
    for _, a in ipairs(argv) do parts[#parts + 1] = sh(a) end
    local out, code, err = capture(table.concat(parts, " "))
    return { ok = code == 0, code = code, stdout = out, stderr = err }
  end,
  action = function(spec) actions[spec.name] = spec end,
  bind = function(key, name) binds[key] = name end,
  unbind = function(key) binds[key] = nil end,
  ui = {
    pick = function(spec)
      asked[#asked + 1] = spec
      local a = table.remove(answers, 1)
      if a == nil then return nil, "cancelled" end
      if type(a) == "table" then return a end -- typed text, as allow_other gives
      for _, row in ipairs(spec.items) do
        if row.title == a or row.id == a then return row.id end
      end
      error("no row " .. tostring(a) .. " in " .. spec.title)
    end,
  },
  session = {
    new_window = function(spec) windows[#windows + 1] = spec; return { window_id = "w1" } end,
    disown_block = function() end,
  },
  client = { queue = function(m, a) queued[#queued + 1] = { m, a } end },
}

-- The script reads HOME as it loads: point it at the fake one.
local real_getenv = os.getenv
os.getenv = function(k) if k == "HOME" then return HOME end return real_getenv(k) end

local P = dofile("legacy/t3projects.lua").setup{
  roots = { "~/Projects" }, t3 = tmp .. "/bin/t3", tr3x_config = tmp .. "/tr3x.lua",
  state_file = tmp .. "/state/t3-projects.json",
}
local I = P._internal

local n = 0
local function test(name, fn)
  n = n + 1
  answers, asked, windows, queued = {}, {}, {}, {}
  os.remove(log)
  local ok, err = pcall(fn)
  print((ok and "ok   " or "FAIL ") .. name .. (ok and "" or ("\n     " .. tostring(err))))
  if not ok then os.exit(1) end
end
local function titles(rows) local t = {} for _, r in ipairs(rows) do t[#t + 1] = r.title end return table.concat(t, ",") end
local function command(i) return table.concat(windows[i or 1].layout.block.options.command, " ") end

test("default key binds the action, and setup moves it", function()
  assert(binds["alt+cmd+t"] == "t3_project_thread")
  P.setup{ key = "ctrl+alt+p" }
  assert(binds["alt+cmd+t"] == nil and binds["ctrl+alt+p"] == "t3_project_thread")
  assert(actions.t3_project_thread and actions.t3_project_remove)
end)

test("tr3x's path and environment come from tr3x.lua", function()
  local t = I.find_tools()
  assert(t.tr3x == tmp .. "/bin/tr3x", t.tr3x)
  assert(t.env.T3CODE_HOME == "/x/.t3" and t.env.TR3X_STATE_DIR == "/x/state")
end)

test("this machine's name comes from tr3x envs", function()
  assert(I.this_machine() == "Mac", tostring(I.this_machine()))
end)

test("project rows: T3's, then repos T3 lacks, then Add Folder", function()
  -- known is in T3 here; fresh is not; plain is no repository (and the
  -- project at that path is on another machine).
  local rows = I.project_rows()
  assert(titles(rows) == "known,far,fresh,Add Folder…", titles(rows))
  assert(rows[3].id == "dir:" .. HOME .. "/Projects/fresh", rows[3].id)
end)

test("a T3 project: pick where to work, then the composer", function()
  answer("known", "New worktree")
  I.new_thread({})
  local line = command()
  assert(line:find("--set-env T3CODE_HOME=/x/.t3", 1, true), line)
  assert(line:find("new --env envA --project p1 --workspace new", 1, true), line)
  assert(windows[1].session_id == "sess-home")
  assert(queued[1][1] == "session.select" and queued[1][2].window_id == "w1")
  assert(not read(log):find("t3 project add", 1, true), "no add for a known project")
end)

test("the workspace used last is listed first, and survives a reload", function()
  assert(I.workspace_rows("envA/p1")[1].id == "new")
  I.reset(); I.find_tools()
  assert(I.load_state().last["envA/p1"] == "new", "state round trip")
end)

test("a repository T3 lacks is added, then opens on its new id", function()
  answer("fresh", "Project folder")
  I.new_thread({})
  assert(read(log):find("t3 project add " .. HOME .. "/Projects/fresh", 1, true), read(log))
  assert(command():find("--env envA --project p2 --workspace local", 1, true), command())
  os.remove(added)
end)

test("Add Folder: a folder T3 already has is found by its path", function()
  answer("Add Folder…", HOME .. "/Projects/known", "Project folder")
  I.new_thread({})
  assert(asked[2].title == "Add Folder to T3")
  assert(command():find("--project p1 ", 1, true), command())
end)

test("Add Folder: a typed path that is no folder stops", function()
  answer("Add Folder…", { text = "/no/such/place" })
  I.new_thread({})
  assert(#windows == 0)
end)

test("cancelling the first picker does nothing", function()
  I.new_thread({})
  assert(#windows == 0 and #asked == 1)
end)

test("a session on another host is refused", function()
  local ok, err = pcall(I.new_thread, { server = "otherbox" })
  assert(not ok and err:find("otherbox", 1, true), tostring(err))
end)

test("remove: this machine's projects only, confirmed, then t3 project remove", function()
  answer("known", "remove")
  I.remove_project()
  assert(titles(asked[1].items) == "known", titles(asked[1].items))
  assert(read(log):find("t3 project remove p1", 1, true), read(log))
end)

test("remove: Keep it removes nothing", function()
  answer("known", "keep")
  I.remove_project()
  assert(not read(log):find("project remove", 1, true))
end)

os.execute("rm -rf " .. sh(tmp))
print(n .. " tests passed")
