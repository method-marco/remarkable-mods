package main

// Exact positions by text between the built-in reader (pages of its converted PDF)
// and KOReader positions (crengine xpointers in the EPUB). Both sides are reduced
// to the same normalized text (norm.go), so layout differences don't matter.

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"math"
	"os"
	"strings"
)

type bookIndex struct {
	id       string
	epub     *epubBook
	pdfStamp string
	pages    []string // normalized text per reader page
	all      string   // all pages concatenated
	starts   []int    // offset of each page in all
}

// pageAt returns the page containing offset off of bk.all.
func (bk *bookIndex) pageAt(off int) int {
	lo, hi := 0, len(bk.starts)-1
	for lo < hi {
		m := (lo + hi + 1) / 2
		if bk.starts[m] <= off {
			lo = m
		} else {
			hi = m - 1
		}
	}
	return lo
}

var curBook *bookIndex

func loadBook(id string) (*bookIndex, error) {
	pdfPath := xochitlDir + "/" + id + ".pdf"
	st, err := os.Stat(pdfPath)
	if err != nil {
		return nil, err
	}
	stamp := st.ModTime().String() + ":" + itoa(int(st.Size())) // regenerated on font changes
	if curBook != nil && curBook.id == id && curBook.pdfStamp == stamp {
		return curBook, nil
	}
	curBook = nil
	epub, err := openEPUB(xochitlDir + "/" + id + ".epub")
	if err != nil {
		return nil, err
	}
	texts, err := pdfPageTexts(pdfPath)
	if err != nil {
		return nil, err
	}
	pages := make([]string, len(texts))
	starts := make([]int, len(texts))
	var all strings.Builder
	for i, t := range texts {
		pages[i] = norm(t)
		starts[i] = all.Len()
		all.WriteString(pages[i])
	}
	curBook = &bookIndex{id: id, epub: epub, pdfStamp: stamp, pages: pages, all: all.String(), starts: starts}
	return curBook, nil
}

// positionForPage: built-in reader page (0-based) -> KOReader xpointer + percentage.
// Uses the text at the top of the page (skipping the first characters, which may
// be the end of a word hyphenated on the previous page).
func positionForPage(id string, page int) (string, float64, bool) {
	bk, err := loadBook(id)
	if err != nil || page < 0 || page >= len(bk.pages) || len(bk.epub.norm) == 0 {
		return "", 0, false
	}
	pn := bk.pages[page]
	expected := float64(page) / float64(len(bk.pages)) * float64(len(bk.epub.norm))
	for _, size := range []int{40, 24} {
		for skip := 10; skip+size <= len(pn) && skip <= 200; skip += 30 {
			needle := pn[skip : skip+size]
			best := nearestIndex(bk.epub.norm, needle, expected, size == 40)
			if best < 0 {
				continue
			}
			idx := best - skip
			if idx < 0 {
				idx = 0
			}
			p := bk.epub.normPos[idx]
			xp := bk.epub.xpointerFor(bk.epub.texts[p.node], p.off)
			return xp, float64(idx) / float64(len(bk.epub.norm)), true
		}
	}
	return "", 0, false
}

// nearestIndex finds the occurrence of needle closest to expected. Short needles
// are only trusted near the expected place.
func nearestIndex(hay, needle string, expected float64, anywhere bool) int {
	best, bestD := -1, math.MaxFloat64
	for from := 0; ; {
		i := strings.Index(hay[from:], needle)
		if i < 0 {
			break
		}
		i += from
		if d := math.Abs(float64(i) - expected); d < bestD {
			best, bestD = i, d
		}
		from = i + 1
	}
	if best >= 0 && !anywhere && bestD > 0.05*float64(len(hay)) {
		return -1
	}
	return best
}

