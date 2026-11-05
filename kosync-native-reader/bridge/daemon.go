package main

// kosync-bridge daemon: KOReader progress sync (kosync protocol) for the
// reMarkable's built-in EPUB reader. Standalone: Go standard library only, its
// own login (config.json), no KOReader needed.
//
//   push: when the reader's page of an EPUB changes (or the panel / closing the
//         book asks), send the exact position (KOReader xpointer by text match)
//   pull: newer positions from other devices -> note per book in pull/<id>.json
//         (applied by the reader patch when the book is opened) or, for panel /
//         auto-pull requests, the target page directly
//
// Files in STATE_DIR (/home/root/.local/share/kosync-bridge):
//   config.json  server, username, userkey (md5 of the password)
//   state.json   device id, per-book digest and sync state
//   status.json  login status for the panel
//   cmd/ result/ requests from / answers to the reader's sync panel
//   pull/        notes for the reader patch

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	deviceName    = "reMarkable (built-in reader)"
	defaultServer = "https://sync.koreader.rocks"
	pushEvery     = 20 * time.Second
	pullEvery     = 120 * time.Second
)

var (
	xochitlDir = envOr("XOCHITL", "/home/root/.local/share/remarkable/xochitl")
	stateDir   = envOr("KOSYNC_BRIDGE_DIR", "/home/root/.local/share/kosync-bridge")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func logf(format string, a ...any) {
	fmt.Printf(time.Now().Format("15:04:05 ")+format+"\n", a...)
}

// ---- files ----------------------------------------------------------------------

func readJSON(path string, v any) bool {
	data, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return false
	}
	return json.Unmarshal(data, v) == nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeJSON(path string, v any) {
	data, _ := json.Marshal(v)
	writeFileAtomic(path, data)
}

// ---- config / state -------------------------------------------------------------

type config struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Userkey  string `json:"userkey"`
}

func loadConfig() config {
	var c config
	readJSON(filepath.Join(stateDir, "config.json"), &c)
	if c.Server == "" {
		c.Server = defaultServer
	}
	return c
}

func saveConfig(c config) { writeJSON(filepath.Join(stateDir, "config.json"), c) }

func (c config) loggedIn() bool { return c.Username != "" && c.Userkey != "" }

type bookState struct {
	Digest     string `json:"digest,omitempty"`
	PushedPage *int   `json:"pushed_page,omitempty"`
	PushedTS   int64  `json:"pushed_ts,omitempty"`
	PulledTS   int64  `json:"pulled_ts,omitempty"`
	ExpectPage *int   `json:"expect_page,omitempty"`
	RetryPage  *int   `json:"retry_page,omitempty"`
}

type stateFile struct {
	DeviceID string                `json:"device_id"`
	Books    map[string]*bookState `json:"books"`
}

var state stateFile

func loadState() {
	path := filepath.Join(stateDir, "state.json")
	if !readJSON(path, &state) {
		// e.g. written by an older version with other field types: keep at least
		// the device id (so our own earlier positions stay recognizable)
		var loose map[string]any
		if readJSON(path, &loose) {
			state.DeviceID, _ = loose["device_id"].(string)
		}
		state.Books = nil
	}
	if state.Books == nil {
		state.Books = map[string]*bookState{}
	}
	if state.DeviceID == "" {
		sum := md5.Sum([]byte(fmt.Sprint(time.Now().UnixNano(), os.Getpid(), "kosync-bridge")))
		state.DeviceID = strings.ToUpper(hex.EncodeToString(sum[:]))
	}
}

func saveState() { writeJSON(filepath.Join(stateDir, "state.json"), state) }

func intp(i int) *int { return &i }

// ---- the built-in reader's books ----------------------------------------------------

type bookInfo struct {
	page, pages int
	name        string
	openedTS    int64 // last opened on the reMarkable (s)
}

var validID = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

