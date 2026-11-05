--[[
kosync-bridge: KOReader progress sync for the reMarkable's built-in EPUB reader.

Runs as a background service with KOReader's own LuaJIT runtime (for HTTPS, MD5,
EPUB unpacking and JSON) and KOReader's Progress sync login (settings/kosync.lua).

  push: when the built-in reader's page of an EPUB changes, send the position to the
        kosync server as a percentage plus a KOReader position (xpointer) at the
        start of the matching chapter, so any KOReader can jump there.
  pull: when another device saved a newer position, write a note
        <STATE>/pull/<document-id>.json {page, percentage, ...}; the reader patch
        (kosync-native-reader.qmd) jumps to that page when the book is opened.

Usage (from the KOReader directory): ./luajit kosync-bridge.lua [--once]
]]

require("setupkoenv")
local lfs = require("libs/libkoreader-lfs")
local md5 = require("ffi/sha2").md5
local Archiver = require("ffi/archiver")
local https = require("ssl.https")
local http = require("socket.http")
local ltn12 = require("ltn12")
local json = require("rapidjson")
local socket = require("socket")

local XOCHITL = os.getenv("XOCHITL") or "/home/root/.local/share/remarkable/xochitl"
local STATE_DIR = os.getenv("KOSYNC_BRIDGE_DIR") or "/home/root/.local/share/kosync-bridge"
local DEVICE_NAME = "reMarkable (built-in reader)"
local PUSH_EVERY = 20     -- seconds between local page checks
local PULL_EVERY = 120    -- seconds between server checks
local ONCE = arg and arg[1] == "--once"

local function log(...)
    io.stdout:write(os.date("%H:%M:%S "), table.concat({...}, " "), "\n")
    io.stdout:flush()
end

local function readfile(p)
    local f = io.open(p, "rb"); if not f then return nil end
    local s = f:read("*a"); f:close(); return s
end

local function writefile(p, s)
    local tmp = p .. ".tmp"
    local f = io.open(tmp, "wb"); if not f then return false end
    f:write(s); f:close()
    return os.rename(tmp, p)
end

local function readjson(p)
    local s = readfile(p); if not s or s == "" then return nil end
    local ok, v = pcall(json.decode, s)
    if ok and type(v) == "table" then return v end
end

lfs.mkdir(STATE_DIR)
lfs.mkdir(STATE_DIR .. "/pull")
local STATE_FILE = STATE_DIR .. "/state.json"
local state = readjson(STATE_FILE) or {}
state.books = state.books or {}
if not state.device_id then
    state.device_id = md5(tostring(os.time()) .. tostring(math.random()) .. "kosync-bridge"):upper()
end
local function save_state() writefile(STATE_FILE, json.encode(state)) end

-- KOReader's Progress sync settings (server, username, md5 password key)
local function load_login()
    local ok, s = pcall(dofile, "settings/kosync.lua")
    local t = ok and type(s) == "table" and s.settings or nil
    if not (t and t.username and t.userkey) then return nil end
    return {
        server = (t.custom_server or "https://sync.koreader.rocks"):gsub("/+$", ""),
        user = t.username, key = t.userkey,
    }
end

-- Same digest as KOReader's "binary" document matching (util.partialMD5).
local function partial_md5(path)
    local f = io.open(path, "rb"); if not f then return nil end
    local update = md5()
    for i = -1, 10 do
        f:seek("set", bit.lshift(1024, 2 * i))
        local sample = f:read(1024)
        if not sample then break end
        update(sample)
    end
    f:close()
    return update()
end

local function xml_attr(tag, name)
    return tag:match("[%s]" .. name .. '%s*=%s*"([^"]*)"') or tag:match("[%s]" .. name .. "%s*=%s*'([^']*)'")
end

local function url_unescape(s)
    return (s:gsub("%%(%x%x)", function(h) return string.char(tonumber(h, 16)) end))
end

