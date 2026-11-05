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

-- KOReader's Progress sync settings (server, username, md5 password key).
-- The single source of truth for both KOReader and the built-in reader.
local KOSYNC_SETTINGS = "settings/kosync.lua"
local DEFAULT_SERVER = "https://sync.koreader.rocks"

local function read_kosync_settings()
    local ok, s = pcall(dofile, KOSYNC_SETTINGS)
    return ok and type(s) == "table" and type(s.settings) == "table" and s.settings or {}
end

local function load_login()
    local t = read_kosync_settings()
    if not (t.username and t.userkey) then return nil end
    return {
        server = (t.custom_server or DEFAULT_SERVER):gsub("/+$", ""),
        user = t.username, key = t.userkey,
    }
end

-- Update KOReader's kosync settings, keeping all its other options.
local function save_kosync_settings(changes)
    local LuaSettings = require("luasettings")
    local s = LuaSettings:open(KOSYNC_SETTINGS)
    local t = s:readSetting("settings") or {}
    local defaults = { auto_sync = false, sync_forward = 1, sync_backward = 3,
                       checksum_method = 0, send_metadata = false }
    for k, v in pairs(defaults) do if t[k] == nil then t[k] = v end end
    for k, v in pairs(changes) do
        if v == false then t[k] = nil else t[k] = v end
    end
    s:saveSetting("settings", t)
    s:flush()
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
        elseif b and b.digest and (b.retry_page or info.page) ~= b.pushed_page then
            -- a failed push from the reader (retry_page) wins over the saved page
            local page = b.retry_page or info.page
            if b.expect_page == page then
                -- we just jumped there because of a pull: nothing new to report
                b.pushed_page, b.expect_page, b.retry_page = page, nil, nil
                changed = true
            else
                local pct = page / info.pages
                local xp = b.spine and xpointer_for(b.spine, pct) or "/body/DocFragment[1]/body"
                local code, res = request(login, "PUT", "/syncs/progress", json.encode({
                    document = b.digest, progress = xp, percentage = pct,
                    device = DEVICE_NAME, device_id = state.device_id,
                }))
                if code == 200 then
                    b.pushed_page, b.expect_page, b.retry_page = page, nil, nil
                    b.pushed_ts = type(res) == "table" and tonumber(res.timestamp) or os.time()
                    changed = true
                    log("push", info.name, string.format("page %d/%d (%.1f%%) %s", page, info.pages, pct * 100, xp))
                else
                    log("push failed", info.name, tostring(code), type(res) == "string" and res or "")
                    break -- probably offline; retry next round
                end
            end
        elseif b and b.retry_page and b.retry_page == b.pushed_page then
            b.retry_page = nil
            changed = true
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

-------------------------------------------------------------------------------
-- Requests from the built-in reader's sync panel (kosync-native-reader.qmd).
-- The panel writes <STATE>/cmd/<id>.json, we answer in <STATE>/result/<id>.json.
-------------------------------------------------------------------------------
local CMD_DIR, RESULT_DIR = STATE_DIR .. "/cmd", STATE_DIR .. "/result"
lfs.mkdir(CMD_DIR); lfs.mkdir(RESULT_DIR)
local STATUS_FILE = STATE_DIR .. "/status.json"

local function write_status(extra)
    local t = read_kosync_settings()
    local st = readjson(STATUS_FILE) or {}
    st.server = t.custom_server or DEFAULT_SERVER
    st.username = t.username or ""
    st.logged_in = (t.username and t.userkey) and true or false
    for k, v in pairs(extra or {}) do st[k] = v end
    local text = json.encode(st)
    if text ~= readfile(STATUS_FILE) then writefile(STATUS_FILE, text) end
end

local function pct_text(p) return p < 0.1 and string.format("%.1f%%", p * 100) or string.format("%d%%", math.floor(p * 100 + 0.5)) end

