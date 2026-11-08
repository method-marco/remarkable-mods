package main

// Minimal PDF reader (Go standard library only): just enough to find the image
// objects of a PDF and decode the largest one. Images imported into the reMarkable
// library are stored as PDFs with the photo embedded as an image object.

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"regexp"
	"strconv"
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
	// robust; later definitions (incremental updates) win. Image objects are never
	// inside compressed object streams.
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

func (f *pdfFile) num(v any) int {
	n, _ := f.resolve(v).(float64)
	return int(n)
}

// ---- images -------------------------------------------------------------------

// largestImage decodes the biggest image object in the PDF.
func largestImage(path string) (image.Image, int, error) {
	f, err := openPDF(path)
	if err != nil {
		return nil, 1, err
	}
	var best *pdfStream
	bestArea := 0
	for n := range f.offsets {
		s, ok := f.get(n).(*pdfStream)
		if !ok || f.resolve(s.dict["Subtype"]) != pdfName("Image") {
			continue
		}
		if a := f.num(s.dict["Width"]) * f.num(s.dict["Height"]); a > bestArea {
			best, bestArea = s, a
		}
	}
	if best == nil {
		return nil, 1, errors.New("no image in this PDF")
	}
	img, err := f.decodeImage(best)
	if err != nil {
		return nil, 1, err
	}
	orientation := 1
	if names, _ := f.filters(best); len(names) == 1 && names[0] == "DCTDecode" {
		orientation = exifOrientation(best.data)
	}
	if m, ok := f.resolve(best.dict["SMask"]).(*pdfStream); ok {
		if mask, err := f.decodeImage(m); err == nil {
			img = onWhite(img, mask)
		}
	}
	return img, orientation, nil
}

func (f *pdfFile) filters(s *pdfStream) ([]pdfName, []map[string]any) {
	var names []pdfName
	var parms []map[string]any
	switch v := f.resolve(s.dict["Filter"]).(type) {
	case pdfName:
		names = []pdfName{v}
	case []any:
		for _, x := range v {
			if n, ok := f.resolve(x).(pdfName); ok {
				names = append(names, n)
			}
		}
	}
	switch v := f.resolve(s.dict["DecodeParms"]).(type) {
	case map[string]any:
		parms = []map[string]any{v}
	case []any:
		for _, x := range v {
			d, _ := f.resolve(x).(map[string]any)
			parms = append(parms, d)
		}
	}
	for len(parms) < len(names) {
		parms = append(parms, nil)
	}
	return names, parms
}

func (f *pdfFile) decodeImage(s *pdfStream) (image.Image, error) {
	names, parms := f.filters(s)
	data := s.data
	for i, name := range names {
		switch name {
		case "FlateDecode":
			r, err := zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			out, err := io.ReadAll(r)
			if err != nil && len(out) == 0 {
				return nil, err
			}
			data = out
			if p := parms[i]; p != nil && f.num(p["Predictor"]) >= 10 {
				colors := max(1, f.num(p["Colors"]))
				bpc := f.num(p["BitsPerComponent"])
				if bpc == 0 {
					bpc = 8
				}
				data, err = pngUnpredict(data, (f.num(p["Columns"])*colors*bpc+7)/8, (colors*bpc+7)/8)
				if err != nil {
					return nil, err
				}
			}
		case "DCTDecode":
			if i != len(names)-1 {
				return nil, errors.New("unsupported filter chain")
			}
			img, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			if c, ok := img.(*image.CMYK); ok && f.isAdobeInvertedCMYK(s, data) {
				for i := range c.Pix {
					c.Pix[i] = 255 - c.Pix[i]
				}
			}
			return img, nil
		default:
			return nil, fmt.Errorf("unsupported image format %s", name)
		}
	}
	return f.rawImage(s, data)
}

// isAdobeInvertedCMYK: Photoshop writes CMYK JPEGs inverted; Go's decoder already
// handles the Adobe marker, a /Decode [1 0 ...] array means inverted again.
func (f *pdfFile) isAdobeInvertedCMYK(s *pdfStream, data []byte) bool {
	d, _ := f.resolve(s.dict["Decode"]).([]any)
	return len(d) >= 2 && f.num(d[0]) == 1 && f.num(d[1]) == 0
}

