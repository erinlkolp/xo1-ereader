package layout

import (
	"strings"
	"unicode"

	"golang.org/x/image/math/fixed"

	"xo1-ereader/internal/epub"
	"xo1-ereader/internal/fonts"
)

// Run is a single styled horizontal text segment on a Line.
type Run struct {
	X      int
	Text   string
	Style  fonts.Style
	SizePx int
}

// Line is a single visual row placed on a Page.
type Line struct {
	Y         int // baseline Y in page pixel coordinates
	TopY      int // top of line box
	BottomY   int // bottom of line box
	Runs      []Run
	QuoteBarX int  // if > 0, draw vertical blockquote rule at this X
	CodeBg    bool // if true, render shaded code background strip
	CodeLeft  int
	CodeRight int
	IsRule    bool // horizontal divider (<hr>)
	RuleLeft  int
	RuleRight int
}

// Page holds all rendered lines for one screenful of a chapter.
type Page struct {
	Lines       []Line
	StartOffset int    // character offset of first text on this page
	EndOffset   int    // character offset after last text on this page
	Preview     string // short snippet for bookmark labels
}

// ChapterLayout holds paginated pages and anchor lookup for one chapter.
type ChapterLayout struct {
	Pages       []Page
	Anchors     map[string]int // HTML anchor ID -> page index
	TotalChars  int
}

// Options defines page geometry and typography settings.
type Options struct {
	PageWidth    int
	PageHeight   int
	MarginLeft   int
	MarginRight  int
	MarginTop    int
	MarginBottom int
	BaseFontSize int
	FontFamily   fonts.Family
}

// PageForOffset returns the page index containing charOffset.
func (cl *ChapterLayout) PageForOffset(charOffset int) int {
	if len(cl.Pages) == 0 {
		return 0
	}
	if charOffset <= 0 {
		return 0
	}
	for i, pg := range cl.Pages {
		if charOffset < pg.EndOffset || i == len(cl.Pages)-1 {
			return i
		}
	}
	return len(cl.Pages) - 1
}

type wordToken struct {
	text    string
	style   fonts.Style
	sizePx  int
	width   fixed.Int26_6
	isSpace bool
	isBreak bool
	chars   int
}

type laidBlock struct {
	lines      []Line
	anchors    []string
	kind       epub.BlockKind
	startChar  int
	endChar    int
	height     int
	spaceAbove int
	spaceBelow int
}

