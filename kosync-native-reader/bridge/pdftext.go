package main

// Minimal PDF text extraction (Go standard library only) for the PDFs the
// reMarkable's built-in reader renders EPUBs to (Qt's PDF writer): classic xref,
// Type0/CID fonts with ToUnicode CMaps, Flate-compressed content streams, text
// shown with hex strings (<0001> Tj) or TJ arrays. We only need the characters in
// reading order per page; spacing and layout are irrelevant (text is normalized).

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"unicode/utf16"
)

type pdfRef struct{ num, gen int }

type pdfStream struct {
	dict map[string]any
	data []byte // raw (still encoded)
}

type pdfFile struct {
	data    []byte
	offsets map[int]int // object number -> byte offset
	cache   map[int]any
}

var objHeader = regexp.MustCompile(`(?m)(\d+)\s+(\d+)\s+obj\b`)

func openPDF(path string) (*pdfFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		return nil, errors.New("not a PDF")
	}
	f := &pdfFile{data: data, offsets: map[int]int{}, cache: map[int]any{}}
	// Scan for object headers instead of trusting the xref table: simple and
	// robust; later definitions (incremental updates) win.
	for _, m := range objHeader.FindAllSubmatchIndex(data, -1) {
		n, _ := strconv.Atoi(string(data[m[2]:m[3]]))
		f.offsets[n] = m[1]
	}
	return f, nil
}

// ---- lexer / parser ---------------------------------------------------------

type pdfLexer struct {
	b   []byte
	pos int
}

