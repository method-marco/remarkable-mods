package main

import (
	"strings"
	"unicode"
)

// Text is compared independent of layout: lower-case letters and digits only (no
// spaces, punctuation, quotes, hyphenation). Ligatures: the reader's PDF may contain
// them as single glyphs or as unknown characters, so the letter pairs that are
// usually ligatures (ffi ffl ff fi fl) are dropped on both sides.

// normalize returns the normalized text and, for every byte of it, the source
// index given in srcs for the rune it came from (srcs[i] belongs to text[i]).
func normalize(text []rune, srcs []int) (string, []int) {
	type nr struct {
		r   rune
		src int
	}
	var kept []nr
	for i, r := range text {
		if r >= 0xFB00 && r <= 0xFB04 || r == 0xFFFD {
			continue
		}
		r = unicode.ToLower(r)
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			src := i
			if srcs != nil {
				src = srcs[i]
			}
			kept = append(kept, nr{r, src})
		}
	}
	// drop ligature letter pairs
	var out []nr
	for i := 0; i < len(kept); {
		if kept[i].r == 'f' && i+1 < len(kept) {
			n := kept[i+1].r
			if n == 'f' {
				if i+2 < len(kept) && (kept[i+2].r == 'i' || kept[i+2].r == 'l') {
					i += 3
				} else {
					i += 2
				}
				continue
			}
			if n == 'i' || n == 'l' {
				i += 2
				continue
			}
		}
		out = append(out, kept[i])
		i++
	}
	var b strings.Builder
	var mapping []int
	for _, x := range out {
		n := b.Len()
		b.WriteRune(x.r)
		for k := n; k < b.Len(); k++ {
			mapping = append(mapping, x.src)
		}
	}
	return b.String(), mapping
}

func norm(s string) string {
	n, _ := normalize([]rune(s), nil)
	return n
}