// Paginate lays out chapter blocks into discrete pages for the given viewport and font settings.
func Paginate(blocks []epub.Block, fm *fonts.Manager, opt Options) *ChapterLayout {
	contentLeft := opt.MarginLeft
	contentRight := opt.PageWidth - opt.MarginRight
	contentWidth := contentRight - contentLeft
	if contentWidth < 120 {
		contentWidth = 120
		contentRight = contentLeft + contentWidth
	}
	contentTop := opt.MarginTop
	contentBottom := opt.PageHeight - opt.MarginBottom
	if contentBottom-contentTop < 100 {
		contentBottom = contentTop + 100
	}

	// Step 1: Lay out each block into relative-Y lines
	laid := make([]laidBlock, 0, len(blocks))
	charCursor := 0

	for _, blk := range blocks {
		lb := layoutBlock(blk, fm, opt, contentLeft, contentRight, charCursor)
		charCursor = lb.endChar
		laid = append(laid, lb)
	}

	// Step 2: Flow laid blocks onto pages, with simple heading orphan prevention
	cl := &ChapterLayout{
		Anchors:    make(map[string]int),
		TotalChars: charCursor,
	}

	curY := contentTop
	curPage := Page{StartOffset: 0}
	hasLines := false

	flushPage := func(nextStart int) {
		if !hasLines && len(cl.Pages) > 0 {
			return
		}
		curPage.EndOffset = nextStart
		cl.Pages = append(cl.Pages, curPage)
		curPage = Page{StartOffset: nextStart}
		curY = contentTop
		hasLines = false
	}

	for bi, lb := range laid {
		// Apply top spacing if we are not at the top of a page
		if hasLines {
			curY += lb.spaceAbove
		}

		// Heading orphan prevention: if this is a heading and wouldn't leave room for
		// at least 2 lines of the following block, start the heading on a new page.
		if lb.kind == epub.BlockHeading && hasLines {
			needed := lb.height + lb.spaceBelow + opt.BaseFontSize*3
			if curY+needed > contentBottom {
				flushPage(lb.startChar)
			}
		}

		// Record anchors at the page where this block starts
		for _, a := range lb.anchors {
			if _, exists := cl.Anchors[a]; !exists {
				cl.Anchors[a] = len(cl.Pages)
			}
		}

		// Estimate char offset progression across lines in this block
		lineCount := len(lb.lines)
		blockChars := lb.endChar - lb.startChar

		for li, ln := range lb.lines {
			lineH := ln.BottomY - ln.TopY
			if lineH < 1 {
				lineH = opt.BaseFontSize
			}
			if hasLines && curY+lineH > contentBottom {
				// Compute approximate char offset at this split point
				splitChar := lb.startChar
				if lineCount > 0 {
					splitChar = lb.startChar + (blockChars*li)/lineCount
				}
				flushPage(splitChar)
			}

			placed := ln
			dy := curY - ln.TopY
			placed.Y += dy
			placed.TopY += dy
			placed.BottomY += dy

			if !hasLines {
				if lineCount > 0 {
					curPage.StartOffset = lb.startChar + (blockChars*li)/lineCount
				} else {
					curPage.StartOffset = lb.startChar
				}
			}
			curPage.Lines = append(curPage.Lines, placed)
			if len(curPage.Preview) < 52 {
				appendPreview(&curPage.Preview, placed)
			}
			hasLines = true
			curY += lineH
		}

		if hasLines {
			curY += lb.spaceBelow
		}
		_ = bi
	}

	if hasLines || len(cl.Pages) == 0 {
		curPage.EndOffset = charCursor
		cl.Pages = append(cl.Pages, curPage)
	}

	return cl
}

func appendPreview(dst *string, ln Line) {
	for _, r := range ln.Runs {
		t := strings.TrimSpace(r.Text)
		if t == "" {
			continue
		}
		if *dst != "" {
			*dst += " "
		}
		*dst += t
		if len(*dst) >= 52 {
			*dst = strings.TrimSpace((*dst)[:52])
			return
		}
	}
}

func headingSize(base, level int) int {
	switch level {
	case 1:
		return (base * 145) / 100
	case 2:
		return (base * 128) / 100
	case 3:
		return (base * 115) / 100
	default:
		return (base * 105) / 100
	}
}

