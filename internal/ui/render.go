package ui

import (
	"fmt"
	"image"
	"image/color"
	"path/filepath"

	"xo1-ereader/internal/fonts"
	"xo1-ereader/internal/layout"
)

func fillRect(dst *image.RGBA, x0, y0, x1, y1 int, col color.RGBA) {
	b := dst.Bounds()
	if x0 < b.Min.X {
		x0 = b.Min.X
	}
	if y0 < b.Min.Y {
		y0 = b.Min.Y
	}
	if x1 > b.Max.X {
		x1 = b.Max.X
	}
	if y1 > b.Max.Y {
		y1 = b.Max.Y
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}
	for y := y0; y < y1; y++ {
		off := dst.PixOffset(x0, y)
		for x := x0; x < x1; x++ {
			dst.Pix[off] = col.R
			dst.Pix[off+1] = col.G
			dst.Pix[off+2] = col.B
			dst.Pix[off+3] = col.A
			off += 4
		}
	}
}

func strokeRect(dst *image.RGBA, x0, y0, x1, y1, thickness int, col color.RGBA) {
	if thickness < 1 {
		thickness = 1
	}
	fillRect(dst, x0, y0, x1, y0+thickness, col)
	fillRect(dst, x0, y1-thickness, x1, y1, col)
	fillRect(dst, x0, y0, x0+thickness, y1, col)
	fillRect(dst, x1-thickness, y0, x1, y1, col)
}

