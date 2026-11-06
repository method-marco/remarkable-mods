package main

// Fallback when a page can't be matched by text (comics, image-only pages, very
// short pages): the reader's own table of contents.
//
// The built-in reader computes, for its current layout, the page where each
// chapter starts (the patch saves it as toc/<id>.json). The EPUB's table of
// contents (nav / NCX) has the same chapters with links into the book. Matching
// them gives one exact anchor per chapter (reader page <-> EPUB position), and
// within a chapter we interpolate: by the chapter's text if it has text, else by
// its spine items (comics usually have one file per page).

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
)

type readerTOC struct {
	PageCount int `json:"pageCount"`
	Toc       []struct {
		Name  string `json:"name"`
		Page  int    `json:"page"`
		Level int    `json:"level"`
	} `json:"toc"`
}

type chapterAnchor struct {
	page int // reader page (0-based) where the chapter starts
	frag int // spine item (1-based)
	norm int // index in the book's normalized text, -1 if the chapter has no text
}

// chapterAnchors: anchors for the current layout, sorted, plus start/end sentinels.
func chapterAnchors(bk *bookIndex, pages int) []chapterAnchor {
	var rt readerTOC
	if !readJSON(filepath.Join(stateDir, "toc", bk.id+".json"), &rt) || rt.PageCount != pages || len(rt.Toc) == 0 {
		return nil // no TOC from the reader yet, or from an older layout
	}
	epubTOC := bk.epub.toc
	var out []chapterAnchor
	j := 0
	for i, e := range rt.Toc {
		match := -1
		want := norm(e.Name)
		for k := j; k < len(epubTOC) && k < j+8; k++ {
			if norm(epubTOC[k].title) == want {
				match = k
				break
			}
		}
		if match < 0 && len(rt.Toc) == len(epubTOC) {
			match = i // same number of entries: same order
		}
		if match < 0 || epubTOC[match].frag < 1 || epubTOC[match].frag > len(bk.epub.frags) {
			continue
		}
		j = match + 1
		te := epubTOC[match]
		a := chapterAnchor{page: e.Page, frag: te.frag, norm: -1}
		el := bk.epub.frags[te.frag-1]
		if te.anchor != "" {
			if f := findID(el, te.anchor); f != nil {
				el = f
			}
		}
		if el != nil {
			t := firstText(el)
			if t == nil {
				t = bk.epub.textAfter(el)
			}
			if t != nil {
				a.norm = bk.epub.normIndex(t, 0)
			}
		}
		// keep anchors in order (a chapter can't start before the previous one)
		if n := len(out); n > 0 && (a.page < out[n-1].page || a.frag < out[n-1].frag) {
			continue
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil
	}
	if out[0].page > 0 {
		out = append([]chapterAnchor{{page: 0, frag: 1, norm: 0}}, out...)
	}
	out = append(out, chapterAnchor{page: pages, frag: len(bk.epub.frags) + 1, norm: len(bk.epub.norm)})
	return out
}

// chapterPush: reader page -> KOReader position via the chapter anchors.
func chapterPush(id string, page, pages int) (string, float64, bool) {
	bk, err := loadBook(id)
	if err != nil {
		return "", 0, false
	}
	as := chapterAnchors(bk, pages)
	if as == nil {
		return "", 0, false
	}
	i := 0
	for k := range as[:len(as)-1] {
		if as[k].page <= page {
			i = k
		}
	}
	a, next := as[i], as[i+1]
	frac := 0.0
	if next.page > a.page {
		frac = float64(page-a.page) / float64(next.page-a.page)
	}
	if a.norm >= 0 && next.norm > a.norm+100 {
		idx := a.norm + int(frac*float64(next.norm-a.norm))
		idx = max(0, min(len(bk.epub.normPos)-1, idx))
		p := bk.epub.normPos[idx]
		return bk.epub.xpointerFor(bk.epub.texts[p.node], p.off), float64(idx) / float64(len(bk.epub.norm)), true
	}
	frag := a.frag + int(frac*float64(next.frag-a.frag))
	frag = max(1, min(len(bk.epub.frags), frag))
	return fmt.Sprintf("/body/DocFragment[%d]/body", frag), float64(page) / float64(pages), true
}

var reFrag = regexp.MustCompile(`^/body/DocFragment\[(\d+)\]`)

// chapterPull: KOReader position -> reader page via the chapter anchors.
func chapterPull(id, progress string, pages int) (int, bool) {
	bk, err := loadBook(id)
	if err != nil {
		return 0, false
	}
	as := chapterAnchors(bk, pages)
	m := reFrag.FindStringSubmatch(progress)
	if as == nil || m == nil {
		return 0, false
	}
	frag, _ := strconv.Atoi(m[1])
	idx := -1
	if t, off, err := bk.epub.resolve(progress); err == nil {
		idx = bk.epub.normIndex(t, off)
	}
	i := 0
	for k := range as[:len(as)-1] {
		if as[k].frag < frag || (as[k].frag == frag && (idx < 0 || as[k].norm < 0 || as[k].norm <= idx)) {
			i = k
		}
	}
	a, next := as[i], as[i+1]
	frac := 0.0
	switch {
	case idx >= 0 && a.norm >= 0 && next.norm > a.norm+100:
		frac = float64(idx-a.norm) / float64(next.norm-a.norm)
	case next.frag > a.frag:
		frac = float64(frag-a.frag) / float64(next.frag-a.frag)
	}
	frac = max(0, min(1, frac))
	page := a.page + int(frac*float64(next.page-a.page))
	return max(0, min(pages-1, page)), true
}
