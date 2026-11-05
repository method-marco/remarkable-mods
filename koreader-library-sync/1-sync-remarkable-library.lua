-- KOReader userpatch (priority 1 = "early", runs on every launch before the UI starts).
-- Mirrors the reMarkable library's EPUBs into KOReader's home folder.
-- Survives KOReader OTA updates (the patches/ dir is not part of the package).
local script = "/home/root/bin/rm-library-sync.sh"
local books = "/home/root/books"

local f = io.open(script, "r")
if f then
    f:close()
    os.execute("/bin/sh '" .. script .. "'")
end

-- Point KOReader's home folder at the mirror (only if no home folder is set yet).
local ok, err = pcall(function()
    local DataStorage = require("datastorage")
    local settings = require("luasettings"):open(DataStorage:getDataDir() .. "/settings.reader.lua")
    if not settings:has("home_dir") then
        settings:saveSetting("home_dir", books)
        settings:saveSetting("lastdir", books)
        settings:flush()
    end
end)
if not ok then
    require("logger").warn("rm-library-sync: could not set home_dir:", err)
end