func (a *App) ensureCanvas(w, h int) *image.RGBA {
	if a.canvas == nil || a.canvas.Rect.Dx() != w || a.canvas.Rect.Dy() != h {
		a.canvas = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	return a.canvas
}

func (a *App) render() {
	logW, logH := a.logicalSize()
	dst := a.ensureCanvas(logW, logH)
	th := themeByName(a.store.Prefs.Theme)

	fillRect(dst, 0, 0, logW, logH, th.Bg)

	switch a.view {
	case viewReader:
		a.renderReader(dst, th)
	case viewLibrary:
		a.renderLibrary(dst, th)
	case viewMenu:
		a.renderReader(dst, th)
		a.renderMenuOverlay(dst, th)
	case viewTOC:
		a.renderTOC(dst, th)
	case viewBookmarks:
		a.renderBookmarks(dst, th)
	case viewHelp:
		a.renderHelp(dst, th)
	}

	if a.toast != "" {
		a.renderToast(dst, th, a.toast)
	}

	_ = a.win.Present(dst, a.store.Prefs.Rotation)
}

func (a *App) readerMetrics(w, h int) (marginL, marginR, marginT, marginB, headerH, footerH int) {
	headerH = 44
	footerH = 44
	// Keep comfortable book line lengths on both 1200x900 landscape and 900x1200 portrait
	maxTextW := 820
	if w > maxTextW+120 {
		marginL = (w - maxTextW) / 2
		marginR = marginL
	} else {
		marginL = w / 14
		if marginL < 28 {
			marginL = 28
		}
		marginR = marginL
	}
	marginT = headerH + 22
	marginB = footerH + 18
	return
}

func (a *App) renderReader(dst *image.RGBA, th Theme) {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	marginL, marginR, _, _, headerH, footerH := a.readerMetrics(w, h)

	uiSmall := a.fm.Face(fonts.FamilySans, fonts.StyleRegular, 18)
	uiSmallBold := a.fm.Face(fonts.FamilySans, fonts.StyleBold, 18)

	if a.book == nil {
		// Welcome / empty state
		msgFace := a.fm.Face(fonts.FamilySans, fonts.StyleBold, 28)
		subFace := a.fm.Face(fonts.FamilySans, fonts.StyleRegular, 20)
		m1 := "XO-1 E-Reader"
		m2 := "Press Square (□) or 'l' to open Library"
		msgFace.DrawString(dst, (w-msgFace.MeasureStringPx(m1))/2, h/2-20, m1, th.Fg, th.CrispMono)
		subFace.DrawString(dst, (w-subFace.MeasureStringPx(m2))/2, h/2+26, m2, th.Muted, th.CrispMono)
		return
	}

	// Top header bar
	bookTitle := a.book.Meta.Title
	chTitle := a.book.ChapterTitle(a.chapterIdx)
	hdrY := (headerH+uiSmall.Ascent())/2 - 2

	// Bookmark badge if current page has a bookmark
	hasBM := false
	var curPage layout.Page
	if a.curLayout != nil && a.pageIdx >= 0 && a.pageIdx < len(a.curLayout.Pages) {
		curPage = a.curLayout.Pages[a.pageIdx]
		hasBM = a.store.HasBookmarkNear(a.book.Meta.ID, a.chapterIdx, curPage.StartOffset, curPage.EndOffset+1)
	}

	leftMax := (w - marginL - marginR) / 2
	rightMax := leftMax - 16
	if hasBM {
		bmTag := "[BOOKMARK]"
		bmW := uiSmallBold.MeasureStringPx(bmTag)
		bmX := w - marginR - bmW
		fillRect(dst, bmX-8, 8, w-marginR+4, headerH-8, th.SelectBg)
		uiSmallBold.DrawString(dst, bmX-2, hdrY, bmTag, th.SelectFg, th.CrispMono)
		rightMax -= bmW + 20
	}

	uiSmallBold.DrawString(dst, marginL, hdrY, uiSmallBold.Truncate(bookTitle, leftMax), th.Muted, th.CrispMono)
	if rightMax > 80 {
		ct := uiSmall.Truncate(chTitle, rightMax)
		ctW := uiSmall.MeasureStringPx(ct)
		ctX := w - marginR - ctW
		if hasBM {
			ctX -= uiSmallBold.MeasureStringPx("[BOOKMARK]") + 20
		}
		uiSmall.DrawString(dst, ctX, hdrY, ct, th.Muted, th.CrispMono)
	}
	fillRect(dst, marginL, headerH-1, w-marginR, headerH, th.Rule)

	// Main page content
	bodyFam := a.bodyFamily()
	if a.curLayout != nil && a.pageIdx >= 0 && a.pageIdx < len(a.curLayout.Pages) {
		for _, ln := range curPage.Lines {
			if ln.CodeBg {
				fillRect(dst, ln.CodeLeft, ln.TopY, ln.CodeRight, ln.BottomY, th.CodeBg)
			}
			if ln.QuoteBarX > 0 {
				fillRect(dst, ln.QuoteBarX, ln.TopY, ln.QuoteBarX+4, ln.BottomY, th.Rule)
			}
			if ln.IsRule {
				fillRect(dst, ln.RuleLeft, ln.Y, ln.RuleRight, ln.Y+2, th.Rule)
				continue
			}
			for _, run := range ln.Runs {
				face := a.fm.Face(bodyFam, run.Style, run.SizePx)
				face.DrawString(dst, run.X, ln.Y, run.Text, th.Fg, th.CrispMono)
			}
		}
	}

	// Bottom footer bar + progress track
	footTop := h - footerH
	fillRect(dst, marginL, footTop, w-marginR, footTop+1, th.Rule)

	totalCh := len(a.book.Spine)
	totalPages := 1
	if a.curLayout != nil && len(a.curLayout.Pages) > 0 {
		totalPages = len(a.curLayout.Pages)
	}
	bookPct := a.overallProgressPercent()

	// Progress bar (4px tall) along footer top
	trackW := w - marginL - marginR
	filledW := (trackW * bookPct) / 100
	if filledW > 0 {
		fillRect(dst, marginL, footTop, marginL+filledW, footTop+4, th.Fg)
	}

	footY := footTop + (footerH+uiSmall.Ascent())/2
	leftStatus := fmt.Sprintf("Ch %d/%d  ·  Page %d/%d", a.chapterIdx+1, totalCh, a.pageIdx+1, totalPages)
	rightStatus := fmt.Sprintf("%d%%  ·  [□/m] Menu", bookPct)
	uiSmall.DrawString(dst, marginL, footY, leftStatus, th.Muted, th.CrispMono)
	rw := uiSmall.MeasureStringPx(rightStatus)
	uiSmall.DrawString(dst, w-marginR-rw, footY, rightStatus, th.Muted, th.CrispMono)
}

func (a *App) renderListFrame(dst *image.RGBA, th Theme, title, subtitle, footerHint string) (listTop, listBottom, padX int) {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	padX = 40
	if w < 700 {
		padX = 24
	}
	titleFace := a.fm.Face(fonts.FamilySans, fonts.StyleBold, 28)
	subFace := a.fm.Face(fonts.FamilySans, fonts.StyleRegular, 18)

	titleFace.DrawString(dst, padX, 46, title, th.Fg, th.CrispMono)
	if subtitle != "" {
		subFace.DrawString(dst, padX, 76, subFace.Truncate(subtitle, w-padX*2), th.Muted, th.CrispMono)
		listTop = 94
	} else {
		listTop = 68
	}
	fillRect(dst, padX, listTop-8, w-padX, listTop-6, th.Rule)

	listBottom = h - 54
	fillRect(dst, padX, listBottom+4, w-padX, listBottom+6, th.Rule)
	subFace.DrawString(dst, padX, h-18, subFace.Truncate(footerHint, w-padX*2), th.Muted, th.CrispMono)
	return listTop, listBottom, padX
}

func (a *App) renderLibrary(dst *image.RGBA, th Theme) {
	w := dst.Rect.Dx()
	sub := fmt.Sprintf("Folder: %s  (%d items)", a.libDir, len(a.libItems))
	hint := "D-Pad/Arrows: Move  ·  ✓/Enter: Open  ·  ○/×: Page  ·  □/Esc: Back  ·  q: Quit"
	top, bottom, padX := a.renderListFrame(dst, th, "Library", sub, hint)

	rowH := 62
	visible := (bottom - top) / rowH
	if visible < 1 {
		visible = 1
	}
	a.libScroll = clampScroll(a.libSel, a.libScroll, visible, len(a.libItems))

	itemTitle := a.fm.Face(fonts.FamilySans, fonts.StyleBold, 22)
	itemMeta := a.fm.Face(fonts.FamilySans, fonts.StyleRegular, 17)

	if len(a.libItems) == 0 {
		itemTitle.DrawString(dst, padX, top+48, "No .epub files or folders found here.", th.Muted, th.CrispMono)
		itemMeta.DrawString(dst, padX, top+82, "Copy .epub books into this folder or launch with: xo1-reader book.epub", th.Muted, th.CrispMono)
		return
	}

	for i := 0; i < visible && a.libScroll+i < len(a.libItems); i++ {
		idx := a.libScroll + i
		it := a.libItems[idx]
		ry := top + i*rowH
		selected := idx == a.libSel

		fg := th.Fg
		muted := th.Muted
		if selected {
			fillRect(dst, padX-8, ry+2, w-padX+8, ry+rowH-2, th.SelectBg)
			fg = th.SelectFg
			muted = th.SelectFg
		}

		maxW := w - padX*2 - 16
		if it.isDir {
			label := "[DIR] " + it.title
			itemTitle.DrawString(dst, padX+4, ry+30, itemTitle.Truncate(label, maxW), fg, th.CrispMono)
			itemMeta.DrawString(dst, padX+4, ry+52, "Open folder", muted, th.CrispMono)
		} else {
			itemTitle.DrawString(dst, padX+4, ry+28, itemTitle.Truncate(it.title, maxW), fg, th.CrispMono)
			meta := it.author
			if meta == "" {
				meta = filepath.Base(it.path)
			}
			if it.sizeBytes > 0 {
				meta = fmt.Sprintf("%s  ·  %d KB", meta, (it.sizeBytes+1023)/1024)
			}
			itemMeta.DrawString(dst, padX+4, ry+51, itemMeta.Truncate(meta, maxW), muted, th.CrispMono)
		}
		if !selected {
			fillRect(dst, padX, ry+rowH-1, w-padX, ry+rowH, th.Rule)
		}
	}
}

func (a *App) renderTOC(dst *image.RGBA, th Theme) {
	w := dst.Rect.Dx()
	sub := ""
	if a.book != nil {
		sub = a.book.Meta.Title
	}
	hint := "D-Pad/Arrows: Move  ·  ✓/Enter: Jump to Chapter  ·  ○/×: Page  ·  □/Esc: Back"
	top, bottom, padX := a.renderListFrame(dst, th, "Table of Contents", sub, hint)

	if a.book == nil || len(a.book.TOC) == 0 {
		return
	}

	rowH := 44
	visible := (bottom - top) / rowH
	if visible < 1 {
		visible = 1
	}
	a.tocScroll = clampScroll(a.tocSel, a.tocScroll, visible, len(a.book.TOC))

	faceReg := a.fm.Face(fonts.FamilySans, fonts.StyleRegular, 21)
	faceBold := a.fm.Face(fonts.FamilySans, fonts.StyleBold, 21)

	for i := 0; i < visible && a.tocScroll+i < len(a.book.TOC); i++ {
		idx := a.tocScroll + i
		entry := a.book.TOC[idx]
		ry := top + i*rowH
		selected := idx == a.tocSel
		isCurrent := entry.ChapterIndex == a.chapterIdx

		fg := th.Fg
		if selected {
			fillRect(dst, padX-8, ry+2, w-padX+8, ry+rowH-2, th.SelectBg)
			fg = th.SelectFg
		}

		indent := entry.Depth * 26
		if indent > 120 {
			indent = 120
		}
		f := faceReg
		prefix := ""
		if isCurrent {
			f = faceBold
			prefix = "▸ "
		}
		maxW := w - padX*2 - indent - 16
		f.DrawString(dst, padX+4+indent, ry+30, f.Truncate(prefix+entry.Title, maxW), fg, th.CrispMono)
	}
}

func (a *App) renderBookmarks(dst *image.RGBA, th Theme) {
	w := dst.Rect.Dx()
	sub := ""
	if a.book != nil {
		sub = a.book.Meta.Title
	}
	hint := "D-Pad/Arrows: Move  ·  ✓/Enter: Jump/Add  ·  d/Backspace: Delete  ·  □/Esc: Back"
	top, bottom, padX := a.renderListFrame(dst, th, "Bookmarks", sub, hint)

	items := a.bookmarkMenuItems()
	rowH := 56
	visible := (bottom - top) / rowH
	if visible < 1 {
		visible = 1
	}
	a.bmScroll = clampScroll(a.bmSel, a.bmScroll, visible, len(items))

	faceBold := a.fm.Face(fonts.FamilySans, fonts.StyleBold, 21)
	faceSub := a.fm.Face(fonts.FamilySans, fonts.StyleRegular, 17)

	for i := 0; i < visible && a.bmScroll+i < len(items); i++ {
		idx := a.bmScroll + i
		it := items[idx]
		ry := top + i*rowH
		selected := idx == a.bmSel
		fg := th.Fg
		muted := th.Muted
		if selected {
			fillRect(dst, padX-8, ry+2, w-padX+8, ry+rowH-2, th.SelectBg)
			fg = th.SelectFg
			muted = th.SelectFg
		}
		maxW := w - padX*2 - 16
		faceBold.DrawString(dst, padX+4, ry+26, faceBold.Truncate(it.title, maxW), fg, th.CrispMono)
		if it.subtitle != "" {
			faceSub.DrawString(dst, padX+4, ry+47, faceSub.Truncate(it.subtitle, maxW), muted, th.CrispMono)
		}
	}
}

func (a *App) renderMenuOverlay(dst *image.RGBA, th Theme) {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	items := a.menuItems()

	boxW := 520
	if w-40 < boxW {
		boxW = w - 40
	}
	rowH := 42
	hdrH := 56
	footH := 42
	boxH := hdrH + len(items)*rowH + footH
	if boxH > h-24 {
		rowH = (h - 24 - hdrH - footH) / len(items)
		if rowH < 30 {
			rowH = 30
		}
		boxH = hdrH + len(items)*rowH + footH
	}

	x0 := (w - boxW) / 2
	y0 := (h - boxH) / 2
	x1 := x0 + boxW
	y1 := y0 + boxH

	fillRect(dst, x0, y0, x1, y1, th.PanelBg)
	strokeRect(dst, x0, y0, x1, y1, 3, th.PanelBorder)

	titleFace := a.fm.Face(fonts.FamilySans, fonts.StyleBold, 24)
	itemFace := a.fm.Face(fonts.FamilySans, fonts.StyleRegular, 20)
	itemBold := a.fm.Face(fonts.FamilySans, fonts.StyleBold, 20)
	hintFace := a.fm.Face(fonts.FamilySans, fonts.StyleRegular, 16)

	titleFace.DrawString(dst, x0+22, y0+38, "XO-1 Reader Menu", th.Fg, th.CrispMono)
	fillRect(dst, x0+16, y0+hdrH-4, x1-16, y0+hdrH-2, th.Rule)

	for i, it := range items {
		ry := y0 + hdrH + i*rowH
		selected := i == a.menuSel
		fg := th.Fg
		if selected {
			fillRect(dst, x0+12, ry+2, x1-12, ry+rowH-2, th.SelectBg)
			fg = th.SelectFg
		}
		textY := ry + (rowH+itemFace.Ascent())/2 - 2
		itemBold.DrawString(dst, x0+24, textY, it.label, fg, th.CrispMono)
		if it.value != "" {
			vw := itemFace.MeasureStringPx(it.value)
			itemFace.DrawString(dst, x1-24-vw, textY, it.value, fg, th.CrispMono)
		}
	}

	fillRect(dst, x0+16, y1-footH+2, x1-16, y1-footH+4, th.Rule)
	hint := "D-Pad: Move/Adjust  ·  ✓/Enter: Select  ·  □/Esc: Close"
	hintFace.DrawString(dst, x0+22, y1-14, hintFace.Truncate(hint, boxW-44), th.Muted, th.CrispMono)
}

func (a *App) renderHelp(dst *image.RGBA, th Theme) {
	w := dst.Rect.Dx()
	hint := "Press any key (□ / ✓ / Esc / Enter) to return"
	top, _, padX := a.renderListFrame(dst, th, "Controls & XO-1 E-Book Mode", "Supports laptop keyboard, touchpad, and folded tablet bezel buttons", hint)

	hdrFace := a.fm.Face(fonts.FamilySans, fonts.StyleBold, 22)
	bodyFace := a.fm.Face(fonts.FamilySans, fonts.StyleRegular, 19)

	bezelRows := [][2]string{
		{"D-Pad Right / Down", "Next page (auto-rotates!)"},
		{"D-Pad Left / Up", "Previous page"},
		{"Cross (× / KP_PgDn)", "Next page / Page down"},
		{"Circle (○ / KP_PgUp)", "Previous page / Page up"},
		{"Square (□ / KP_Home)", "Open Menu / Go Back"},
		{"Checkmark (✓ / KP_End)", "Bookmark / Confirm"},
	}

	keyRows := [][2]string{
		{"Space / Right / PgDn / j", "Next page"},
		{"Backspace / Left / PgUp / k", "Previous page"},
		{"[  and  ]", "Previous / Next chapter"},
		{"m / Esc", "Open Menu"},
		{"c  /  l  /  b", "TOC / Library / Bookmark"},
		{"+ / -  and  g", "Font size / Serif-Sans"},
		{"t  /  r  /  f", "Theme / Rotate / Fullscreen"},
		{"q", "Save position & Quit"},
	}

	colSplit := w / 2
	if w < 800 {
		// Stacked layout in narrow portrait
		y := top + 28
		hdrFace.DrawString(dst, padX, y, "XO-1 Bezel (Tablet / E-Book Mode)", th.Fg, th.CrispMono)
		y += 34
		for _, r := range bezelRows {
			bodyFace.DrawString(dst, padX, y, r[0]+"  —  "+r[1], th.Fg, th.CrispMono)
			y += 30
		}
		y += 20
		hdrFace.DrawString(dst, padX, y, "Keyboard & Touchpad", th.Fg, th.CrispMono)
		y += 34
		for _, r := range keyRows {
			bodyFace.DrawString(dst, padX, y, r[0]+"  —  "+r[1], th.Fg, th.CrispMono)
			y += 30
		}
		return
	}

	// Side-by-side 2-column layout in landscape
	yL := top + 30
	hdrFace.DrawString(dst, padX, yL, "XO-1 Bezel (E-Book Mode)", th.Fg, th.CrispMono)
	yL += 38
	for _, r := range bezelRows {
		bodyFace.DrawString(dst, padX, yL, r[0], th.Fg, th.CrispMono)
		bodyFace.DrawString(dst, padX+250, yL, r[1], th.Muted, th.CrispMono)
		yL += 34
	}

	yR := top + 30
	hdrFace.DrawString(dst, colSplit+12, yR, "Keyboard & Touchpad", th.Fg, th.CrispMono)
	yR += 38
	for _, r := range keyRows {
		bodyFace.DrawString(dst, colSplit+12, yR, r[0], th.Fg, th.CrispMono)
		bodyFace.DrawString(dst, colSplit+280, yR, r[1], th.Muted, th.CrispMono)
		yR += 34
	}
}

func (a *App) renderToast(dst *image.RGBA, th Theme, msg string) {
	w, h := dst.Rect.Dx(), dst.Rect.Dy()
	face := a.fm.Face(fonts.FamilySans, fonts.StyleBold, 20)
	tw := face.MeasureStringPx(msg)
	bw := tw + 40
	bh := 42
	x0 := (w - bw) / 2
	y0 := h - 98
	fillRect(dst, x0, y0, x0+bw, y0+bh, th.SelectBg)
	strokeRect(dst, x0, y0, x0+bw, y0+bh, 2, th.PanelBorder)
	face.DrawString(dst, x0+20, y0+28, msg, th.SelectFg, th.CrispMono)
}

func clampScroll(sel, scroll, visible, total int) int {
	if sel < scroll {
		scroll = sel
	}
	if sel >= scroll+visible {
		scroll = sel - visible + 1
	}
	maxScroll := total - visible
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	return scroll
}
