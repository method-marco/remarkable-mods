// kosync-bridge: KOReader progress sync for the reMarkable's built-in EPUB reader.
// Standalone, Go standard library only. See ../README.md.
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const usage = `kosync-bridge - KOReader progress sync for the reMarkable's built-in EPUB reader

  kosync-bridge                      run the sync service (systemd: kosync-bridge.service)
  kosync-bridge --once               one push + pull round, then exit
  kosync-bridge --check              list the EPUBs with their sync digest and position
  kosync-bridge --map <id> page <n>  reader page (0-based) -> KOReader position
  kosync-bridge --map <id> xp <xpointer> [percentage]
                                     KOReader position -> reader page
  kosync-bridge verify <epub> <pdf> <groundtruth.json>   (development)
`

func main() {
	args := os.Args[1:]
	switch {
	case len(args) == 0:
		runDaemon(false)
	case args[0] == "--once":
		runDaemon(true)
	case args[0] == "--check":
		runCheck()
	case args[0] == "--map" && len(args) >= 4:
		runMap(args[1], args[2], args[3], args[4:])
	case args[0] == "--toc" && len(args) == 2:
		runTOC(args[1])
	case args[0] == "--map-all" && len(args) >= 2:
		step := 25
		if len(args) >= 3 {
			step, _ = strconv.Atoi(args[2])
		}
		runMapAll(args[1], step)
	case args[0] == "verify" && len(args) == 4:
		runVerify(args[1], args[2], args[3])
	default:
		fmt.Print(usage)
		if args[0] != "-h" && args[0] != "--help" {
			os.Exit(2)
		}
	}
}

func runCheck() {
	for _, id := range listEPUBs() {
		info, ok := readBookInfo(id)
		if !ok {
			continue
		}
		digest, _ := partialMD5(xochitlDir + "/" + id + ".epub")
		t0 := time.Now()
		xp, pct, exact := pushPosition(id, info.page, info.pages)
		fmt.Printf("%s  %s\n  digest %s  page %d/%d -> %s (%.1f%%, %s, %.1fs)\n",
			id, info.name, digest, info.page+1, info.pages, xp, pct*100, exact, time.Since(t0).Seconds())
	}
}

// runMapAll checks a whole book in one go (book loaded once): for every n-th page
// it prints "page <TAB> xpointer <TAB> text at that position <TAB> round-trip page".
func runMapAll(id string, samples int) {
	info, ok := readBookInfo(id)
	if !ok {
		fmt.Println("unknown book", id)
		os.Exit(1)
	}
	bk, err := loadBook(id)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	step := max(1, info.pages/max(1, samples))
	okTrips, total := 0, 0
	for page := 0; page < info.pages; page += step {
		xp, _, exact := positionForPage(id, page)
		if !exact {
			fmt.Printf("%d\t-\t-\t-\n", page)
			continue
		}
		text := ""
		if t, off, err := bk.epub.resolve(xp); err == nil {
			text = trunc(string(t.text[off:]), 60)
		}
		back, _ := pageForRemote(id, xp, 0)
		total++
		if back == page {
			okTrips++
		}
		fmt.Printf("%d\t%s\t%s\t%d\n", page, xp, text, back)
	}
	fmt.Fprintf(os.Stderr, "%s: %d positions, %d exact round trips\n", info.name, total, okTrips)
}

// runTOC shows the EPUB's table of contents and the chapter anchors (if the
// reader's table of contents for the current layout is known).
func runTOC(id string) {
	info, ok := readBookInfo(id)
	bk, err := loadBook(id)
	if !ok || err != nil {
		fmt.Println("unknown book", id, err)
		os.Exit(1)
	}
	fmt.Printf("%s: %d chapter files, %d table of contents entries\n", info.name, len(bk.epub.frags), len(bk.epub.toc))
	for i, e := range bk.epub.toc {
		if i < 8 || i == len(bk.epub.toc)-1 {
			fmt.Printf("  %-50s DocFragment[%d] #%s\n", trunc(e.title, 50), e.frag, e.anchor)
		}
	}
	as := chapterAnchors(bk, info.pages)
	fmt.Printf("chapter anchors for the current layout: %d\n", len(as))
	for i, a := range as {
		if i < 8 || i == len(as)-1 {
			fmt.Printf("  page %4d  DocFragment[%d]  text index %d\n", a.page+1, a.frag, a.norm)
		}
	}
}

func runMap(id, mode, value string, rest []string) {
	info, ok := readBookInfo(id)
	if !ok {
		fmt.Println("unknown book", id)
		os.Exit(1)
	}
	t0 := time.Now()
	if mode == "page" {
		page, _ := strconv.Atoi(value)
		xp, pct, exact := positionForPage(id, page)
		fmt.Printf("page %d/%d -> %s (%.1f%%, exact=%v)\n", page+1, info.pages, xp, pct*100, exact)
		if bk, err := loadBook(id); err == nil && exact {
			if t, off, err := bk.epub.resolve(xp); err == nil {
				fmt.Printf("  text there: %s\n", trunc(string(t.text[off:]), 100))
			}
		}
	} else {
		pct := 0.0
		if len(rest) > 0 {
			pct, _ = strconv.ParseFloat(rest[0], 64)
		}
		page, exact := pageForRemote(id, value, pct)
		fmt.Printf("%s -> page %d/%d (exact=%v)\n", value, page+1, info.pages, exact)
	}
	fmt.Printf("  (%.1fs)\n", time.Since(t0).Seconds())
}
