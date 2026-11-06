package main

// EPUB reading and KOReader (crengine) compatible positions ("xpointers"),
// Go standard library only.
//
// crengine's DOM for an EPUB: /body/DocFragment[i]/body/... where DocFragment i is
// the i-th spine item and the inner body is that file's <body>. Path steps are
// element names, with [n] (1-based, among siblings of the same name) only when
// there is more than one such sibling; text nodes are text() / text()[n]; the
// final .k is the character offset in that text node.

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"
)

type domNode struct {
	name     string // element name (lower case); "" for text nodes
	id       string // id attribute (link targets of the table of contents)
	text     []rune // text nodes only
	children []*domNode
	parent   *domNode
}

func (n *domNode) isText() bool { return n.name == "" }

type epubBook struct {
	frags []*domNode // body element of each spine item (nil if missing)
	toc   []tocEntry // the EPUB's own table of contents, in reading order
	// whole-book text index
	texts   []*domNode // all text nodes in reading order
	norm    string     // normalized text of the whole book
	normPos []textPos  // per byte of norm: where it comes from
}

type textPos struct {
	node int // index into texts
	off  int // rune offset in that text node
}

// blockish elements: whitespace-only text directly inside them is not content
// (crengine drops it while building the DOM).
var blockElems = map[string]bool{
	"html": true, "body": true, "div": true, "section": true, "article": true, "aside": true,
	"header": true, "footer": true, "nav": true, "main": true, "figure": true, "blockquote": true,
	"ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true, "table": true,
	"thead": true, "tbody": true, "tfoot": true, "tr": true, "td": true, "th": true,
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"hgroup": true, "figcaption": true, "pre": false, "address": true, "center": true,
	"caption": true, "colgroup": true, "col": true, "head": true,
}

func isSpaceOnly(r []rune) bool {
	for _, c := range r {
		if !unicode.IsSpace(c) || c == 0xA0 {
			return false
		}
	}
	return true
}

func parseXHTML(data []byte) (*domNode, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	root := &domNode{name: "#document"}
	cur := root
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return root, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &domNode{name: strings.ToLower(t.Name.Local), parent: cur}
			for _, a := range t.Attr {
				if a.Name.Local == "id" {
					n.id = a.Value
				}
			}
			cur.children = append(cur.children, n)
			cur = n
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			for p := cur; p != nil && p != root; p = p.parent {
				if p.name == name {
					cur = p.parent
					break
				}
			}
		case xml.CharData:
			txt := []rune(string(t))
			if k := len(cur.children); k > 0 && cur.children[k-1].isText() {
				cur.children[k-1].text = append(cur.children[k-1].text, txt...)
			} else {
				cur.children = append(cur.children, &domNode{text: txt, parent: cur})
			}
		}
	}
	cleanDOM(root)
	return root, nil
}

// cleanDOM applies crengine's text normalization: whitespace-only text inside a
// block element is dropped when it only separates block-level content (or sits at
// the edge of the block); between inline elements it is a real space and is kept.
// In remaining text, runs of whitespace (incl. new lines) become a single space.
// (Checked against crengine on real books, see the verify mode.)
func cleanDOM(n *domNode) {
	isBlock := func(c *domNode) bool { return c == nil || (!c.isText() && blockElems[c.name]) }
	var kept []*domNode
	for i, c := range n.children {
		if c.isText() {
			if isSpaceOnly(c.text) && blockElems[n.name] {
				var prev, next *domNode
				if i > 0 {
					prev = n.children[i-1]
				}
				if i+1 < len(n.children) {
					next = n.children[i+1]
				}
				if isBlock(prev) || isBlock(next) {
					continue
				}
			}
			c.text = collapseSpace(c.text)
			if len(c.text) == 0 {
				continue
			}
		} else {
			cleanDOM(c)
		}
		kept = append(kept, c)
	}
	n.children = kept
}

func collapseSpace(r []rune) []rune {
	out := make([]rune, 0, len(r))
	prevSpace := false
	for _, c := range r {
		if unicode.IsSpace(c) && c != 0xA0 {
			if prevSpace {
				continue
			}
			prevSpace = true
			out = append(out, ' ')
			continue
		}
		prevSpace = false
		out = append(out, c)
	}
	return out
}