// rawImage builds an image from decoded samples (8 bits per component, or 1 bit gray).
func (f *pdfFile) rawImage(s *pdfStream, data []byte) (image.Image, error) {
	w, h := f.num(s.dict["Width"]), f.num(s.dict["Height"])
	bpc := f.num(s.dict["BitsPerComponent"])
	if bpc == 0 {
		bpc = 8
	}
	if w <= 0 || h <= 0 {
		return nil, errors.New("bad image size")
	}
	cs := f.resolve(s.dict["ColorSpace"])
	var palette []color.Color
	baseComps := func(cs any) int {
		switch v := f.resolve(cs).(type) {
		case pdfName:
			switch v {
			case "DeviceGray", "CalGray", "G":
				return 1
			case "DeviceCMYK", "CMYK":
				return 4
			}
			return 3
		case []any:
			if len(v) >= 2 && f.resolve(v[0]) == pdfName("ICCBased") {
				if st, ok := f.resolve(v[1]).(*pdfStream); ok {
					if n := f.num(st.dict["N"]); n > 0 {
						return n
					}
				}
			}
			if len(v) >= 1 && f.resolve(v[0]) == pdfName("CalGray") {
				return 1
			}
		}
		return 3
	}
	comps := 1
	if s.dict["ImageMask"] == true || s.dict["ColorSpace"] == nil {
		comps = 1
	} else if a, ok := cs.([]any); ok && len(a) == 4 && f.resolve(a[0]) == pdfName("Indexed") {
		// [/Indexed base hival lookup]
		bc := baseComps(a[1])
		var lut []byte
		switch l := f.resolve(a[3]).(type) {
		case string:
			lut = []byte(l)
		case *pdfStream:
			lut, _ = f.decodeStream(l)
		}
		for i := 0; i+bc <= len(lut); i += bc {
			palette = append(palette, compColor(lut[i:i+bc]))
		}
	} else {
		comps = baseComps(cs)
	}
	stride := (w*comps*bpc + 7) / 8
	if len(data) < stride*h {
		return nil, errors.New("image data too short")
	}
	sample := func(row []byte, i int) byte {
		switch bpc {
		case 8:
			return row[i]
		case 1, 2, 4:
			bit := i * bpc
			v := (row[bit/8] >> (8 - bpc - bit%8)) & (1<<bpc - 1)
			if palette != nil {
				return v
			}
			return v * byte(255/(1<<bpc-1))
		case 16:
			return row[2*i]
		}
		return 0
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	px := make([]byte, comps)
	for y := 0; y < h; y++ {
		row := data[y*stride : (y+1)*stride]
		for x := 0; x < w; x++ {
			for c := 0; c < comps; c++ {
				px[c] = sample(row, x*comps+c)
			}
			var col color.Color
			if palette != nil {
				if int(px[0]) < len(palette) {
					col = palette[px[0]]
				} else {
					col = color.White
				}
			} else {
				col = compColor(px)
			}
			img.Set(x, y, col)
		}
	}
	return img, nil
}

func (f *pdfFile) decodeStream(s *pdfStream) ([]byte, error) {
	names, _ := f.filters(s)
	if len(names) == 0 {
		return s.data, nil
	}
	if len(names) == 1 && names[0] == "FlateDecode" {
		r, err := zlib.NewReader(bytes.NewReader(s.data))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(r)
	}
	return nil, errors.New("unsupported stream filter")
}

func compColor(p []byte) color.Color {
	switch len(p) {
	case 1:
		return color.Gray{p[0]}
	case 4:
		return color.CMYK{p[0], p[1], p[2], p[3]}
	default:
		return color.RGBA{p[0], p[1], p[2], 255}
	}
}

// pngUnpredict reverses PNG row predictors (FlateDecode /Predictor >= 10).
func pngUnpredict(data []byte, rowLen, bpp int) ([]byte, error) {
	if rowLen <= 0 {
		return nil, errors.New("bad predictor columns")
	}
	var out []byte
	prev := make([]byte, rowLen)
	for len(data) >= rowLen+1 {
		ft, row := data[0], append([]byte(nil), data[1:rowLen+1]...)
		data = data[rowLen+1:]
		for i := range row {
			var a, b, c byte
			if i >= bpp {
				a, c = row[i-bpp], prev[i-bpp]
			}
			b = prev[i]
			switch ft {
			case 1:
				row[i] += a
			case 2:
				row[i] += b
			case 3:
				row[i] += byte((int(a) + int(b)) / 2)
			case 4:
				p := int(a) + int(b) - int(c)
				pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
				if pa <= pb && pa <= pc {
					row[i] += a
				} else if pb <= pc {
					row[i] += b
				} else {
					row[i] += c
				}
			}
		}
		out = append(out, row...)
		prev = row
	}
	return out, nil
}

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

// onWhite puts an image with a soft mask (alpha) on a white background.
func onWhite(img, mask image.Image) image.Image {
	b := img.Bounds()
	mb := mask.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		my := mb.Min.Y + (y-b.Min.Y)*mb.Dy()/b.Dy()
		for x := b.Min.X; x < b.Max.X; x++ {
			mx := mb.Min.X + (x-b.Min.X)*mb.Dx()/b.Dx()
			a := uint32(color.GrayModel.Convert(mask.At(mx, my)).(color.Gray).Y)
			r, g, bl, _ := img.At(x, y).RGBA()
			mix := func(v uint32) uint8 { return uint8((v>>8*a + 255*(255-a)) / 255) }
			out.SetRGBA(x, y, color.RGBA{mix(r), mix(g), mix(bl), 255})
		}
	}
	return out
}