func readBookInfo(id string) (bookInfo, bool) {
	var meta struct {
		Deleted        bool   `json:"deleted"`
		Parent         string `json:"parent"`
		VisibleName    string `json:"visibleName"`
		LastOpenedPage int    `json:"lastOpenedPage"`
		LastOpened     string `json:"lastOpened"`
	}
	if !readJSON(filepath.Join(xochitlDir, id+".metadata"), &meta) || meta.Deleted || meta.Parent == "trash" {
		return bookInfo{}, false
	}
	var content struct {
		PageCount int `json:"pageCount"`
	}
	if !readJSON(filepath.Join(xochitlDir, id+".content"), &content) || content.PageCount < 1 {
		return bookInfo{}, false
	}
	var opened int64
	fmt.Sscan(meta.LastOpened, &opened)
	return bookInfo{page: meta.LastOpenedPage, pages: content.PageCount, name: meta.VisibleName, openedTS: opened / 1000}, true
}

func listEPUBs() []string {
	matches, _ := filepath.Glob(filepath.Join(xochitlDir, "*.epub"))
	var ids []string
	for _, m := range matches {
		ids = append(ids, strings.TrimSuffix(filepath.Base(m), ".epub"))
	}
	return ids
}

func book(id string) *bookState {
	b := state.Books[id]
	if b == nil {
		b = &bookState{}
		state.Books[id] = b
	}
	if b.Digest == "" {
		b.Digest, _ = partialMD5(filepath.Join(xochitlDir, id+".epub"))
	}
	return b
}

// position to push for a page: exact by text, else estimated
func pushPosition(id string, page, pages int) (string, float64, bool) {
	if xp, pct, ok := positionForPage(id, page); ok {
		return xp, pct, true
	}
	return "/body/DocFragment[1]/body", float64(page) / float64(pages), false
}

// page for a server position: exact by text, else estimated
func pullTarget(id string, res progressResp, pages int) (int, bool) {
	if p, ok := pageForRemote(id, res.Progress, res.Percentage); ok {
		return min(p, pages-1), true
	}
	t := int(math.Round(res.Percentage * float64(pages)))
	return max(0, min(pages-1, t)), false
}

// ---- kosync API -------------------------------------------------------------------

var httpClient = &http.Client{Timeout: 15 * time.Second}

type progressResp struct {
	Document   string  `json:"document"`
	Progress   string  `json:"progress"`
	Percentage float64 `json:"percentage"`
	Device     string  `json:"device"`
	DeviceID   string  `json:"device_id"`
	Timestamp  int64   `json:"timestamp"`
}

// request returns the HTTP status (0 if the server couldn't be reached), the body
// and the transport error.
func request(c config, method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, strings.TrimRight(c.Server, "/")+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/vnd.koreader.v1+json")
	req.Header.Set("x-auth-user", c.Username)
	req.Header.Set("x-auth-key", c.Userkey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, data, nil
}

func errText(what string, code int, body []byte, err error) string {
	if code == 0 {
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return what + " failed: the sync server didn't answer (timeout)."
		}
		return what + " failed: can't reach the sync server. Is Wi-Fi on?"
	}
	if code == 401 || code == 403 {
		return what + " failed: login rejected. Log in again under Server settings."
	}
	if code >= 500 {
		return fmt.Sprintf("%s failed: server error (%d). Try again later.", what, code)
	}
	var m struct {
		Message string `json:"message"`
	}
	json.Unmarshal(body, &m)
	if m.Message != "" {
		return fmt.Sprintf("%s failed (HTTP %d: %s).", what, code, m.Message)
	}
	return fmt.Sprintf("%s failed (HTTP %d).", what, code)
}

func pctText(p float64) string {
	if p < 0.1 {
		return fmt.Sprintf("%.1f%%", p*100)
	}
	return fmt.Sprintf("%d%%", int(math.Round(p*100)))
}

// putProgress pushes a page; returns the server timestamp.
func putProgress(c config, id string, b *bookState, page, pages int) (int64, string, float64, bool, string) {
	xp, pct, exact := pushPosition(id, page, pages)
	code, body, err := request(c, "PUT", "/syncs/progress", map[string]any{
		"document": b.Digest, "progress": xp, "percentage": pct,
		"device": deviceName, "device_id": state.DeviceID,
	})
	if code != 200 {
		return 0, xp, pct, exact, errText("Push", code, body, err)
	}
	var r struct {
		Timestamp int64 `json:"timestamp"`
	}
	json.Unmarshal(body, &r)
	if r.Timestamp == 0 {
		r.Timestamp = time.Now().Unix()
	}
	return r.Timestamp, xp, pct, exact, ""
}