func findElem(n *domNode, name string) *domNode {
	if n.name == name {
		return n
	}
	for _, c := range n.children {
		if !c.isText() {
			if f := findElem(c, name); f != nil {
				return f
			}
		}
	}
	return nil
}

type opfPackage struct {
	Manifest []struct {
		ID         string `xml:"id,attr"`
		Href       string `xml:"href,attr"`
		MediaType  string `xml:"media-type,attr"`
		Properties string `xml:"properties,attr"`
	} `xml:"manifest>item"`
	Spine struct {
		Toc      string `xml:"toc,attr"`
		Itemrefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

// tocEntry: one chapter of the EPUB's table of contents.
type tocEntry struct {
	title  string
	frag   int    // spine item (1-based, = DocFragment index); 0 if unknown
	anchor string // element id within that file, "" for the file start
}

func openEPUB(file string) (*epubBook, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	read := func(name string) ([]byte, error) {
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("missing %s", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	container, err := read("META-INF/container.xml")
	if err != nil {
		return nil, err
	}
	var c struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(container, &c); err != nil || len(c.Rootfiles) == 0 {
		return nil, errors.New("no rootfile in container.xml")
	}
	opfPath := c.Rootfiles[0].FullPath
	opfData, err := read(opfPath)
	if err != nil {
		return nil, err
	}
	var pkg opfPackage
	dec := xml.NewDecoder(bytes.NewReader(opfData))
	dec.Strict = false
	if err := dec.Decode(&pkg); err != nil {
		return nil, fmt.Errorf("opf: %w", err)
	}
	hrefs := map[string]string{}
	base := path.Dir(opfPath)
	for _, it := range pkg.Manifest {
		h, _ := url.PathUnescape(it.Href)
		if base != "." {
			h = path.Join(base, h)
		}
		hrefs[it.ID] = h
	}
	b := &epubBook{}
	var srcRunes []rune
	var srcPos []int
	fragOf := map[string]int{}
	for i, it := range pkg.Spine.Itemrefs {
		fragOf[hrefs[it.IDRef]] = i + 1
	}
	for _, it := range pkg.Spine.Itemrefs {
		var body *domNode
		if data, err := read(hrefs[it.IDRef]); err == nil {
			if doc, err := parseXHTML(data); err == nil || doc != nil {
				body = findElem(doc, "body")
			}
		}
		b.frags = append(b.frags, body)
		if body == nil {
			continue
		}
		var walk func(n *domNode)
		walk = func(n *domNode) {
			for _, ch := range n.children {
				if ch.isText() {
					idx := len(b.texts)
					b.texts = append(b.texts, ch)
					for k, r := range ch.text {
						srcRunes = append(srcRunes, r)
						srcPos = append(srcPos, idx<<20|k)
					}
					// block boundary: keep words apart (normalization drops it anyway)
				} else {
					walk(ch)
				}
			}
		}
		walk(body)
	}
	b.toc = readTOC(pkg, hrefs, fragOf, read)
	normText, mapping := normalize(srcRunes, srcPos)
	b.norm = normText
	b.normPos = make([]textPos, len(mapping))
	for i, m := range mapping {
		b.normPos[i] = textPos{node: m >> 20, off: m & (1<<20 - 1)}
	}
	return b, nil
}

// ---- xpointers ----------------------------------------------------------------

// xpointerFor builds the crengine position of rune offset off in text node t.
func (b *epubBook) xpointerFor(t *domNode, off int) string {
	var steps []string
	// text step
	same, idx := 0, 0
	for _, s := range t.parent.children {
		if s.isText() {
			same++
			if s == t {
				idx = same
			}
		}
	}
	if same > 1 {
		steps = append(steps, fmt.Sprintf("text()[%d].%d", idx, off))
	} else {
		steps = append(steps, fmt.Sprintf("text().%d", off))
	}
	n := t.parent
	frag := 0
	for n != nil && n.name != "body" {
		same, idx = 0, 0
		for _, s := range n.parent.children {
			if s.name == n.name {
				same++
				if s == n {
					idx = same
				}
			}
		}
		if same > 1 {
			steps = append(steps, fmt.Sprintf("%s[%d]", n.name, idx))
		} else {
			steps = append(steps, n.name)
		}
		n = n.parent
	}
	for i, f := range b.frags {
		if f == n {
			frag = i + 1
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "/body/DocFragment[%d]/body", frag)
	for i := len(steps) - 1; i >= 0; i-- {
		sb.WriteString("/")
		sb.WriteString(steps[i])
	}
	return sb.String()
}

// resolve finds the text node and rune offset for a crengine xpointer. For an
// element xpointer (no text step) it returns the element's first text node.
func (b *epubBook) resolve(xp string) (*domNode, int, error) {
	if !strings.HasPrefix(xp, "/body/DocFragment[") {
		return nil, 0, errors.New("not a KOReader position")
	}
	rest := strings.TrimPrefix(xp, "/body/DocFragment[")
	end := strings.Index(rest, "]")
	if end < 0 {
		return nil, 0, errors.New("bad xpointer")
	}
	fi, err := strconv.Atoi(rest[:end])
	if err != nil || fi < 1 || fi > len(b.frags) || b.frags[fi-1] == nil {
		return nil, 0, errors.New("no such DocFragment")
	}
	rest = strings.TrimPrefix(rest[end+1:], "/body")
	n := b.frags[fi-1]
	off := 0
	for _, step := range strings.Split(strings.Trim(rest, "/"), "/") {
		if step == "" {
			continue
		}
		name, idx := step, 1
		if dot := strings.LastIndex(step, "."); dot >= 0 && !strings.Contains(step[dot:], "]") {
			off, _ = strconv.Atoi(step[dot+1:])
			name = step[:dot]
		}
		if lb := strings.Index(name, "["); lb >= 0 && strings.HasSuffix(name, "]") {
			idx, _ = strconv.Atoi(name[lb+1 : len(name)-1])
			name = name[:lb]
		}
		var next *domNode
		count := 0
		for _, c := range n.children {
			if (name == "text()" && c.isText()) || (name != "text()" && c.name == name) {
				count++
				if count == idx {
					next = c
					break
				}
			}
		}
		if next == nil {
			var kids []string
			for _, c := range n.children {
				if c.isText() {
					kids = append(kids, fmt.Sprintf("text(%q)", trunc(string(c.text), 25)))
				} else {
					kids = append(kids, c.name)
				}
			}
			return nil, 0, fmt.Errorf("step %q not found in %s: %s", step, n.name, strings.Join(kids, ", "))
		}
		n = next
	}
	if !n.isText() {
		// element position (e.g. an image): the first text at or after it
		t := firstText(n)
		if t == nil {
			t = b.textAfter(n)
		}
		if t == nil {
			return nil, 0, errors.New("no text at position")
		}
		return t, 0, nil
	}
	if off > len(n.text) {
		off = len(n.text)
	}
	return n, off, nil
}

// textAfter returns the first text node following element n in reading order.
func (b *epubBook) textAfter(n *domNode) *domNode {
	for p := n; p != nil && p.parent != nil; p = p.parent {
		seen := false
		for _, c := range p.parent.children {
			if c == p {
				seen = true
				continue
			}
			if !seen {
				continue
			}
			if c.isText() {
				return c
			}
			if t := firstText(c); t != nil {
				return t
			}
		}
	}
	return nil
}

func firstText(n *domNode) *domNode {
	for _, c := range n.children {
		if c.isText() {
			return c
		}
		if t := firstText(c); t != nil {
			return t
		}
	}
	return nil
}

// normIndex returns the index in b.norm of the first normalized character at or
// after (text node, offset).
func (b *epubBook) normIndex(t *domNode, off int) int {
	ti := -1
	for i, x := range b.texts {
		if x == t {
			ti = i
			break
		}
	}
	if ti < 0 {
		return -1
	}
	lo, hi := 0, len(b.normPos)
	for lo < hi { // first position >= (ti, off)
		m := (lo + hi) / 2
		p := b.normPos[m]
		if p.node < ti || (p.node == ti && p.off < off) {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}

// ---- table of contents ------------------------------------------------------------

// readTOC reads the EPUB 3 nav document (or the EPUB 2 NCX) into chapters in
// reading order, each pointing at a spine item and optional anchor.
func readTOC(pkg opfPackage, hrefs map[string]string, fragOf map[string]int, read func(string) ([]byte, error)) []tocEntry {
	target := func(base, href string) (int, string) {
		h, frag, _ := strings.Cut(href, "#")
		h, _ = url.PathUnescape(h)
		if base != "." && base != "" {
			h = path.Join(base, h)
		}
		return fragOf[path.Clean(h)], frag
	}
	navPath, ncxPath := "", ""
	for _, it := range pkg.Manifest {
		if strings.Contains(" "+it.Properties+" ", " nav ") {
			navPath = hrefs[it.ID]
		}
		if it.MediaType == "application/x-dtbncx+xml" || it.ID == pkg.Spine.Toc {
			ncxPath = hrefs[it.ID]
		}
	}
	var out []tocEntry
	if navPath != "" {
		if data, err := read(navPath); err == nil {
			out = navEntries(data, path.Dir(navPath), target)
		}
	}
	if len(out) == 0 && ncxPath != "" {
		if data, err := read(ncxPath); err == nil {
			out = ncxEntries(data, path.Dir(ncxPath), target)
		}
	}
	return out
}

// navEntries: <a href> links inside the <nav epub:type="toc"> (or the first <nav>).
func navEntries(data []byte, base string, target func(string, string) (int, string)) []tocEntry {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict, dec.AutoClose, dec.Entity = false, xml.HTMLAutoClose, xml.HTMLEntity
	var out []tocEntry
	navDepth, inToc, sawToc := 0, false, false
	var cur *tocEntry
	var title strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if name == "nav" {
				navDepth++
				isToc := false
				for _, a := range t.Attr {
					if a.Name.Local == "type" && strings.Contains(a.Value, "toc") {
						isToc = true
					}
				}
				if navDepth == 1 && (isToc || !sawToc) {
					inToc, sawToc = true, sawToc || isToc
					if isToc {
						out = out[:0] // prefer the real toc nav over an earlier one
					}
				}
			}
			if name == "a" && inToc {
				for _, a := range t.Attr {
					if a.Name.Local == "href" {
						f, anchor := target(base, a.Value)
						cur = &tocEntry{frag: f, anchor: anchor}
						title.Reset()
					}
				}
			}
		case xml.CharData:
			if cur != nil {
				title.Write(t)
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			if name == "a" && cur != nil {
				cur.title = strings.Join(strings.Fields(title.String()), " ")
				out = append(out, *cur)
				cur = nil
			}
			if name == "nav" {
				navDepth--
				if navDepth == 0 {
					inToc = false
				}
			}
		}
	}
	return out
}

// ncxEntries: navPoints in document order (navLabel/text + content/@src).
func ncxEntries(data []byte, base string, target func(string, string) (int, string)) []tocEntry {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var out []tocEntry
	var stack []int // indexes into out of the open navPoints
	inText := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch strings.ToLower(t.Name.Local) {
			case "navpoint":
				out = append(out, tocEntry{})
				stack = append(stack, len(out)-1)
			case "text":
				inText = len(stack) > 0
			case "content":
				if len(stack) > 0 {
					for _, a := range t.Attr {
						if a.Name.Local == "src" {
							e := &out[stack[len(stack)-1]]
							e.frag, e.anchor = target(base, a.Value)
						}
					}
				}
			}
		case xml.CharData:
			if inText && len(stack) > 0 {
				if e := &out[stack[len(stack)-1]]; e.title == "" {
					e.title = strings.Join(strings.Fields(string(t)), " ")
				}
			}
		case xml.EndElement:
			switch strings.ToLower(t.Name.Local) {
			case "text":
				inText = false
			case "navpoint":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
		}
	}
	return out
}

// findID returns the element with the given id below n.
func findID(n *domNode, id string) *domNode {
	if n == nil {
		return nil
	}
	if n.id == id {
		return n
	}
	for _, c := range n.children {
		if !c.isText() {
			if f := findID(c, id); f != nil {
				return f
			}
		}
	}
	return nil
}
