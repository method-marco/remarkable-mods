// sleepscreen-library: turns the documents in the reMarkable library folder
// "Screensaver" into full-screen sleep screen PNGs (one per document) for
// sleepscreen-rotate. Images imported into the library are stored as PDFs: the
// photo is taken out of the PDF, center-cropped to the screen's aspect ratio and
// resized. Converted pictures are kept until the document changes or leaves the
// folder (moved, deleted, trashed). Go standard library only.
//
//	sleepscreen-library [sync]       update the pictures, print what changed
//	sleepscreen-library remove       only drop pictures of documents that left the folder
//	sleepscreen-library convert <pdf> <out.png>   one file (testing)
package main

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	xochitlDir = envOr("XOCHITL_DIR", "/home/root/.local/share/remarkable/xochitl")
	outDir     = envOr("OUT_DIR", "/home/root/sleepscreens/library")
	folderName = envOr("FOLDER", "Screensaver")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type entry struct {
	id, name, parent, kind string
	deleted                bool
}

func readLibrary() map[string]entry {
	out := map[string]entry{}
	files, _ := filepath.Glob(filepath.Join(xochitlDir, "*.metadata"))
	for _, f := range files {
		var m struct {
			Deleted     bool   `json:"deleted"`
			Parent      string `json:"parent"`
			Type        string `json:"type"`
			VisibleName string `json:"visibleName"`
		}
		data, err := os.ReadFile(f)
		if err != nil || json.Unmarshal(data, &m) != nil {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(f), ".metadata")
		out[id] = entry{id: id, name: m.VisibleName, parent: m.Parent, kind: m.Type, deleted: m.Deleted}
	}
	return out
}

// sources: the PDF documents directly in a library folder named "Screensaver"
// (any case). found is false if there is no such folder.
func sources(lib map[string]entry) (map[string]entry, bool) {
	alive := func(e entry) bool {
		for i := 0; i < 64 && e.id != ""; i++ {
			if e.deleted || e.parent == "trash" {
				return false
			}
			if e.parent == "" {
				return true
			}
			e = lib[e.parent]
		}
		return e.id == ""
	}
	inFolder := func(e entry) bool {
		p, ok := lib[e.parent]
		return ok && p.kind == "CollectionType" && strings.EqualFold(strings.TrimSpace(p.name), folderName)
	}
	found := false
	for _, e := range lib {
		if e.kind == "CollectionType" && strings.EqualFold(strings.TrimSpace(e.name), folderName) && alive(e) {
			found = true
		}
	}
	docs := map[string]entry{}
	for id, e := range lib {
		if e.kind == "DocumentType" && alive(e) && inFolder(e) {
			if _, err := os.Stat(filepath.Join(xochitlDir, id+".pdf")); err == nil {
				docs[id] = e
			}
		}
	}
	return docs, found
}

func screenSize() (int, int) {
	if f, err := os.Open("/usr/share/remarkable/suspended.png"); err == nil {
		defer f.Close()
		if c, err := png.DecodeConfig(f); err == nil && c.Width > 0 && c.Height > 0 {
			return c.Width, c.Height
		}
	}
	return 954, 1696 // Paper Pro Move
}

func convert(pdf, out string, w, h int) error {
	img, orientation, err := largestImage(pdf)
	if err != nil {
		return err
	}
	// crop + resize in the stored orientation, then turn the small result upright
	tw, th := w, h
	if orientSwaps(orientation) {
		tw, th = h, w
	}
	pic := applyOrientation(coverResize(img, tw, th), orientation)
	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(f, pic); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, out)
}

// sync brings the pictures in line with the folder. Removing pictures of documents
// that left it is instant; converting new documents takes a few seconds each and
// only happens with convert (sleepscreen-rotate runs that in the background).
func sync(convertNew bool) int {
	lib := readLibrary()
	docs, found := sources(lib)
	if !found {
		// no folder: no pictures
		if old, _ := filepath.Glob(filepath.Join(outDir, "*.png")); len(old) > 0 {
			for _, p := range old {
				os.Remove(p)
			}
			fmt.Printf("library folder %q is gone: removed %d picture(s)\n", folderName, len(old))
		}
		return 0
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Println(err)
		return 1
	}
	// pictures of documents that left the folder (and failure marks)
	existing, _ := filepath.Glob(filepath.Join(outDir, "*.png"))
	marks, _ := filepath.Glob(filepath.Join(outDir, "*.png.failed"))
	for _, p := range append(existing, marks...) {
		id := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(p), ".failed"), ".png")
		if _, ok := docs[id]; !ok {
			os.Remove(p)
			if !strings.HasSuffix(p, ".failed") {
				fmt.Printf("remove %s\n", id)
			}
		}
	}
	if !convertNew {
		return 0
	}
	// half-written files of an interrupted run (only one converting run at a time)
	if tmps, _ := filepath.Glob(filepath.Join(outDir, "*.png.tmp")); len(tmps) > 0 {
		for _, t := range tmps {
			os.Remove(t)
		}
	}
	w, h := screenSize()
	sizeFile := filepath.Join(outDir, ".size")
	size := fmt.Sprintf("%dx%d v2", w, h) // bump the version when the conversion changes
	if old, _ := os.ReadFile(sizeFile); string(old) != size {
		pngs, _ := filepath.Glob(filepath.Join(outDir, "*.png"))
		for _, p := range pngs {
			os.Remove(p)
		}
		os.WriteFile(sizeFile, []byte(size), 0o644)
	}
	ids := make([]string, 0, len(docs))
	for id := range docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		pdf := filepath.Join(xochitlDir, id+".pdf")
		out := filepath.Join(outDir, id+".png")
		ps, err := os.Stat(pdf)
		if err != nil {
			continue // deleted meanwhile
		}
		if st, err := os.Stat(out); err == nil && !ps.ModTime().After(st.ModTime()) && os.Getenv("FORCE") == "" {
			continue // converted and unchanged since
		}
		badMark := out + ".failed"
		if bs, err := os.Stat(badMark); err == nil && !ps.ModTime().After(bs.ModTime()) {
			continue // known not to work, unchanged since
		}
		if err := convert(pdf, out, w, h); err != nil {
			fmt.Printf("skip   %s: %v\n", docs[id].name, err)
			os.WriteFile(badMark, []byte(err.Error()), 0o644)
			continue
		}
		os.Remove(badMark)
		fmt.Printf("add    %s\n", docs[id].name)
	}
	return 0
}

func main() {
	args := os.Args[1:]
	switch {
	case len(args) == 0 || args[0] == "sync":
		os.Exit(sync(true))
	case args[0] == "remove":
		os.Exit(sync(false))
	case args[0] == "convert" && len(args) == 3:
		w, h := screenSize()
		if err := convert(args[1], args[2], w, h); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	default:
		fmt.Println("usage: sleepscreen-library [sync] | remove | convert <pdf> <out.png>")
		os.Exit(2)
	}
}