func getProgress(c config, b *bookState) (*progressResp, string) {
	code, body, err := request(c, "GET", "/syncs/progress/"+b.Digest, nil)
	if code != 200 {
		return nil, errText("Pull", code, body, err)
	}
	var r progressResp
	if json.Unmarshal(body, &r) != nil || (r.Percentage == 0 && r.Progress == "" && r.Timestamp == 0) {
		return nil, ""
	}
	return &r, ""
}

// ---- background sync ----------------------------------------------------------------

func pushChanges(c config) {
	changed := false
	defer func() {
		if changed {
			saveState()
		}
	}()
	for _, id := range listEPUBs() {
		info, ok := readBookInfo(id)
		if !ok {
			continue
		}
		b := book(id)
		if b.Digest == "" {
			continue
		}
		if b.PushedPage == nil {
			// first time we see this book: take the current page as baseline without
			// pushing, so an older local position never overwrites the server
			b.PushedPage, b.PushedTS, changed = intp(info.page), 0, true
			continue
		}
		page := info.page
		if b.RetryPage != nil {
			page = *b.RetryPage // a failed push from the reader wins
		}
		if page == *b.PushedPage {
			if b.RetryPage != nil {
				b.RetryPage, changed = nil, true
			}
			continue
		}
		if b.ExpectPage != nil && *b.ExpectPage == page {
			// we just jumped there because of a pull: nothing new to report
			b.PushedPage, b.ExpectPage, b.RetryPage, changed = intp(page), nil, nil, true
			continue
		}
		ts, xp, pct, exact, errMsg := putProgress(c, id, b, page, info.pages)
		if errMsg != "" {
			logf("push failed %s: %s", info.name, errMsg)
			return // probably offline; retry next round
		}
		b.PushedPage, b.PushedTS, b.ExpectPage, b.RetryPage, changed = intp(page), ts, nil, nil, true
		logf("push %s page %d/%d (%.1f%%) %s%s", info.name, page+1, info.pages, pct*100, xp, map[bool]string{false: " (estimated)"}[exact])
	}
}

func pullChanges(c config) {
	changed := false
	defer func() {
		if changed {
			saveState()
		}
	}()
	for _, id := range listEPUBs() {
		info, ok := readBookInfo(id)
		if !ok {
			continue
		}
		b := book(id)
		if b.Digest == "" {
			continue
		}
		notePath := filepath.Join(stateDir, "pull", id+".json")
		res, errMsg := getProgress(c, b)
		if errMsg != "" {
			logf("pull failed: %s", errMsg)
			return
		}
		if res != nil && res.DeviceID != state.DeviceID {
			local := max(b.PushedTS, info.openedTS)
			if res.Timestamp > local && res.Timestamp > b.PulledTS {
				target, exact := pullTarget(id, *res, info.pages)
				b.PulledTS, changed = res.Timestamp, true
				if target != info.page {
					b.ExpectPage = intp(target)
					writeJSON(notePath, map[string]any{"page": target, "percentage": res.Percentage,
						"device": res.Device, "timestamp": res.Timestamp})
					logf("pull %s -> page %d/%d (%.1f%% from %s)%s", info.name, target+1, info.pages,
						res.Percentage*100, res.Device, map[bool]string{false: " (estimated)"}[exact])
				}
			}
		}
		// forget notes that were applied or overtaken by reading on this device
		var note struct {
			Page      *int  `json:"page"`
			Timestamp int64 `json:"timestamp"`
		}
		if readJSON(notePath, &note) && note.Page != nil && (*note.Page == info.page || b.PushedTS > note.Timestamp) {
			os.Remove(notePath)
		}
	}
}

// ---- requests from the reader's sync panel ---------------------------------------------

type command struct {
	Action   string `json:"action"`
	Doc      any    `json:"doc"`
	Page     int    `json:"page"`
	Pages    int    `json:"pages"`
	Auto     bool   `json:"auto"`
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
	KeepKey  bool   `json:"keep_key"`
}

