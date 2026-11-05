package main

// verify: development check of the PDF text extraction and the xpointer code
// against ground truth exported from KOReader's engines (MuPDF, crengine).
//   kosync-bridge verify <book.epub> <book.pdf> <groundtruth.json>

import (
	"encoding/json"
	"fmt"
	"os"
)

type groundTruth struct {
	PdfPages []string `json:"pdf_pages"`
	Samples  []struct {
		XP   string `json:"xp"`
		Node string `json:"node"`
	} `json:"samples"`
}

func runVerify(epubPath, pdfPath, gtPath string) {
	var gt groundTruth
	data, err := os.ReadFile(gtPath)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	json.Unmarshal(data, &gt)

	pages, err := pdfPageTexts(pdfPath)
	if err != nil {
		fmt.Println("pdf:", err)
		os.Exit(1)
	}
	same, shown := 0, 0
	for i := range pages {
		if i < len(gt.PdfPages) && norm(pages[i]) == norm(gt.PdfPages[i]) {
			same++
		} else if shown < 3 {
			shown++
			fmt.Printf("pdf page %d differs\n  ours:  %.150s\n  mupdf: %.150s\n", i+1, norm(pages[i]), norm(gt.PdfPages[min(i, len(gt.PdfPages)-1)]))
		}
	}
	fmt.Printf("PDF: %d pages (MuPDF %d), identical text: %d\n", len(pages), len(gt.PdfPages), same)

	book, err := openEPUB(epubPath)
	if err != nil {
		fmt.Println("epub:", err)
		os.Exit(1)
	}
	resolved, textOK, genOK, shown := 0, 0, 0, 0
	for _, s := range gt.Samples {
		t, off, err := book.resolve(s.XP)
		if err != nil {
			if shown < 8 {
				shown++
				fmt.Printf("resolve failed: %s (%v)\n", s.XP, err)
			}
			continue
		}
		resolved++
		ours := string(t.text)
		want := s.Node
		if len([]rune(ours)) > len([]rune(want)) {
			ours = string([]rune(ours)[:len([]rune(want))])
		}
		if ours == want {
			textOK++
		} else if shown < 8 {
			shown++
			fmt.Printf("text differs at %s\n  ours: %q\n  cre:  %q\n", s.XP, trunc(ours, 90), trunc(want, 90))
		}
		if g := book.xpointerFor(t, off); g == s.XP {
			genOK++
		} else if shown < 8 {
			shown++
			fmt.Printf("xpointer differs\n  ours: %s\n  cre:  %s\n", g, s.XP)
		}
	}
	fmt.Printf("XPointers: %d samples, resolved %d, same text %d, regenerated identically %d\n",
		len(gt.Samples), resolved, textOK, genOK)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
