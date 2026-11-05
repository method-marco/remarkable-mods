--[[
KOReader userpatch (priority 2 = "late"): exact positions from the reMarkable's
built-in reader.

kosync-bridge sends the built-in reader's position as a percentage plus a KOReader
position at the start of the matching chapter (that's all it can know). Plain
KOReader only uses the chapter position, so it lands at the chapter start. With this
patch, KOReader jumps to the exact percentage instead whenever the position came
from the bridge (device "reMarkable (built-in reader)").
]]
local userpatch = require("userpatch")
local Event = require("ui/event")
local logger = require("logger")

local BRIDGE_DEVICE = "reMarkable (built-in reader)"
local last_body -- most recent answer from the sync server

userpatch.registerPatchPluginFunc("kosync", function(KOSync)
    if KOSync._native_reader_percent_patched then return end
    KOSync._native_reader_percent_patched = true

    local ok, KOSyncClient = pcall(require, "KOSyncClient")
    if ok and KOSyncClient and KOSyncClient.get_progress then
        local orig_get = KOSyncClient.get_progress
        KOSyncClient.get_progress = function(self, username, userkey, document, callback)
            return orig_get(self, username, userkey, document, function(res_ok, body)
                last_body = res_ok and body or nil
                return callback(res_ok, body)
            end)
        end
    end

    local orig_sync = KOSync.syncToProgress
    KOSync.syncToProgress = function(self, progress)
        local b = last_body
        if b and b.device == BRIDGE_DEVICE and b.progress == progress
           and tonumber(b.percentage) and not self.ui.document.info.has_pages then
            logger.info("kosync-native-reader: going to", b.percentage * 100, "% from the built-in reader")
            self.ui:handleEvent(Event:new("GotoPercent", tonumber(b.percentage) * 100))
            return
        end
        return orig_sync(self, progress)
    end
end)