func writeStatus(extra map[string]any) {
	c := loadConfig()
	st := map[string]any{}
	readJSON(filepath.Join(stateDir, "status.json"), &st)
	st["server"], st["username"], st["logged_in"] = c.Server, c.Username, c.loggedIn()
	for k, v := range extra {
		st[k] = v
	}
	data, _ := json.Marshal(st)
	path := filepath.Join(stateDir, "status.json")
	if old, err := os.ReadFile(path); err != nil || !bytes.Equal(old, data) {
		writeFileAtomic(path, data)
	}
}

func cmdPush(c config, id string, cmd command) (bool, string, map[string]any) {
	b := book(id)
	if b.Digest == "" || cmd.Pages < 1 {
		return false, "This book can't be synced.", nil
	}
	page := max(0, min(cmd.Pages-1, cmd.Page))
	if cmd.Auto && ((b.PushedPage != nil && page == *b.PushedPage) || (b.ExpectPage != nil && page == *b.ExpectPage)) {
		if b.ExpectPage != nil && page == *b.ExpectPage {
			b.PushedPage, b.ExpectPage = intp(page), nil
			saveState()
		}
		return true, "", map[string]any{"skipped": true}
	}
	ts, xp, pct, exact, errMsg := putProgress(c, id, b, page, cmd.Pages)
	if errMsg != "" {
		b.RetryPage = intp(page) // the background loop retries once we're online
		saveState()
		return false, errMsg, nil
	}
	b.PushedPage, b.PushedTS, b.ExpectPage, b.RetryPage = intp(page), ts, nil, nil
	os.Remove(filepath.Join(stateDir, "pull", id+".json"))
	saveState()
	logf("push (%s) %s page %d %s %s%s", map[bool]string{true: "on close", false: "panel"}[cmd.Auto], id, page+1, pctText(pct), xp, map[bool]string{false: " (estimated)"}[exact])
	return true, fmt.Sprintf("Pushed %s (page %d of %d).", pctText(pct), page+1, cmd.Pages), nil
}

func cmdPull(c config, id string, cmd command) (bool, string, map[string]any) {
	b := book(id)
	if b.Digest == "" || cmd.Pages < 1 {
		return false, "This book can't be synced.", nil
	}
	res, errMsg := getProgress(c, b)
	if errMsg != "" {
		return false, errMsg, nil
	}
	skipped := map[string]any{"skipped": true}
	if res == nil {
		if cmd.Auto {
			return true, "", skipped
		}
		return false, "No position saved on the server for this book yet.", nil
	}
	if res.DeviceID == state.DeviceID {
		if cmd.Auto {
			return true, "", skipped
		}
		return true, fmt.Sprintf("The latest position (%s) is from this reMarkable.", pctText(res.Percentage)), nil
	}
	if cmd.Auto && res.Timestamp <= b.PushedTS {
		return true, "", skipped
	}
	target, exact := pullTarget(id, *res, cmd.Pages)
	b.PulledTS = max(b.PulledTS, res.Timestamp)
	os.Remove(filepath.Join(stateDir, "pull", id+".json"))
	if target == cmd.Page {
		saveState()
		if cmd.Auto {
			return true, "", skipped
		}
		return true, fmt.Sprintf("Already at the latest position (%s).", pctText(res.Percentage)), nil
	}
	b.ExpectPage = intp(target)
	saveState()
	from := res.Device
	if from == "" {
		from = "another device"
	}
	logf("pull (%s) %s -> page %d %s from %s%s", map[bool]string{true: "on open", false: "panel"}[cmd.Auto], id, target+1, pctText(res.Percentage), from, map[bool]string{false: " (estimated)"}[exact])
	return true, fmt.Sprintf("Synced to %s from %s.", pctText(res.Percentage), from), map[string]any{"page": target}
}