// pageForRemote: server position -> built-in reader page (0-based).
// progress is a KOReader xpointer if the position comes from KOReader; other apps
// (e.g. Readest) only give a usable percentage.
func pageForRemote(id, progress string, pct float64) (int, bool) {
	bk, err := loadBook(id)
	if err != nil || len(bk.pages) == 0 || len(bk.epub.norm) == 0 {
		return 0, false
	}
	n := len(bk.epub.norm)
	idx := -1
	if t, off, err := bk.epub.resolve(progress); err == nil {
		idx = bk.epub.normIndex(t, off)
	}
	if idx < 0 || idx >= n {
		idx = int(pct * float64(n))
	}
	idx = max(0, min(n-1, idx))
	expected := float64(idx) / float64(n) * float64(len(bk.all)) // same place in the PDF text
	text := bk.epub.norm[idx:]
	// The text at the position, made longer until it's unique in the PDF (repeated
	// phrases), then the occurrence nearest the expected place. Short probes are a
	// fallback for text the PDF orders differently (tables) and must be close by.
	found := -1
	for _, size := range []int{40, 80, 160, 320} {
		if size > len(text) {
			break
		}
		occ := allIndexes(bk.all, text[:size])
		if len(occ) == 0 {
			break
		}
		found = nearest(occ, expected)
		if len(occ) == 1 {
			break
		}
	}
	if found < 0 {
		for _, size := range []int{24, 16} {
			if size > len(text) {
				continue
			}
			if occ := allIndexes(bk.all, text[:size]); len(occ) > 0 {
				f := nearest(occ, expected)
				if math.Abs(float64(f)-expected) < 0.05*float64(len(bk.all)) {
					found = f
					break
				}
			}
		}
	}
	if found < 0 {
		// e.g. the end of a table cell, whose following text the PDF puts elsewhere:
		// use the text just before the position, and the place right after it
		for _, size := range []int{40, 24} {
			if idx < size {
				continue
			}
			if occ := allIndexes(bk.all, bk.epub.norm[idx-size:idx]); len(occ) > 0 {
				f := nearest(occ, expected-float64(size)) + size
				if math.Abs(float64(f)-expected) < 0.05*float64(len(bk.all)) {
					return bk.pageAt(min(f, len(bk.all)-1)), true
				}
			}
		}
		return 0, false
	}
	p := bk.pageAt(found)
	// The reader's pages overlap: the (cut off) last line of a page is repeated at
	// the top of the next one. KOReader's position is the top of its screen, so if
	// the next page shows this text nearer its top, that's the page.
	probe := text[:min(20, len(text))]
	if p+1 < len(bk.pages) && len(bk.pages[p]) > 0 && len(bk.pages[p+1]) > 0 {
		rel := float64(found-bk.starts[p]) / float64(len(bk.pages[p]))
		if s2 := strings.Index(bk.pages[p+1], probe); s2 >= 0 && float64(s2)/float64(len(bk.pages[p+1])) < rel {
			p++
		}
	}
	return p, true
}

func allIndexes(hay, needle string) []int {
	var out []int
	for from := 0; from <= len(hay)-len(needle); {
		i := strings.Index(hay[from:], needle)
		if i < 0 {
			break
		}
		out = append(out, from+i)
		from += i + 1
	}
	return out
}

func nearest(occ []int, expected float64) int {
	best, bestD := occ[0], math.MaxFloat64
	for _, o := range occ {
		if d := math.Abs(float64(o) - expected); d < bestD {
			best, bestD = o, d
		}
	}
	return best
}

// partialMD5: KOReader's "binary" document digest (util.partialMD5): MD5 over
// 1 KiB samples at offsets 0 and 1024 << 2i (i = 0..10).
func partialMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	buf := make([]byte, 1024)
	for i := -1; i <= 10; i++ {
		var off int64
		if i >= 0 {
			off = int64(1024) << (2 * i)
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			break
		}
		n, _ := io.ReadFull(f, buf)
		if n == 0 {
			break
		}
		h.Write(buf[:n])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func itoa(i int) string {
	var b [20]byte
	pos := len(b)
	neg := i < 0
	if neg {
		i = -i
	}
	for {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
		if i == 0 {
			break
		}
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