-- Human-readable error for a failed request (code = HTTP status, or nil + error text).
local function err_text(what, code, res)
    if not code then
        local e = tostring(res or "")
        if e:match("timeout") then return what .. " failed: the sync server didn't answer (timeout)." end
        return what .. " failed: can't reach the sync server. Is Wi-Fi on?"
    end
    if code == 401 or code == 403 then return what .. " failed: login rejected. Log in again under Server settings." end
    if code >= 500 then return what .. " failed: server error (" .. code .. "). Try again later." end
    local msg = type(res) == "table" and (res.message or res.error)
    return what .. " failed (HTTP " .. code .. (msg and (": " .. tostring(msg)) or "") .. ")."
end

-- c.auto = true: sent automatically when an EPUB is closed. Then nothing is pushed
-- if the page didn't change (or we only got there by pulling).
local function cmd_push(login, c)
    local b = book_state(c.doc)
    local pages = tonumber(c.pages) or 0
    if not (b and b.digest) or pages < 1 then return false, "This book can't be synced." end
    local page = math.max(0, math.min(pages - 1, tonumber(c.page) or 0))
    if c.auto and (page == b.pushed_page or page == b.expect_page) then
        if page == b.expect_page then b.pushed_page, b.expect_page = page, nil; save_state() end
        return true, "", { skipped = true }
    end
    local pct = page / pages
    local xp = b.spine and xpointer_for(b.spine, pct) or "/body/DocFragment[1]/body"
    local code, res = request(login, "PUT", "/syncs/progress", json.encode({
        document = b.digest, progress = xp, percentage = pct,
        device = DEVICE_NAME, device_id = state.device_id,
    }))
    if code ~= 200 then
        -- remember it: the background loop retries once we're online again
        b.retry_page = page; save_state()
        return false, err_text("Push", code, res)
    end
    b.pushed_page, b.expect_page, b.retry_page = page, nil, nil
    b.pushed_ts = type(res) == "table" and tonumber(res.timestamp) or os.time()
    os.remove(STATE_DIR .. "/pull/" .. c.doc .. ".json")
    save_state()
    log(c.auto and "push (on close)" or "push (panel)", c.doc, pct_text(pct))
    return true, "Pushed " .. pct_text(pct) .. " (page " .. (page + 1) .. " of " .. pages .. ")."
end

-- c.auto = true: sent automatically when an EPUB is opened. Then we only move if the
-- server position is newer than this reMarkable's last push of the book.
local function cmd_pull(login, c)
    local b = book_state(c.doc)
    local pages = tonumber(c.pages) or 0
    if not (b and b.digest) or pages < 1 then return false, "This book can't be synced." end
    local code, res = request(login, "GET", "/syncs/progress/" .. b.digest)
    if code ~= 200 then return false, err_text("Pull", code, res) end
    if type(res) ~= "table" or not tonumber(res.percentage) then
        if c.auto then return true, "", { skipped = true } end
        return false, "No position saved on the server for this book yet."
    end
    local ts = tonumber(res.timestamp) or 0
    if res.device_id == state.device_id then
        if c.auto then return true, "", { skipped = true } end
        return true, "The latest position (" .. pct_text(res.percentage) .. ") is from this reMarkable."
    end
    if c.auto and ts <= (b.pushed_ts or 0) then
        return true, "", { skipped = true }
    end
    local target = math.floor(tonumber(res.percentage) * pages + 0.5)
    target = math.max(0, math.min(pages - 1, target))
    b.pulled_ts = math.max(b.pulled_ts or 0, ts)
    os.remove(STATE_DIR .. "/pull/" .. c.doc .. ".json")
    if target == tonumber(c.page) then
        save_state()
        if c.auto then return true, "", { skipped = true } end
        return true, "Already at the latest position (" .. pct_text(res.percentage) .. ")."
    end
    b.expect_page = target
    save_state()
    log(c.auto and "pull (on open)" or "pull (panel)", c.doc, pct_text(res.percentage), tostring(res.device))
    return true, "Synced to " .. pct_text(res.percentage) .. " from " .. tostring(res.device or "another device") .. ".",
           { page = target }
end