-- Reading order of the EPUB as cumulative size fractions per spine item:
-- spine[i] = fraction of the book's text that comes before item i.
-- KOReader (crengine) numbers spine items as /body/DocFragment[i].
local function epub_spine(path)
    local a = Archiver.Reader:new()
    if not a:open(path) then return nil end
    local sizes = {}
    for e in a:iterate() do sizes[e.path] = tonumber(e.size) end
    local container = a:extractToMemory("META-INF/container.xml")
    local opf_path = container and container:match('full%-path%s*=%s*"([^"]+)"')
    local opf = opf_path and a:extractToMemory(opf_path)
    a:close()
    if not opf then return nil end
    local base = opf_path:match("^(.*/)") or ""
    local manifest = {}
    for tag in opf:gmatch("<[%w:]*item%s[^>]*>") do
        local id, href = xml_attr(tag, "id"), xml_attr(tag, "href")
        if id and href then manifest[id] = base .. url_unescape(href) end
    end
    local spine_xml = opf:match("<[%w:]*spine[%s>].-</[%w:]*spine>") or ""
    local items = {}
    for tag in spine_xml:gmatch("<[%w:]*itemref%s[^>]*>") do
        local href = manifest[xml_attr(tag, "idref") or ""]
        items[#items + 1] = math.max(sizes[href or ""] or 0, 1)
    end
    if #items == 0 then return nil end
    local total, spine, acc = 0, {}, 0
    for _, s in ipairs(items) do total = total + s end
    for i, s in ipairs(items) do spine[i] = acc / total; acc = acc + s end
    return spine
end

local function xpointer_for(spine, pct)
    local idx = 1
    for i = 1, #spine do if spine[i] <= pct then idx = i end end
    return string.format("/body/DocFragment[%d]/body", idx)
end

-- kosync HTTP API
local function request(login, method, path, body)
    local resp = {}
    local headers = {
        ["Accept"] = "application/vnd.koreader.v1+json",
        ["x-auth-user"] = login.user,
        ["x-auth-key"] = login.key,
    }
    local src
    if body then
        headers["Content-Type"] = "application/json"
        headers["Content-Length"] = tostring(#body)
        src = ltn12.source.string(body)
    end
    local url = login.server .. path
    local lib = url:match("^https:") and https or http
    lib.TIMEOUT = 15
    local ok, code = lib.request{ url = url, method = method, headers = headers,
                                  source = src, sink = ltn12.sink.table(resp) }
    if not ok then return nil, tostring(code) end
    local text = table.concat(resp)
    local decoded = text ~= "" and select(2, pcall(json.decode, text)) or nil
    return tonumber(code), decoded
end

-- The built-in reader's state of one EPUB
local function book_info(id)
    local meta = readjson(XOCHITL .. "/" .. id .. ".metadata")
    if not meta or meta.deleted or meta.parent == "trash" then return nil end
    local content_raw = readfile(XOCHITL .. "/" .. id .. ".content") or ""
    local pages = tonumber(content_raw:match('"pageCount"%s*:%s*(%d+)'))
    if not pages or pages < 1 then return nil end
    return { page = tonumber(meta.lastOpenedPage) or 0, pages = pages,
             name = meta.visibleName or id,
             -- when the book was last opened on the reMarkable (ms -> s)
             opened_ts = math.floor((tonumber(meta.lastOpened) or 0) / 1000) }
end

local function list_epubs()
    local ids = {}
    for f in lfs.dir(XOCHITL) do
        local id = f:match("^(.+)%.epub$")
        if id then ids[#ids + 1] = id end
    end
    return ids
end

local function book_state(id)
    local b = state.books[id]
    if not b then
        b = {}; state.books[id] = b
    end
    if not b.digest then
        b.digest = partial_md5(XOCHITL .. "/" .. id .. ".epub")
        b.spine = epub_spine(XOCHITL .. "/" .. id .. ".epub")
    end
    return b
end

local function push_changes(login)
    local changed = false
    for _, id in ipairs(list_epubs()) do
        local info = book_info(id)
        local b = info and book_state(id)
        if b and b.digest and b.pushed_page == nil then
            -- first time we see this book: take the current page as baseline without
            -- pushing, so an older local position never overwrites the server
            b.pushed_page, b.pushed_ts = info.page, 0
            changed = true
        elseif b and b.digest and info.page ~= b.pushed_page then
            if b.expect_page == info.page then
                -- we just jumped there because of a pull: nothing new to report
                b.pushed_page, b.expect_page = info.page, nil
                changed = true
            else
                local pct = info.page / info.pages
                local xp = b.spine and xpointer_for(b.spine, pct) or "/body/DocFragment[1]/body"
                local code, res = request(login, "PUT", "/syncs/progress", json.encode({
                    document = b.digest, progress = xp, percentage = pct,
                    device = DEVICE_NAME, device_id = state.device_id,
                }))
                if code == 200 then
                    b.pushed_page = info.page
                    b.pushed_ts = type(res) == "table" and tonumber(res.timestamp) or os.time()
                    b.expect_page = nil
                    changed = true
                    log("push", info.name, string.format("page %d/%d (%.1f%%) %s", info.page, info.pages, pct * 100, xp))
                else
                    log("push failed", info.name, tostring(code), type(res) == "string" and res or "")
                    break -- probably offline; retry next round
                end
            end
        end
    end
    if changed then save_state() end
end

local function pull_changes(login)
    local changed = false
    for _, id in ipairs(list_epubs()) do
        local info = book_info(id)
        local b = info and book_state(id)
        local note = STATE_DIR .. "/pull/" .. id .. ".json"
        if b and b.digest then
            local code, res = request(login, "GET", "/syncs/progress/" .. b.digest)
            if not code then log("pull failed:", tostring(res)); break end
            if code == 200 and type(res) == "table" and tonumber(res.percentage)
               and res.device_id ~= state.device_id then
                local ts = tonumber(res.timestamp) or 0
                -- only positions saved after the last local reading/push count as newer
                local local_ts = math.max(b.pushed_ts or 0, info.opened_ts or 0)
                if ts > local_ts and ts > (b.pulled_ts or 0) then
                    local target = math.floor(tonumber(res.percentage) * info.pages + 0.5)
                    target = math.max(0, math.min(info.pages - 1, target))
                    b.pulled_ts = ts
                    changed = true
                    if target ~= info.page then
                        b.expect_page = target
                        writefile(note, json.encode({ page = target, percentage = res.percentage,
                                                      device = res.device, timestamp = ts }))
                        log("pull", info.name, string.format("-> page %d/%d (%.1f%% from %s)",
                            target, info.pages, res.percentage * 100, tostring(res.device)))
                    end
                end
            end
            -- forget notes that were applied (or overtaken by reading on this device)
            local n = readjson(note)
            if n and n.page and (n.page == info.page or (b.pushed_ts or 0) > (n.timestamp or 0)) then
                os.remove(note)
            end
        end
    end
    if changed then save_state() end
end

if arg and arg[1] == "--check" then
    -- offline self-test: digests and chapter mapping, no network, no state changes
    for _, id in ipairs(list_epubs()) do
        local info = book_info(id)
        if info then
            local path = XOCHITL .. "/" .. id .. ".epub"
            local spine = epub_spine(path)
            local pct = tonumber(arg[2]) or info.page / info.pages
            print(string.format("%s\n  digest %s  page %d/%d  chapters %d  %.1f%% -> %s",
                info.name, tostring(partial_md5(path)), info.page, info.pages,
                spine and #spine or 0, pct * 100, spine and xpointer_for(spine, pct) or "?"))
        end
    end
    os.exit(0)
end

log("kosync-bridge started, device_id " .. state.device_id)
local last_pull = 0
while true do
    local login = load_login()
    if login then
        local ok, err = pcall(push_changes, login)
        if not ok then log("push error:", tostring(err)) end
        if os.time() - last_pull >= PULL_EVERY or ONCE then
            ok, err = pcall(pull_changes, login)
            if not ok then log("pull error:", tostring(err)) end
            last_pull = os.time()
        end
    elseif ONCE then
        log("No Progress sync login in KOReader (settings/kosync.lua).")
    end
    if ONCE then break end
    socket.sleep(PUSH_EVERY)
end