func cmdAccount(cmd command) (bool, string) {
	c := loadConfig()
	server := strings.TrimRight(strings.Join(strings.Fields(cmd.Server), ""), "/")
	if server == "" {
		server = c.Server
	}
	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		server = "https://" + server
	}
	switch cmd.Action {
	case "logout":
		c.Username, c.Userkey = "", ""
		saveConfig(c)
		return true, "Logged out."
	case "server":
		c.Server = server
		saveConfig(c)
		return true, "Server set to " + server + "."
	}
	user := strings.TrimSpace(cmd.Username)
	key := ""
	if cmd.KeepKey {
		if key = c.Userkey; key == "" {
			return false, "Enter your password."
		}
	} else {
		if cmd.Password == "" {
			return false, "Enter a username and password."
		}
		sum := md5.Sum([]byte(cmd.Password))
		key = hex.EncodeToString(sum[:])
	}
	if user == "" {
		return false, "Enter a username and password."
	}
	try := config{Server: server, Username: user, Userkey: key}
	if cmd.Action == "register" {
		code, body, err := request(try, "POST", "/users/create", map[string]string{"username": user, "password": key})
		if code == 402 {
			return false, "That username is already taken."
		}
		if code != 201 && code != 200 {
			return false, errText("Registration", code, body, err)
		}
	} else {
		code, body, err := request(try, "GET", "/users/auth", nil)
		if code == 401 {
			return false, "Wrong username or password."
		}
		if code != 200 {
			return false, errText("Login", code, body, err)
		}
	}
	saveConfig(try)
	logf("%s ok as %s on %s", cmd.Action, user, server)
	if cmd.Action == "register" {
		return true, "Registered and logged in as " + user + "."
	}
	return true, "Logged in as " + user + "."
}

var cmdName = regexp.MustCompile(`^[\w-]+\.json$`)

func processCommands() {
	cmdDir, resDir := filepath.Join(stateDir, "cmd"), filepath.Join(stateDir, "result")
	entries, _ := os.ReadDir(cmdDir)
	var names []string
	for _, e := range entries {
		if cmdName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(cmdDir, name)
		var cmd command
		ok := readJSON(path, &cmd)
		os.Remove(path)
		if !ok || cmd.Action == "" {
			continue
		}
		result := handle(cmd)
		writeJSON(filepath.Join(resDir, name), result)
		writeStatus(map[string]any{"last_message": result["message"], "last_time": time.Now().Format("15:04")})
	}
	// answers the panel never picked up
	results, _ := os.ReadDir(resDir)
	for _, e := range results {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 5*time.Minute {
			os.Remove(filepath.Join(resDir, e.Name()))
		}
	}
}

func handle(cmd command) (result map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			result = map[string]any{"ok": false, "message": fmt.Sprint("Error: ", r)}
		}
	}()
	var ok bool
	var msg string
	var extra map[string]any
	switch cmd.Action {
	case "status":
		ok = true
	case "push", "pull":
		id, _ := cmd.Doc.(string)
		c := loadConfig()
		switch {
		case !validID.MatchString(id):
			ok, msg = false, "Sync failed: this document has no valid ID."
		case !c.loggedIn():
			ok, msg = false, "Not logged in. Log in under Server settings."
		case cmd.Action == "push":
			ok, msg, extra = cmdPush(c, id, cmd)
		default:
			ok, msg, extra = cmdPull(c, id, cmd)
		}
	default:
		ok, msg = cmdAccount(cmd)
	}
	result = map[string]any{"ok": ok, "message": msg}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

// ---- main loop --------------------------------------------------------------------

func runDaemon(once bool) {
	for _, d := range []string{stateDir, filepath.Join(stateDir, "cmd"), filepath.Join(stateDir, "result"), filepath.Join(stateDir, "pull")} {
		os.MkdirAll(d, 0o755)
	}
	loadState()
	saveState()
	logf("kosync-bridge started, device_id %s", state.DeviceID)
	writeStatus(nil)
	var lastPush, lastPull time.Time
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logf("error: %v", r)
				}
			}()
			processCommands()
			if time.Since(lastPush) >= pushEvery || once {
				lastPush = time.Now()
				if c := loadConfig(); c.loggedIn() {
					pushChanges(c)
					if time.Since(lastPull) >= pullEvery || once {
						pullChanges(c)
						lastPull = time.Now()
					}
				} else if once {
					logf("Not logged in: log in in the built-in reader (three dots > Progress sync).")
				}
				writeStatus(nil)
			}
		}()
		if once {
			return
		}
		time.Sleep(time.Second)
	}
}
