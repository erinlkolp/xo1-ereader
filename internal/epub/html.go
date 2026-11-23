package epub

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/net/html"

	"xo1-ereader/internal/fonts"
)

// BlockKind classifies a vertical layout block extracted from chapter XHTML.
type BlockKind uint8

const (
	BlockParagraph BlockKind = iota
	BlockHeading
	BlockQuote
	BlockListItem
	BlockCode
	BlockRule
)

// Span is an inline run of styled text inside a Block.
type Span struct {
	Text      string
	Style     fonts.Style
	LineBreak bool // explicit <br>
}

// Block is a vertical flow element in a chapter.
type Block struct {
	Kind      BlockKind
	Level     int      // heading level (1..6) or list/quote nesting depth
	ListLabel string   // e.g. "•" or "1." for BlockListItem
	Anchors   []string // HTML id/name attributes pointing to this block
	Spans     []Span
}

// ParseXHTML parses chapter HTML/XHTML bytes into a clean list of Blocks.
func ParseXHTML(data []byte) ([]Block, error) {
	// Strip UTF-8 BOM if present
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	body := findElement(doc, "body")
	if body == nil {
		body = doc
	}

	p := &htmlParser{}
	p.walk(body, fonts.StyleRegular, 0, false)
	p.flushCurrent()
	return p.blocks, nil
}

func findElement(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, tag) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findElement(c, tag); found != nil {
			return found
		}
	}
	return nil
}

type htmlParser struct {
	blocks         []Block
	cur            *Block
	pendingAnchors []string
	listCounters   []int // 0 = unordered, >0 = ordered next number
}

func (p *htmlParser) addAnchor(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	if p.cur != nil {
		p.cur.Anchors = append(p.cur.Anchors, id)
	} else {
		p.pendingAnchors = append(p.pendingAnchors, id)
	}
}

func (p *htmlParser) startBlock(kind BlockKind, level int, listLabel string) {
	p.flushCurrent()
	b := Block{
		Kind:      kind,
		Level:     level,
		ListLabel: listLabel,
	}
	if len(p.pendingAnchors) > 0 {
		b.Anchors = append(b.Anchors, p.pendingAnchors...)
		p.pendingAnchors = nil
	}
	p.cur = &b
}

func (p *htmlParser) ensureParagraph(quoteDepth int) {
	if p.cur == nil {
		kind := BlockParagraph
		if quoteDepth > 0 {
			kind = BlockQuote
		}
		p.startBlock(kind, quoteDepth, "")
	}
}

func (p *htmlParser) appendSpan(sp Span, quoteDepth int) {
	if !sp.LineBreak && sp.Text == "" {
		return
	}
	p.ensureParagraph(quoteDepth)
	// Merge adjacent spans with identical style
	if !sp.LineBreak && len(p.cur.Spans) > 0 {
		last := &p.cur.Spans[len(p.cur.Spans)-1]
		if !last.LineBreak && last.Style == sp.Style {
			last.Text += sp.Text
			return
		}
	}
	p.cur.Spans = append(p.cur.Spans, sp)
}

func (p *htmlParser) flushCurrent() {
	if p.cur == nil {
		return
	}
	if p.cur.Kind == BlockRule {
		p.blocks = append(p.blocks, *p.cur)
		p.cur = nil
		return
	}
	// Clean leading/trailing whitespace on non-code blocks
	if p.cur.Kind != BlockCode {
		p.cur.Spans = cleanBlockSpans(p.cur.Spans)
	}
	hasContent := false
	for _, sp := range p.cur.Spans {
		if sp.LineBreak || strings.TrimSpace(sp.Text) != "" {
			hasContent = true
			break
		}
	}
	if hasContent {
		p.blocks = append(p.blocks, *p.cur)
	} else if len(p.cur.Anchors) > 0 {
		// Preserve anchors so they attach to the next visible block
		p.pendingAnchors = append(p.pendingAnchors, p.cur.Anchors...)
	}
	p.cur = nil
}