func layoutBlock(blk epub.Block, fm *fonts.Manager, opt Options, contentLeft, contentRight, startChar int) laidBlock {
	lb := laidBlock{
		anchors:   blk.Anchors,
		kind:      blk.Kind,
		startChar: startChar,
		endChar:   startChar,
	}

	base := opt.BaseFontSize
	lineLeading := (base * 142) / 100 // comfortable 1.42x book line height

	switch blk.Kind {
	case epub.BlockRule:
		lb.spaceAbove = base / 2
		lb.spaceBelow = base / 2
		h := base / 2
		if h < 12 {
			h = 12
		}
		inset := (contentRight - contentLeft) / 6
		lb.lines = []Line{{
			Y:         h / 2,
			TopY:      0,
			BottomY:   h,
			IsRule:    true,
			RuleLeft:  contentLeft + inset,
			RuleRight: contentRight - inset,
		}}
		lb.height = h
		return lb

	case epub.BlockCode:
		codeSize := (base * 82) / 100
		if codeSize < 12 {
			codeSize = 12
		}
		face := fm.Face(opt.FontFamily, fonts.StyleMono, codeSize)
		lineH := (face.Height() * 125) / 100
		ascent := face.Ascent()
		padX := base / 2
		left := contentLeft + padX
		right := contentRight - padX
		maxW := fixed.I(right - left)

		var raw strings.Builder
		for _, sp := range blk.Spans {
			raw.WriteString(sp.Text)
		}
		rawLines := strings.Split(strings.Trim(raw.String(), "\n"), "\n")
		y := 0
		for _, rl := range rawLines {
			rl = strings.ReplaceAll(rl, "\t", "    ")
			lb.endChar += len([]rune(rl)) + 1
			// Hard-wrap long code lines by rune width
			chunks := hardWrapString(rl, face, maxW)
			for _, chunk := range chunks {
				lb.lines = append(lb.lines, Line{
					Y:         y + ascent + (lineH-face.Height())/2,
					TopY:      y,
					BottomY:   y + lineH,
					Runs:      []Run{{X: left, Text: chunk, Style: fonts.StyleMono, SizePx: codeSize}},
					CodeBg:    true,
					CodeLeft:  contentLeft + padX/2,
					CodeRight: contentRight - padX/2,
				})
				y += lineH
			}
		}
		lb.height = y
		lb.spaceAbove = base / 2
		lb.spaceBelow = base / 2
		return lb
	}

	// Determine block geometry & typography
	fontSize := base
	left := contentLeft
	right := contentRight
	quoteBarX := 0
	firstLinePrefix := ""
	firstLinePrefixX := 0

	switch blk.Kind {
	case epub.BlockHeading:
		fontSize = headingSize(base, blk.Level)
		lineLeading = (fontSize * 130) / 100
		lb.spaceAbove = (base * 95) / 100
		lb.spaceBelow = (base * 45) / 100
	case epub.BlockQuote:
		depth := blk.Level
		if depth < 1 {
			depth = 1
		}
		if depth > 3 {
			depth = 3
		}
		indent := depth * ((base * 120) / 100)
		quoteBarX = contentLeft + indent - (base*45)/100
		left = contentLeft + indent
		right = contentRight - (base*40)/100
		lb.spaceAbove = (base * 35) / 100
		lb.spaceBelow = (base * 45) / 100
	case epub.BlockListItem:
		depth := blk.Level
		if depth < 1 {
			depth = 1
		}
		if depth > 4 {
			depth = 4
		}
		indent := depth * ((base * 135) / 100)
		left = contentLeft + indent
		firstLinePrefix = blk.ListLabel
		firstLinePrefixX = left - (base * 105 / 100)
		if firstLinePrefixX < contentLeft {
			firstLinePrefixX = contentLeft
		}
		lb.spaceAbove = (base * 15) / 100
		lb.spaceBelow = (base * 25) / 100
	default: // Paragraph
		lb.spaceAbove = 0
		lb.spaceBelow = (base * 55) / 100
	}

	if right-left < 80 {
		left = contentLeft
		right = contentRight
	}
	maxWidth := fixed.I(right - left)

	// Tokenize spans into words, spaces, and explicit line breaks
	tokens := tokenizeSpans(blk.Spans, fm, opt.FontFamily, fontSize)
	for _, t := range tokens {
		lb.endChar += t.chars
	}

	baseFace := fm.Face(opt.FontFamily, fonts.StyleRegular, fontSize)
	if blk.Kind == epub.BlockHeading {
		baseFace = fm.Face(opt.FontFamily, fonts.StyleBold, fontSize)
	}
	ascent := baseFace.Ascent()
	topPad := (lineLeading - baseFace.Height()) / 2
	if topPad < 0 {
		topPad = 0
	}

	// Wrap tokens into visual lines
	y := 0
	lineIdx := 0
	var curRuns []Run
	curX := fixed.Int26_6(0)

	commitLine := func() {
		ln := Line{
			Y:         y + topPad + ascent,
			TopY:      y,
			BottomY:   y + lineLeading,
			Runs:      curRuns,
			QuoteBarX: quoteBarX,
		}
		if lineIdx == 0 && firstLinePrefix != "" {
			prefixRun := Run{
				X:      firstLinePrefixX,
				Text:   firstLinePrefix,
				Style:  fonts.StyleBold,
				SizePx: fontSize,
			}
			ln.Runs = append([]Run{prefixRun}, ln.Runs...)
		}
		lb.lines = append(lb.lines, ln)
		curRuns = nil
		curX = 0
		y += lineLeading
		lineIdx++
	}

	appendTokenToLine := func(tok wordToken) {
		runX := left + curX.Round()
		if len(curRuns) > 0 {
			last := &curRuns[len(curRuns)-1]
			if last.Style == tok.style && last.SizePx == tok.sizePx {
				last.Text += tok.text
				curX += tok.width
				return
			}
		}
		curRuns = append(curRuns, Run{
			X:      runX,
			Text:   tok.text,
			Style:  tok.style,
			SizePx: tok.sizePx,
		})
		curX += tok.width
	}

	for _, tok := range tokens {
		if tok.isBreak {
			commitLine()
			continue
		}
		if tok.isSpace {
			// Don't start a new line with leading whitespace
			if len(curRuns) == 0 {
				continue
			}
			if curX+tok.width <= maxWidth {
				appendTokenToLine(tok)
			}
			continue
		}

		// Non-space word token
		if len(curRuns) > 0 && curX+tok.width > maxWidth {
			// Trim trailing space on previous run if any
			trimTrailingSpace(curRuns)
			commitLine()
		}

		// If a single word is wider than the entire line, hard-wrap it
		if tok.width > maxWidth {
			face := fm.Face(opt.FontFamily, tok.style, tok.sizePx)
			parts := hardWrapString(tok.text, face, maxWidth)
			for pi, part := range parts {
				if pi > 0 {
					commitLine()
				}
				w := face.MeasureString(part)
				appendTokenToLine(wordToken{
					text:   part,
					style:  tok.style,
					sizePx: tok.sizePx,
					width:  w,
				})
			}
			continue
		}

		appendTokenToLine(tok)
	}

	if len(curRuns) > 0 {
		trimTrailingSpace(curRuns)
		commitLine()
	}

	lb.height = y
	return lb
}

