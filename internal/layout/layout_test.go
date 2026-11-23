package layout

import (
	"strings"
	"testing"

	"xo1-ereader/internal/epub"
	"xo1-ereader/internal/fonts"
)

func TestPaginateAndAnchorAndOffsetPreservation(t *testing.T) {
	fm, err := fonts.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	var blocks []epub.Block
	blocks = append(blocks, epub.Block{
		Kind:  epub.BlockHeading,
		Level: 1,
		Spans: []epub.Span{{Text: "Introduction", Style: fonts.StyleBold}},
	})
	paraText := strings.Repeat("The quick brown fox jumps over the lazy dog on an OLPC XO-1. ", 6)
	for i := 0; i < 18; i++ {
		blk := epub.Block{
			Kind:  epub.BlockParagraph,
			Spans: []epub.Span{{Text: paraText, Style: fonts.StyleRegular}},
		}
		if i == 10 {
			blk.Anchors = []string{"midpoint"}
		}
		blocks = append(blocks, blk)
	}

	optLandscape := Options{
		PageWidth:    1200,
		PageHeight:   900,
		MarginLeft:   190,
		MarginRight:  190,
		MarginTop:    66,
		MarginBottom: 62,
		BaseFontSize: 28,
		FontFamily:   fonts.FamilySerif,
	}
	clLand := Paginate(blocks, fm, optLandscape)
	if len(clLand.Pages) < 3 {
		t.Fatalf("expected multiple pages, got %d", len(clLand.Pages))
	}
	midPage, ok := clLand.Anchors["midpoint"]
	if !ok || midPage <= 0 {
		t.Fatalf("expected anchor 'midpoint' on page > 0, got ok=%v page=%d", ok, midPage)
	}

	// Reflow into rotated portrait (900x1200) and confirm PageForOffset stays valid
	savedOffset := clLand.Pages[midPage].StartOffset
	optPortrait := optLandscape
	optPortrait.PageWidth = 900
	optPortrait.PageHeight = 1200
	optPortrait.MarginLeft = 64
	optPortrait.MarginRight = 64

	clPort := Paginate(blocks, fm, optPortrait)
	mappedPage := clPort.PageForOffset(savedOffset)
	if mappedPage < 0 || mappedPage >= len(clPort.Pages) {
		t.Fatalf("invalid mapped page %d of %d", mappedPage, len(clPort.Pages))
	}
	if clPort.Pages[mappedPage].EndOffset <= savedOffset && mappedPage != len(clPort.Pages)-1 {
		t.Fatalf("mapped page %d [%d..%d] does not cover offset %d",
			mappedPage, clPort.Pages[mappedPage].StartOffset, clPort.Pages[mappedPage].EndOffset, savedOffset)
	}
}