func isWS(c byte) bool   { return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f' || c == 0 }
func isDelim(c byte) bool { return bytes.IndexByte([]byte("()<>[]{}/%"), c) >= 0 }

func (l *pdfLexer) skipWS() {
	for l.pos < len(l.b) {
		c := l.b[l.pos]
		if isWS(c) {
			l.pos++
		} else if c == '%' {
			for l.pos < len(l.b) && l.b[l.pos] != '\n' && l.b[l.pos] != '\r' {
				l.pos++
			}
		} else {
			return
		}
	}
}

type pdfName string
type pdfKeyword string

// token returns the next primitive token: number (float64), pdfName, string
// ([]byte), pdfKeyword, or delimiter as pdfKeyword ("[", "]", "<<", ">>").
func (l *pdfLexer) token() (any, error) {
	l.skipWS()
	if l.pos >= len(l.b) {
		return nil, io.EOF
	}
	c := l.b[l.pos]
	switch {
	case c == '/':
		l.pos++
		start := l.pos
		for l.pos < len(l.b) && !isWS(l.b[l.pos]) && !isDelim(l.b[l.pos]) {
			l.pos++
		}
		return pdfName(l.b[start:l.pos]), nil
	case c == '(':
		return l.literalString()
	case c == '<':
		if l.pos+1 < len(l.b) && l.b[l.pos+1] == '<' {
			l.pos += 2
			return pdfKeyword("<<"), nil
		}
		return l.hexString()
	case c == '>':
		if l.pos+1 < len(l.b) && l.b[l.pos+1] == '>' {
			l.pos += 2
			return pdfKeyword(">>"), nil
		}
		l.pos++
		return pdfKeyword(">"), nil
	case c == '[' || c == ']' || c == '{' || c == '}':
		l.pos++
		return pdfKeyword(string(c)), nil
	default:
		start := l.pos
		for l.pos < len(l.b) && !isWS(l.b[l.pos]) && !isDelim(l.b[l.pos]) {
			l.pos++
		}
		if start == l.pos {
			l.pos++
			return pdfKeyword(string(c)), nil
		}
		w := string(l.b[start:l.pos])
		if v, err := strconv.ParseFloat(w, 64); err == nil {
			return v, nil
		}
		return pdfKeyword(w), nil
	}
}

func (l *pdfLexer) literalString() (any, error) {
	l.pos++ // (
	var out []byte
	depth := 1
	for l.pos < len(l.b) {
		c := l.b[l.pos]
		l.pos++
		switch c {
		case '\\':
			if l.pos >= len(l.b) {
				break
			}
			e := l.b[l.pos]
			l.pos++
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\r', '\n':
				// line continuation
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := 0; k < 2 && l.pos < len(l.b) && l.b[l.pos] >= '0' && l.b[l.pos] <= '7'; k++ {
						v = v*8 + int(l.b[l.pos]-'0')
						l.pos++
					}
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
		case '(':
			depth++
			out = append(out, c)
		case ')':
			depth--
			if depth == 0 {
				return out, nil
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out, nil
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func (l *pdfLexer) hexString() (any, error) {
	l.pos++ // <
	var out []byte
	var hi byte
	half := false
	for l.pos < len(l.b) && l.b[l.pos] != '>' {
		if v, ok := hexVal(l.b[l.pos]); ok {
			if half {
				out = append(out, hi<<4|v)
			} else {
				hi = v
			}
			half = !half
		}
		l.pos++
	}
	if half {
		out = append(out, hi<<4)
	}
	l.pos++ // >
	return out, nil
}

// object parses a full object (resolving "n g R" references lazily as pdfRef).
func (l *pdfLexer) object() (any, error) {
	t, err := l.token()
	if err != nil {
		return nil, err
	}
	switch v := t.(type) {
	case pdfKeyword:
		switch v {
		case "<<":
			d := map[string]any{}
			for {
				l.skipWS()
				if l.pos+1 < len(l.b) && l.b[l.pos] == '>' && l.b[l.pos+1] == '>' {
					l.pos += 2
					return d, nil
				}
				k, err := l.token()
				if err != nil {
					return d, err
				}
				name, ok := k.(pdfName)
				if !ok {
					continue
				}
				val, err := l.object()
				if err != nil {
					return d, err
				}
				d[string(name)] = val
			}
		case "[":
			var a []any
			for {
				l.skipWS()
				if l.pos < len(l.b) && l.b[l.pos] == ']' {
					l.pos++
					return a, nil
				}
				val, err := l.object()
				if err != nil {
					return a, err
				}
				a = append(a, val)
			}
		}
		return v, nil
	case float64:
		// number, or "num gen R"
		save := l.pos
		t2, err2 := l.token()
		if g, ok := t2.(float64); ok && err2 == nil {
			t3, err3 := l.token()
			if kw, ok := t3.(pdfKeyword); ok && err3 == nil && kw == "R" {
				return pdfRef{int(v), int(g)}, nil
			}
		}
		l.pos = save
		return v, nil
	}
	return t, nil
}

func (f *pdfFile) get(num int) any {
	if v, ok := f.cache[num]; ok {
		return v
	}
	off, ok := f.offsets[num]
	if !ok {
		return nil
	}
	l := &pdfLexer{b: f.data, pos: off}
	v, err := l.object()
	if err != nil {
		return nil
	}
	if d, ok := v.(map[string]any); ok {
		l.skipWS()
		if bytes.HasPrefix(f.data[l.pos:], []byte("stream")) {
			p := l.pos + len("stream")
			if p < len(f.data) && f.data[p] == '\r' {
				p++
			}
			if p < len(f.data) && f.data[p] == '\n' {
				p++
			}
			length := -1
			if n, ok := f.resolve(d["Length"]).(float64); ok {
				length = int(n)
			}
			end := p + length
			if length < 0 || end > len(f.data) || !bytes.HasPrefix(bytes.TrimLeft(f.data[end:min(end+20, len(f.data))], "\r\n "), []byte("endstream")) {
				if e := bytes.Index(f.data[p:], []byte("endstream")); e >= 0 {
					end = p + e
				} else {
					end = len(f.data)
				}
			}
			v = &pdfStream{dict: d, data: f.data[p:end]}
		}
	}
	f.cache[num] = v
	return v
}

func (f *pdfFile) resolve(v any) any {
	for i := 0; i < 10; i++ {
		r, ok := v.(pdfRef)
		if !ok {
			return v
		}
		v = f.get(r.num)
	}
	return v
}

func (f *pdfFile) dict(v any) map[string]any {
	switch d := f.resolve(v).(type) {
	case map[string]any:
		return d
	case *pdfStream:
		return d.dict
	}
	return nil
}

func (f *pdfFile) streamData(v any) []byte {
	s, ok := f.resolve(v).(*pdfStream)
	if !ok {
		return nil
	}
	filter := f.resolve(s.dict["Filter"])
	if a, ok := filter.([]any); ok && len(a) > 0 {
		filter = f.resolve(a[0])
	}
	if filter == pdfName("FlateDecode") {
		r, err := zlib.NewReader(bytes.NewReader(s.data))
		if err != nil {
			return nil
		}
		out, _ := io.ReadAll(r)
		return out
	}
	if filter == nil {
		return s.data
	}
	return nil // images etc.
}

// ---- pages ------------------------------------------------------------------

func (f *pdfFile) pages() []map[string]any {
	trailerRoot := regexp.MustCompile(`/Root\s+(\d+)\s+(\d+)\s+R`).FindAllSubmatch(f.data, -1)
	var root map[string]any
	if len(trailerRoot) > 0 {
		n, _ := strconv.Atoi(string(trailerRoot[len(trailerRoot)-1][1]))
		root = f.dict(pdfRef{n, 0})
	}
	var out []map[string]any
	var walk func(node any, depth int)
	walk = func(node any, depth int) {
		d := f.dict(node)
		if d == nil || depth > 64 {
			return
		}
		switch f.resolve(d["Type"]) {
		case pdfName("Pages"):
			kids, _ := f.resolve(d["Kids"]).([]any)
			for _, k := range kids {
				walk(k, depth+1)
			}
		case pdfName("Page"):
			out = append(out, d)
		}
	}
	if root != nil {
		walk(root["Pages"], 0)
	}
	return out
}

// ---- fonts / ToUnicode --------------------------------------------------------

type pdfFont struct {
	twoByte bool
	toUni   map[uint32][]rune
}

var (
	reBfchar  = regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`)
	reBfrange = regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`)
	reHex     = regexp.MustCompile(`<([0-9A-Fa-f]*)>|\[|\]`)
)

func hexBytes(s string) []byte {
	l := &pdfLexer{b: []byte("<" + s + ">")}
	v, _ := l.hexString()
	b, _ := v.([]byte)
	return b
}

func codeOf(b []byte) uint32 {
	var c uint32
	for _, x := range b {
		c = c<<8 | uint32(x)
	}
	return c
}

func utf16Runes(b []byte) []rune {
	var u []uint16
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return utf16.Decode(u)
}

func parseCMap(data []byte) map[uint32][]rune {
	m := map[uint32][]rune{}
	for _, blk := range reBfchar.FindAllSubmatch(data, -1) {
		toks := reHex.FindAllSubmatch(blk[1], -1)
		for i := 0; i+1 < len(toks); i += 2 {
			m[codeOf(hexBytes(string(toks[i][1])))] = utf16Runes(hexBytes(string(toks[i+1][1])))
		}
	}
	for _, blk := range reBfrange.FindAllSubmatch(data, -1) {
		toks := reHex.FindAllSubmatch(blk[1], -1)
		for i := 0; i+2 < len(toks); {
			lo := codeOf(hexBytes(string(toks[i][1])))
			hi := codeOf(hexBytes(string(toks[i+1][1])))
			if string(toks[i+2][0]) == "[" {
				j := i + 3
				for c := lo; j < len(toks) && string(toks[j][0]) != "]"; c, j = c+1, j+1 {
					m[c] = utf16Runes(hexBytes(string(toks[j][1])))
				}
				i = j + 1
				continue
			}
			dst := utf16Runes(hexBytes(string(toks[i+2][1])))
			for c := lo; c <= hi && c-lo < 65536; c++ {
				if len(dst) > 0 {
					r := make([]rune, len(dst))
					copy(r, dst)
					r[len(r)-1] += rune(c - lo)
					m[c] = r
				}
			}
			i += 3
		}
	}
	return m
}

func (f *pdfFile) font(v any, cache map[any]*pdfFont) *pdfFont {
	if ft, ok := cache[v]; ok {
		return ft
	}
	d := f.dict(v)
	ft := &pdfFont{}
	if d != nil {
		ft.twoByte = f.resolve(d["Subtype"]) == pdfName("Type0")
		if tu := f.streamData(d["ToUnicode"]); tu != nil {
			ft.toUni = parseCMap(tu)
		}
	}
	cache[v] = ft
	return ft
}

// pageText returns the text of one page (1-based) in content-stream order.
func (f *pdfFile) pageText(page map[string]any, fontCache map[any]*pdfFont) string {
	var content []byte
	switch c := f.resolve(page["Contents"]).(type) {
	case []any:
		for _, part := range c {
			content = append(content, f.streamData(part)...)
			content = append(content, '\n')
		}
	default:
		content = f.streamData(page["Contents"])
	}
	fonts := map[string]any{}
	res := f.dict(page["Resources"])
	if res != nil {
		for k, v := range f.dict(res["Font"]) {
			fonts[k] = v
		}
	}
	var out []rune
	var cur *pdfFont
	var operands []any
	show := func(s []byte) {
		if cur == nil {
			out = append(out, []rune(string(s))...)
			return
		}
		step := 1
		if cur.twoByte {
			step = 2
		}
		for i := 0; i+step <= len(s); i += step {
			code := codeOf(s[i : i+step])
			if r, ok := cur.toUni[code]; ok {
				out = append(out, r...)
			} else {
				out = append(out, '�')
			}
		}
	}
	l := &pdfLexer{b: content}
	for {
		obj, err := l.object()
		if err != nil {
			break
		}
		kw, isKw := obj.(pdfKeyword)
		if !isKw {
			operands = append(operands, obj)
			continue
		}
		switch kw {
		case "Tf":
			if len(operands) >= 2 {
				if name, ok := operands[len(operands)-2].(pdfName); ok {
					if ref, ok := fonts[string(name)]; ok {
						cur = f.font(ref, fontCache)
					}
				}
			}
		case "Tj", "'", "\"":
			if len(operands) > 0 {
				if s, ok := operands[len(operands)-1].([]byte); ok {
					show(s)
				}
			}
		case "TJ":
			if len(operands) > 0 {
				if a, ok := operands[len(operands)-1].([]any); ok {
					for _, e := range a {
						if s, ok := e.([]byte); ok {
							show(s)
						}
					}
				}
			}
		case "ET", "T*":
			// Qt positions every glyph with Td, so only text-block ends separate words
			// (enough: text is compared without spaces anyway)
			out = append(out, ' ')
		case "BI":
			// inline image: skip to EI
			if e := bytes.Index(content[l.pos:], []byte("EI")); e >= 0 {
				l.pos += e + 2
			}
		}
		operands = operands[:0]
	}
	return string(out)
}

// pdfPageTexts extracts all pages' text.
func pdfPageTexts(path string) ([]string, error) {
	f, err := openPDF(path)
	if err != nil {
		return nil, err
	}
	pages := f.pages()
	if len(pages) == 0 {
		return nil, fmt.Errorf("no pages found in %s", path)
	}
	fontCache := map[any]*pdfFont{}
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = f.pageText(p, fontCache)
	}
	return out, nil
}