local function cmd_account(c)
    local server = (c.server and c.server ~= "" and c.server or DEFAULT_SERVER):gsub("%s", ""):gsub("/+$", "")
    if not server:match("^https?://") then server = "https://" .. server end
    if c.action == "logout" then
        save_kosync_settings({ username = false, userkey = false })
        return true, "Logged out."
    end
    if c.action == "server" then
        save_kosync_settings({ custom_server = server })
        return true, "Server set to " .. server .. "."
    end
    local user, pass = c.username or "", c.password or ""
    local key
    if c.keep_key then
        -- password field still shows the saved (hidden) password: reuse its key
        key = read_kosync_settings().userkey
        if not key then return false, "Enter your password." end
    else
        if pass == "" then return false, "Enter a username and password." end
        key = md5(pass)
    end
    if user == "" then return false, "Enter a username and password." end
    local temp = { server = server, user = user, key = key }
    local code, res
    if c.action == "register" then
        code, res = request(temp, "POST", "/users/create",
                            json.encode({ username = user, password = temp.key }))
        if code == 402 then return false, "That username is already taken." end
        if code ~= 201 and code ~= 200 then return false, "Registration failed (" .. tostring(code or res) .. ")." end
    else
        code, res = request(temp, "GET", "/users/auth")
        if code == 401 then return false, "Wrong username or password." end
        if code ~= 200 then return false, "Login failed (" .. tostring(code or res) .. "). Check the server and Wi-Fi." end
    end
    save_kosync_settings({ custom_server = server, username = user, userkey = temp.key })
    log(c.action, "ok as", user, "on", server)
    return true, (c.action == "register" and "Registered and logged in as " or "Logged in as ") .. user .. "."
end

local function process_commands()
    local files = {}
    for f in lfs.dir(CMD_DIR) do
        if f:match("^[%w%-_]+%.json$") then files[#files + 1] = f end
    end
    table.sort(files)
    for _, f in ipairs(files) do
        local path = CMD_DIR .. "/" .. f
        local c = readjson(path)
        os.remove(path)
        if c and c.action then
            if c.doc ~= nil and (type(c.doc) ~= "string" or not c.doc:match("^[%w%-]+$")) then
                c.doc = nil
            end
            local ok, okres, msg, extra = pcall(function()
                if (c.action == "push" or c.action == "pull") and not c.doc then
                    return false, "Sync failed: this document has no valid ID."
                end
                if c.action == "status" then return true, "" end
                if c.action == "push" or c.action == "pull" then
                    local login = load_login()
                    if not login then return false, "Not logged in. Log in under Server settings." end
                    if c.action == "push" then return cmd_push(login, c) end
                    return cmd_pull(login, c)
                end
                return cmd_account(c)
            end)
            if not ok then okres, msg = false, "Error: " .. tostring(okres) end
            local result = { ok = okres and true or false, message = msg or "" }
            for k, v in pairs(extra or {}) do result[k] = v end
            writefile(RESULT_DIR .. "/" .. f, json.encode(result))
            write_status({ last_message = result.message, last_time = os.date("%H:%M") })
        end
    end
    -- results the panel never picked up
    for f in lfs.dir(RESULT_DIR) do
        local p = RESULT_DIR .. "/" .. f
        local attr = lfs.attributes(p)
        if attr and attr.mode == "file" and os.time() - attr.modification > 300 then os.remove(p) end
    end
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
write_status()
local last_pull, last_push = 0, 0
while true do
    local ok, err = pcall(process_commands)
    if not ok then log("command error:", tostring(err)) end
    if os.time() - last_push >= PUSH_EVERY or ONCE then
        last_push = os.time()
        local login = load_login()
        if login then
            ok, err = pcall(push_changes, login)
            if not ok then log("push error:", tostring(err)) end
            if os.time() - last_pull >= PULL_EVERY or ONCE then
                ok, err = pcall(pull_changes, login)
                if not ok then log("pull error:", tostring(err)) end
                last_pull = os.time()
            end
        elseif ONCE then
            log("No Progress sync login in KOReader (settings/kosync.lua).")
        end
        write_status()
    end
    if ONCE then break end
    socket.sleep(1)
end