func (p *htmlParser) walk(n *html.Node, style fonts.Style, quoteDepth int, preformatted bool) {
	switch n.Type {
	case html.TextNode:
		if preformatted {
			p.appendSpan(Span{Text: strings.ReplaceAll(n.Data, "\r\n", "\n"), Style: style | fonts.StyleMono}, quoteDepth)
			return
		}
		text := normalizeInlineWhitespace(n.Data)
		if text == "" {
			return
		}
		// Skip pure whitespace if we haven't started a block yet
		if p.cur == nil && text == " " {
			return
		}
		p.appendSpan(Span{Text: text, Style: style}, quoteDepth)
		return

	case html.ElementNode:
		tag := strings.ToLower(n.Data)

		// Skip non-rendered tags
		switch tag {
		case "head", "title", "style", "script", "noscript", "svg", "math", "audio", "video":
			return
		}

		// Record anchor IDs
		for _, a := range n.Attr {
			if a.Key == "id" || (tag == "a" && a.Key == "name") {
				p.addAnchor(a.Val)
			}
		}

		switch tag {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			lvl := int(tag[1] - '0')
			p.startBlock(BlockHeading, lvl, "")
			p.walkChildren(n, style|fonts.StyleBold, quoteDepth, false)
			p.flushCurrent()

		case "p":
			kind := BlockParagraph
			if quoteDepth > 0 {
				kind = BlockQuote
			}
			p.startBlock(kind, quoteDepth, "")
			p.walkChildren(n, style, quoteDepth, false)
			p.flushCurrent()

		case "blockquote":
			p.flushCurrent()
			p.walkChildren(n, style, quoteDepth+1, false)
			p.flushCurrent()

		case "ul":
			p.flushCurrent()
			p.listCounters = append(p.listCounters, 0)
			p.walkChildren(n, style, quoteDepth, false)
			p.listCounters = p.listCounters[:len(p.listCounters)-1]
			p.flushCurrent()

		case "ol":
			p.flushCurrent()
			p.listCounters = append(p.listCounters, 1)
			p.walkChildren(n, style, quoteDepth, false)
			p.listCounters = p.listCounters[:len(p.listCounters)-1]
			p.flushCurrent()

		case "li":
			depth := len(p.listCounters)
			if depth == 0 {
				depth = 1
			}
			label := "•"
			if len(p.listCounters) > 0 {
				top := len(p.listCounters) - 1
				if p.listCounters[top] > 0 {
					label = fmt.Sprintf("%d.", p.listCounters[top])
					p.listCounters[top]++
				}
			}
			p.startBlock(BlockListItem, depth, label)
			p.walkChildren(n, style, quoteDepth, false)
			p.flushCurrent()

		case "pre":
			p.startBlock(BlockCode, quoteDepth, "")
			p.walkChildren(n, style|fonts.StyleMono, quoteDepth, true)
			p.flushCurrent()

		case "hr":
			p.startBlock(BlockRule, 0, "")
			p.flushCurrent()

		case "br":
			p.appendSpan(Span{LineBreak: true}, quoteDepth)

		case "div", "section", "article", "header", "footer", "main", "aside", "figure", "figcaption", "tr", "dt", "dd":
			p.flushCurrent()
			p.walkChildren(n, style, quoteDepth, preformatted)
			p.flushCurrent()

		case "td", "th":
			if p.cur != nil && len(p.cur.Spans) > 0 {
				p.appendSpan(Span{Text: "  ", Style: style}, quoteDepth)
			}
			s := style
			if tag == "th" {
				s |= fonts.StyleBold
			}
			p.walkChildren(n, s, quoteDepth, preformatted)

		case "b", "strong":
			p.walkChildren(n, style|fonts.StyleBold, quoteDepth, preformatted)

		case "i", "em", "cite", "dfn", "var":
			p.walkChildren(n, style|fonts.StyleItalic, quoteDepth, preformatted)

		case "code", "tt", "kbd", "samp":
			p.walkChildren(n, style|fonts.StyleMono, quoteDepth, preformatted)

		case "img":
			// Images are intentionally skipped for lightweight XO-1 reading;
			// display alt text in subtle italic brackets if descriptive.
			for _, a := range n.Attr {
				if a.Key == "alt" {
					alt := collapseSpace(a.Val)
					if alt != "" && len(alt) <= 120 {
						p.appendSpan(Span{Text: " [" + alt + "] ", Style: style | fonts.StyleItalic}, quoteDepth)
					}
					break
				}
			}

		default:
			p.walkChildren(n, style, quoteDepth, preformatted)
		}

	default:
		p.walkChildren(n, style, quoteDepth, preformatted)
	}
}

func (p *htmlParser) walkChildren(n *html.Node, style fonts.Style, quoteDepth int, preformatted bool) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		p.walk(c, style, quoteDepth, preformatted)
	}
}

func normalizeInlineWhitespace(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == '\u00A0' {
			// Non-breaking space -> normal space
			if !inSpace {
				sb.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		if unicode.IsSpace(r) {
			if !inSpace {
				sb.WriteByte(' ')
				inSpace = true
			}
		} else {
			sb.WriteRune(r)
			inSpace = false
		}
	}
	return sb.String()
}

func collapseSpace(s string) string {
	return strings.TrimSpace(normalizeInlineWhitespace(s))
}

func cleanBlockSpans(spans []Span) []Span {
	if len(spans) == 0 {
		return nil
	}
	out := make([]Span, 0, len(spans))
	prevEndedSpace := true
	for _, sp := range spans {
		if sp.LineBreak {
			out = append(out, sp)
			prevEndedSpace = true
			continue
		}
		t := sp.Text
		if prevEndedSpace {
			t = strings.TrimLeft(t, " ")
		}
		if t == "" {
			continue
		}
		prevEndedSpace = strings.HasSuffix(t, " ")
		out = append(out, Span{Text: t, Style: sp.Style})
	}
	for len(out) > 0 {
		last := &out[len(out)-1]
		if last.LineBreak {
			out = out[:len(out)-1]
			continue
		}
		last.Text = strings.TrimRight(last.Text, " ")
		if last.Text == "" {
			out = out[:len(out)-1]
			continue
		}
		break
	}
	return out
}