func trimTrailingSpace(runs []Run) {
	if len(runs) == 0 {
		return
	}
	last := &runs[len(runs)-1]
	last.Text = strings.TrimRight(last.Text, " ")
}

func tokenizeSpans(spans []epub.Span, fm *fonts.Manager, fam fonts.Family, fontSize int) []wordToken {
	tokens := make([]wordToken, 0, len(spans)*4)
	for _, sp := range spans {
		if sp.LineBreak {
			tokens = append(tokens, wordToken{isBreak: true, chars: 1})
			continue
		}
		size := fontSize
		if sp.Style&fonts.StyleMono != 0 {
			size = (fontSize * 86) / 100
			if size < 12 {
				size = 12
			}
		}
		face := fm.Face(fam, sp.Style, size)
		runes := []rune(sp.Text)
		i := 0
		for i < len(runes) {
			if unicode.IsSpace(runes[i]) {
				j := i + 1
				for j < len(runes) && unicode.IsSpace(runes[j]) {
					j++
				}
				tokens = append(tokens, wordToken{
					text:    " ",
					style:   sp.Style,
					sizePx:  size,
					width:   face.Advance(' '),
					isSpace: true,
					chars:   j - i,
				})
				i = j
				continue
			}
			j := i + 1
			for j < len(runes) && !unicode.IsSpace(runes[j]) {
				j++
			}
			word := string(runes[i:j])
			tokens = append(tokens, wordToken{
				text:   word,
				style:  sp.Style,
				sizePx: size,
				width:  face.MeasureString(word),
				chars:  j - i,
			})
			i = j
		}
	}
	return tokens
}

func hardWrapString(s string, face *fonts.CachedFace, maxW fixed.Int26_6) []string {
	if s == "" {
		return []string{""}
	}
	var out []string
	var sb strings.Builder
	var curW fixed.Int26_6
	for _, r := range s {
		adv := face.Advance(r)
		if sb.Len() > 0 && curW+adv > maxW {
			out = append(out, sb.String())
			sb.Reset()
			curW = 0
		}
		sb.WriteRune(r)
		curW += adv
	}
	if sb.Len() > 0 {
		out = append(out, sb.String())
	}
	return out
}
