-- A JSON codec just big enough for tr3x's lists and the state file, standing
-- in for rex.json in test/run.lua.
local json = {}

function json.encode(v)
  if type(v) == "string" then return '"' .. v:gsub('[\\"]', "\\%0") .. '"' end
  if type(v) ~= "table" then return tostring(v) end
  local parts = {}
  if #v > 0 then
    for _, x in ipairs(v) do parts[#parts + 1] = json.encode(x) end
    return "[" .. table.concat(parts, ",") .. "]"
  end
  for k, x in pairs(v) do parts[#parts + 1] = json.encode(k) .. ":" .. json.encode(x) end
  return "{" .. table.concat(parts, ",") .. "}"
end

function json.decode(s)
  local pos = 1
  local function ws() pos = s:find("%S", pos) or #s + 1 end
  local value
  function value()
    ws()
    local c = s:sub(pos, pos)
    if c == '"' then
      local out = {}
      pos = pos + 1
      while s:sub(pos, pos) ~= '"' do
        if s:sub(pos, pos) == "\\" then pos = pos + 1 end
        out[#out + 1] = s:sub(pos, pos)
        pos = pos + 1
      end
      pos = pos + 1
      return table.concat(out)
    elseif c == "[" or c == "{" then
      local obj, close = {}, c == "[" and "]" or "}"
      pos = pos + 1
      ws()
      if s:sub(pos, pos) == close then pos = pos + 1; return obj end
      while true do
        if close == "]" then
          obj[#obj + 1] = value()
        else
          local k = value()
          ws(); pos = pos + 1
          obj[k] = value()
        end
        ws()
        local d = s:sub(pos, pos)
        pos = pos + 1
        if d == close then return obj end
      end
    end
    local lit = s:match("^[%w%.%-]+", pos)
    if lit == nil then error("bad json at " .. pos) end
    pos = pos + #lit
    if lit == "true" then return true elseif lit == "false" then return false elseif lit == "null" then return nil end
    return tonumber(lit)
  end
  return value()
end

return json
