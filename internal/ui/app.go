package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"xo1-ereader/internal/epub"
	"xo1-ereader/internal/fonts"
	"xo1-ereader/internal/layout"
	"xo1-ereader/internal/state"
	"xo1-ereader/internal/x11"
)

type viewMode uint8

const (
	viewReader viewMode = iota
	viewLibrary
	viewMenu
	viewTOC
	viewBookmarks
	viewHelp
)

type libraryItem struct {
	isDir     bool
	path      string
	title     string
	author    string
	sizeBytes int64
}

type menuItem struct {
	id    string
	label string
	value string
}

type bookmarkListItem struct {
	isAddAction bool
	bmIndex     int
	title       string
	subtitle    string
}

// Config configures initial startup options for the App.
type Config struct {
	InitialBook string
	LibraryDir  string
	Theme       string
	FontSize    int
	Rotation    int
	Fullscreen  bool
	HasTheme    bool
	HasFontSize bool
	HasRotation bool
	HasFull     bool
	StatePath   string
}

// App manages the X11 window, EPUB book, layout cache, state persistence, and UI event loop.
type App struct {
	win    *x11.Window
	fm     *fonts.Manager
	store  *state.Store
	canvas *image.RGBA

	book       *epub.Book
	chapterIdx int
	pageIdx    int
	curLayout  *layout.ChapterLayout
	cache      map[int]*layout.ChapterLayout

	view      viewMode
	prevView  viewMode
	toast     string
	quit      bool

	// Library browser state
	libDir    string
	libItems  []libraryItem
	libSel    int
	libScroll int

	// Menu / TOC / Bookmark selection state
	menuSel   int
	tocSel    int
	tocScroll int
	bmSel     int
	bmScroll  int
}

// NewApp initializes fonts, persistent state, X11 window, and optional initial book.
func NewApp(cfg Config) (*App, error) {
	fm, err := fonts.NewManager()
	if err != nil {
		return nil, fmt.Errorf("init fonts: %w", err)
	}

	st := state.Load(cfg.StatePath)
	if cfg.HasTheme {
		st.Prefs.Theme = cfg.Theme
	}
	if cfg.HasFontSize && cfg.FontSize >= 14 && cfg.FontSize <= 60 {
		st.Prefs.FontSize = cfg.FontSize
	}
	if cfg.HasRotation {
		st.Prefs.Rotation = ((cfg.Rotation % 360) + 360) % 360
		if st.Prefs.Rotation%90 != 0 {
			st.Prefs.Rotation = 0
		}
	}
	if cfg.HasFull {
		st.Prefs.Fullscreen = cfg.Fullscreen
	}
	if cfg.LibraryDir != "" {
		if abs, err := filepath.Abs(cfg.LibraryDir); err == nil {
			st.Prefs.LibraryDir = abs
		}
	}
	if st.Prefs.LibraryDir == "" {
		st.Prefs.LibraryDir, _ = os.UserHomeDir()
	}

	// Default to XO-1's native 1200x900 (clamped automatically to actual screen size)
	win, err := x11.OpenWindow("XO-1 Reader", 1200, 900, st.Prefs.Fullscreen)
	if err != nil {
		return nil, err
	}

	app := &App{
		win:    win,
		fm:     fm,
		store:  st,
		cache:  make(map[int]*layout.ChapterLayout),
		libDir: st.Prefs.LibraryDir,
	}

	// Determine what to open on startup
	bookToOpen := cfg.InitialBook
	if bookToOpen == "" && st.LastBook != "" {
		if _, err := os.Stat(st.LastBook); err == nil {
			bookToOpen = st.LastBook
		}
	}

	if bookToOpen != "" {
		if err := app.openBook(bookToOpen); err != nil {
			app.toast = "Could not open book: " + err.Error()
			app.openLibrary(app.libDir)
		} else {
			app.view = viewReader
		}
	} else {
		app.openLibrary(app.libDir)
	}

	return app, nil
}

// Run executes the blocking X11 event loop until the user quits or closes the window.
func (a *App) Run() error {
	defer a.Close()

	a.render()
	for !a.quit {
		ev, err := a.win.NextEvent()
		if err != nil {
			return nil // X11 connection closed cleanly
		}
		a.toast = "" // clear transient toast on next interaction
		dirty := false

		switch ev.Type {
		case x11.EventClose:
			a.quit = true

		case x11.EventExpose:
			dirty = true

		case x11.EventResize:
			a.relayoutPreservingPosition()
			dirty = true

		case x11.EventKeyPress:
			sym := x11.RotateDPadKeySym(ev.KeySym, a.store.Prefs.Rotation)
			dirty = a.handleKey(sym, ev.Mod)

		case x11.EventButtonPress:
			lx, ly := x11.PhysicalToLogicalPoint(ev.X, ev.Y, a.win.Width, a.win.Height, a.store.Prefs.Rotation)
			dirty = a.handleMouse(ev.Button, lx, ly)
		}

		if dirty && !a.quit {
			a.render()
		}
	}
	return nil
}

// Close saves persistent reading state and closes resources.
func (a *App) Close() {
	a.saveProgress()
	_ = a.store.Save()
	if a.book != nil {
		_ = a.book.Close()
		a.book = nil
	}
	if a.win != nil {
		_ = a.win.Close()
	}
}

func (a *App) logicalSize() (int, int) {
	return x11.LogicalSize(a.win.Width, a.win.Height, a.store.Prefs.Rotation)
}

func (a *App) bodyFamily() fonts.Family {
	if a.store.Prefs.FontFamily == "sans" {
		return fonts.FamilySans
	}
	return fonts.FamilySerif
}

func (a *App) openBook(path string) error {
	absPath, err := filepath.Abs(path)
	if err == nil {
		path = absPath
	}
	b, err := epub.Open(path)
	if err != nil {
		return err
	}
	if a.book != nil {
		a.saveProgress()
		_ = a.book.Close()
	}
	a.book = b
	a.cache = make(map[int]*layout.ChapterLayout)
	a.win.SetTitle(b.Meta.Title + " — XO-1 Reader")

	// Restore saved position if available
	bs := a.store.Book(b.Meta.ID, path, b.Meta.Title)
	ch := bs.ChapterIndex
	if ch < 0 || ch >= len(b.Spine) {
		ch = 0
	}
	a.chapterIdx = ch
	if err := a.loadChapterLayout(ch); err != nil {
		return err
	}
	a.pageIdx = a.curLayout.PageForOffset(bs.CharOffset)
	a.saveProgress()
	_ = a.store.Save()
	return nil
}

func (a *App) loadChapterLayout(ch int) error {
	if a.book == nil {
		return nil
	}
	if cl, ok := a.cache[ch]; ok {
		a.curLayout = cl
		return nil
	}
	blocks, err := a.book.LoadChapter(ch)
	if err != nil {
		return err
	}
	w, h := a.logicalSize()
	mL, mR, mT, mB, _, _ := a.readerMetrics(w, h)
	cl := layout.Paginate(blocks, a.fm, layout.Options{
		PageWidth:    w,
		PageHeight:   h,
		MarginLeft:   mL,
		MarginRight:  mR,
		MarginTop:    mT,
		MarginBottom: mB,
		BaseFontSize: a.store.Prefs.FontSize,
		FontFamily:   a.bodyFamily(),
	})
	// Keep cache small (max 4 chapters) to stay light on XO-1's 256 MB RAM
	if len(a.cache) >= 4 {
		a.cache = make(map[int]*layout.ChapterLayout)
	}
	a.cache[ch] = cl
	a.curLayout = cl
	return nil
}

func (a *App) relayoutPreservingPosition() {
	if a.book == nil {
		return
	}
	curOffset := a.currentCharOffset()
	a.cache = make(map[int]*layout.ChapterLayout)
	if err := a.loadChapterLayout(a.chapterIdx); err == nil {
		a.pageIdx = a.curLayout.PageForOffset(curOffset)
	}
}

func (a *App) currentCharOffset() int {
	if a.curLayout == nil || a.pageIdx < 0 || a.pageIdx >= len(a.curLayout.Pages) {
		return 0
	}
	return a.curLayout.Pages[a.pageIdx].StartOffset
}

func (a *App) saveProgress() {
	if a.book == nil {
		return
	}
	a.store.UpdatePosition(a.book.Meta.ID, a.book.Path, a.book.Meta.Title, a.chapterIdx, a.currentCharOffset())
}

func (a *App) overallProgressPercent() int {
	if a.book == nil || len(a.book.Spine) == 0 {
		return 0
	}
	totalCh := len(a.book.Spine)
	pages := 1
	if a.curLayout != nil && len(a.curLayout.Pages) > 0 {
		pages = len(a.curLayout.Pages)
	}
	// Pure integer percentage across chapters + current chapter pages
	num := a.chapterIdx*1000 + ((a.pageIdx+1)*1000)/pages
	pct := (num * 100) / (totalCh * 1000)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct
}

func (a *App) nextPage() {
	if a.book == nil || a.curLayout == nil {
		return
	}
	if a.pageIdx+1 < len(a.curLayout.Pages) {
		a.pageIdx++
	} else if a.chapterIdx+1 < len(a.book.Spine) {
		a.chapterIdx++
		if err := a.loadChapterLayout(a.chapterIdx); err == nil {
			a.pageIdx = 0
		}
	} else {
		a.toast = "End of book"
		return
	}
	a.saveProgress()
	_ = a.store.Save()
}

func (a *App) prevPage() {
	if a.book == nil || a.curLayout == nil {
		return
	}
	if a.pageIdx > 0 {
		a.pageIdx--
	} else if a.chapterIdx > 0 {
		a.chapterIdx--
		if err := a.loadChapterLayout(a.chapterIdx); err == nil {
			a.pageIdx = len(a.curLayout.Pages) - 1
			if a.pageIdx < 0 {
				a.pageIdx = 0
			}
		}
	} else {
		a.toast = "Beginning of book"
		return
	}
	a.saveProgress()
	_ = a.store.Save()
}

func (a *App) jumpChapter(ch int, fragment string) {
	if a.book == nil || ch < 0 || ch >= len(a.book.Spine) {
		return
	}
	a.chapterIdx = ch
	if err := a.loadChapterLayout(ch); err != nil {
		a.toast = "Error loading chapter"
		return
	}
	a.pageIdx = 0
	if fragment != "" {
		if pg, ok := a.curLayout.Anchors[fragment]; ok {
			a.pageIdx = pg
		}
	}
	a.saveProgress()
	_ = a.store.Save()
}

func (a *App) toggleCurrentBookmark() {
	if a.book == nil || a.curLayout == nil || a.pageIdx >= len(a.curLayout.Pages) {
		return
	}
	pg := a.curLayout.Pages[a.pageIdx]
	label := pg.Preview
	if label == "" {
		label = a.book.ChapterTitle(a.chapterIdx)
	}
	added := a.store.ToggleBookmark(a.book.Meta.ID, a.book.Path, a.book.Meta.Title, a.chapterIdx, pg.StartOffset, label)
	_ = a.store.Save()
	if added {
		a.toast = "Bookmark saved"
	} else {
		a.toast = "Bookmark removed"
	}
}

func (a *App) adjustFontSize(delta int) {
	ns := a.store.Prefs.FontSize + delta
	if ns < 16 {
		ns = 16
	}
	if ns > 52 {
		ns = 52
	}
	if ns == a.store.Prefs.FontSize {
		return
	}
	a.store.Prefs.FontSize = ns
	a.relayoutPreservingPosition()
	_ = a.store.Save()
	a.toast = fmt.Sprintf("Font size: %d px", ns)
}

func (a *App) toggleFontFamily() {
	if a.store.Prefs.FontFamily == "serif" {
		a.store.Prefs.FontFamily = "sans"
		a.toast = "Font: Sans-Serif"
	} else {
		a.store.Prefs.FontFamily = "serif"
		a.toast = "Font: Liberation Serif"
	}
	a.relayoutPreservingPosition()
	_ = a.store.Save()
}

func (a *App) cycleTheme(dir int) {
	if dir >= 0 {
		a.store.Prefs.Theme = nextThemeName(a.store.Prefs.Theme)
	} else {
		a.store.Prefs.Theme = prevThemeName(a.store.Prefs.Theme)
	}
	_ = a.store.Save()
	a.toast = "Theme: " + themeByName(a.store.Prefs.Theme).Label
}

func (a *App) rotateScreen(delta int) {
	a.store.Prefs.Rotation = ((a.store.Prefs.Rotation+delta)%360 + 360) % 360
	a.relayoutPreservingPosition()
	_ = a.store.Save()
	a.toast = fmt.Sprintf("Rotation: %d°", a.store.Prefs.Rotation)
}

func (a *App) toggleFullscreen() {
	a.store.Prefs.Fullscreen = !a.store.Prefs.Fullscreen
	a.win.SetFullscreen(a.store.Prefs.Fullscreen)
	_ = a.store.Save()
	if a.store.Prefs.Fullscreen {
		a.toast = "Fullscreen: On"
	} else {
		a.toast = "Fullscreen: Off"
	}
}

func (a *App) openLibrary(dir string) {
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}
	a.libDir = dir
	a.store.Prefs.LibraryDir = dir
	a.libItems = nil
	a.libSel = 0
	a.libScroll = 0

	// Parent directory entry (unless at filesystem root)
	parent := filepath.Dir(dir)
	if parent != dir {
		a.libItems = append(a.libItems, libraryItem{
			isDir: true,
			path:  parent,
			title: ".. (Parent Folder)",
		})
	}

	entries, err := os.ReadDir(dir)
	if err == nil {
		var dirs []libraryItem
		var books []libraryItem
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			full := filepath.Join(dir, name)
			if e.IsDir() {
				dirs = append(dirs, libraryItem{
					isDir: true,
					path:  full,
					title: name,
				})
				continue
			}
			if strings.EqualFold(filepath.Ext(name), ".epub") {
				info, _ := e.Info()
				var sz int64
				if info != nil {
					sz = info.Size()
				}
				title := strings.TrimSuffix(name, filepath.Ext(name))
				author := ""
				if b, err := epub.Open(full); err == nil {
					if b.Meta.Title != "" {
						title = b.Meta.Title
					}
					author = b.Meta.Author
					_ = b.Close()
				}
				books = append(books, libraryItem{
					isDir:     false,
					path:      full,
					title:     title,
					author:    author,
					sizeBytes: sz,
				})
			}
		}
		sort.Slice(books, func(i, j int) bool {
			return strings.ToLower(books[i].title) < strings.ToLower(books[j].title)
		})
		sort.Slice(dirs, func(i, j int) bool {
			return strings.ToLower(dirs[i].title) < strings.ToLower(dirs[j].title)
		})
		// Show EPUB books first so the user lands right on readable books, then subfolders
		a.libItems = append(a.libItems, books...)
		a.libItems = append(a.libItems, dirs...)
		// If there is a book right after "..", pre-select the first book!
		if len(books) > 0 && parent != dir {
			a.libSel = 1
		}
	}
	a.view = viewLibrary
}

func (a *App) openTOC() {
	if a.book == nil {
		return
	}
	a.tocSel = 0
	for i, e := range a.book.TOC {
		if e.ChapterIndex == a.chapterIdx {
			a.tocSel = i
			break
		}
	}
	a.tocScroll = 0
	a.view = viewTOC
}

func (a *App) openBookmarks() {
	if a.book == nil {
		return
	}
	a.bmSel = 0
	a.bmScroll = 0
	a.view = viewBookmarks
}

func (a *App) bookmarkMenuItems() []bookmarkListItem {
	items := []bookmarkListItem{{
		isAddAction: true,
		title:       "+ Toggle Bookmark on Current Page",
		subtitle:    fmt.Sprintf("Ch %d · Page %d", a.chapterIdx+1, a.pageIdx+1),
	}}
	if a.book == nil {
		return items
	}
	bs, ok := a.store.Books[a.book.Meta.ID]
	if !ok {
		return items
	}
	for i, bm := range bs.Bookmarks {
		chTitle := a.book.ChapterTitle(bm.ChapterIndex)
		items = append(items, bookmarkListItem{
			bmIndex:  i,
			title:    bm.Label,
			subtitle: fmt.Sprintf("Ch %d: %s  ·  %s", bm.ChapterIndex+1, chTitle, bm.CreatedAt.Format("Jan 2 15:04")),
		})
	}
	return items
}

func (a *App) menuItems() []menuItem {
	bmCount := 0
	if a.book != nil {
		if bs, ok := a.store.Books[a.book.Meta.ID]; ok {
			bmCount = len(bs.Bookmarks)
		}
	}
	famLabel := "Serif"
	if a.store.Prefs.FontFamily == "sans" {
		famLabel = "Sans"
	}
	fullLabel := "Off"
	if a.store.Prefs.Fullscreen {
		fullLabel = "On"
	}

	items := make([]menuItem, 0, 12)
	if a.book != nil {
		items = append(items,
			menuItem{id: "resume", label: "Resume Reading"},
			menuItem{id: "toc", label: "Table of Contents"},
			menuItem{id: "bookmarks", label: fmt.Sprintf("Bookmarks (%d)", bmCount)},
			menuItem{id: "toggle_bm", label: "Toggle Bookmark Here"},
		)
	}
	items = append(items,
		menuItem{id: "library", label: "Open Library..."},
		menuItem{id: "font_size", label: "Font Size", value: fmt.Sprintf("< %d px >", a.store.Prefs.FontSize)},
		menuItem{id: "font_family", label: "Font Family", value: fmt.Sprintf("< %s >", famLabel)},
		menuItem{id: "theme", label: "Theme", value: fmt.Sprintf("< %s >", themeByName(a.store.Prefs.Theme).Label)},
		menuItem{id: "rotate", label: "Screen Rotation", value: fmt.Sprintf("< %d° >", a.store.Prefs.Rotation)},
		menuItem{id: "fullscreen", label: "Fullscreen", value: fmt.Sprintf("< %s >", fullLabel)},
		menuItem{id: "help", label: "Controls & Help"},
		menuItem{id: "quit", label: "Quit"},
	)
	return items
}

func (a *App) handleKey(sym uint32, mod uint16) bool {
	// Global Ctrl+C / Ctrl+Q quit
	if (mod&x11.ModControl) != 0 && (sym == 'c' || sym == 'C' || sym == 'q' || sym == 'Q') {
		a.quit = true
		return false
	}
	// Global hardware rotate key & F11 fullscreen
	if sym == x11.KeyXF86RotateWindows {
		a.rotateScreen(90)
		return true
	}
	if sym == x11.KeyF11 {
		a.toggleFullscreen()
		return true
	}

	switch a.view {
	case viewReader:
		return a.handleReaderKey(sym, mod)
	case viewMenu:
		return a.handleMenuKey(sym)
	case viewLibrary:
		return a.handleLibraryKey(sym)
	case viewTOC:
		return a.handleTOCKey(sym)
	case viewBookmarks:
		return a.handleBookmarksKey(sym)
	case viewHelp:
		a.view = a.prevView
		return true
	}
	return false
}

func (a *App) handleReaderKey(sym uint32, mod uint16) bool {
	switch sym {
	case x11.KeyRight, x11.KeyDown, x11.KeyPageDown, x11.KeySpace,
		x11.KeyKPRight, x11.KeyKPDown, x11.KeyKPPageDown, 'j', 'n':
		if (mod&x11.ModShift) != 0 && a.book != nil && a.chapterIdx+1 < len(a.book.Spine) {
			a.jumpChapter(a.chapterIdx+1, "")
			return true
		}
		a.nextPage()
		return true

	case x11.KeyLeft, x11.KeyUp, x11.KeyPageUp, x11.KeyBackSpace,
		x11.KeyKPLeft, x11.KeyKPUp, x11.KeyKPPageUp, 'k', 'p':
		if (mod&x11.ModShift) != 0 && a.book != nil && a.chapterIdx > 0 {
			a.jumpChapter(a.chapterIdx-1, "")
			return true
		}
		a.prevPage()
		return true

	case ']':
		if a.book != nil && a.chapterIdx+1 < len(a.book.Spine) {
			a.jumpChapter(a.chapterIdx+1, "")
			return true
		}
	case '[':
		if a.book != nil && a.chapterIdx > 0 {
			a.jumpChapter(a.chapterIdx-1, "")
			return true
		}

	case x11.KeyKPHome, x11.KeyEscape, x11.KeyTab, 'm', 'M':
		a.menuSel = 0
		a.view = viewMenu
		return true

	case x11.KeyKPEnd, 'b', 'B':
		a.toggleCurrentBookmark()
		return true

	case 'c', 'C':
		a.openTOC()
		return true

	case 'l', 'L', 'o', 'O':
		a.openLibrary(a.libDir)
		return true

	case '+', '=', x11.KeyKPAdd:
		a.adjustFontSize(2)
		return true

	case '-', '_', x11.KeyKPSubtract:
		a.adjustFontSize(-2)
		return true

	case 'g', 'G':
		a.toggleFontFamily()
		return true

	case 't', 'T':
		a.cycleTheme(1)
		return true

	case 'r', 'R':
		a.rotateScreen(90)
		return true

	case 'f', 'F':
		a.toggleFullscreen()
		return true

	case '?', 'h', 'H', x11.KeyF1:
		a.prevView = viewReader
		a.view = viewHelp
		return true

	case 'q', 'Q':
		a.quit = true
		return false
	}
	return false
}

func (a *App) handleMenuKey(sym uint32) bool {
	items := a.menuItems()
	if len(items) == 0 {
		return false
	}
	if a.menuSel >= len(items) {
		a.menuSel = len(items) - 1
	}

	switch sym {
	case x11.KeyUp, x11.KeyKPUp, 'k':
		a.menuSel = (a.menuSel + len(items) - 1) % len(items)
		return true
	case x11.KeyDown, x11.KeyKPDown, 'j':
		a.menuSel = (a.menuSel + 1) % len(items)
		return true
	case x11.KeyLeft, x11.KeyKPLeft:
		return a.activateMenuItem(items[a.menuSel].id, -1)
	case x11.KeyRight, x11.KeyKPRight:
		return a.activateMenuItem(items[a.menuSel].id, 1)
	case x11.KeyReturn, x11.KeyKPEnter, x11.KeyKPEnd, x11.KeySpace:
		return a.activateMenuItem(items[a.menuSel].id, 0)
	case x11.KeyEscape, x11.KeyKPHome, x11.KeyBackSpace, 'm', 'M':
		if a.book != nil {
			a.view = viewReader
		} else {
			a.view = viewLibrary
		}
		return true
	case 'q', 'Q':
		a.quit = true
		return false
	}
	return false
}

func (a *App) activateMenuItem(id string, dir int) bool {
	switch id {
	case "resume":
		if dir == 0 {
			a.view = viewReader
			return true
		}
	case "toc":
		if dir == 0 {
			a.openTOC()
			return true
		}
	case "bookmarks":
		if dir == 0 {
			a.openBookmarks()
			return true
		}
	case "toggle_bm":
		if dir == 0 {
			a.toggleCurrentBookmark()
			a.view = viewReader
			return true
		}
	case "library":
		if dir == 0 {
			a.openLibrary(a.libDir)
			return true
		}
	case "font_size":
		if dir < 0 {
			a.adjustFontSize(-2)
		} else {
			a.adjustFontSize(2)
		}
		return true
	case "font_family":
		a.toggleFontFamily()
		return true
	case "theme":
		if dir < 0 {
			a.cycleTheme(-1)
		} else {
			a.cycleTheme(1)
		}
		return true
	case "rotate":
		if dir < 0 {
			a.rotateScreen(-90)
		} else {
			a.rotateScreen(90)
		}
		return true
	case "fullscreen":
		a.toggleFullscreen()
		return true
	case "help":
		if dir == 0 {
			a.prevView = viewMenu
			a.view = viewHelp
			return true
		}
	case "quit":
		if dir == 0 {
			a.quit = true
			return false
		}
	}
	return false
}

func (a *App) handleLibraryKey(sym uint32) bool {
	n := len(a.libItems)
	switch sym {
	case x11.KeyUp, x11.KeyKPUp, 'k':
		if n > 0 && a.libSel > 0 {
			a.libSel--
			return true
		}
	case x11.KeyDown, x11.KeyKPDown, 'j':
		if n > 0 && a.libSel+1 < n {
			a.libSel++
			return true
		}
	case x11.KeyPageUp, x11.KeyKPPageUp:
		if n > 0 {
			a.libSel -= 8
			if a.libSel < 0 {
				a.libSel = 0
			}
			return true
		}
	case x11.KeyPageDown, x11.KeyKPPageDown:
		if n > 0 {
			a.libSel += 8
			if a.libSel >= n {
				a.libSel = n - 1
			}
			return true
		}
	case x11.KeyReturn, x11.KeyKPEnter, x11.KeyKPEnd, x11.KeySpace, x11.KeyRight, x11.KeyKPRight:
		if n > 0 && a.libSel >= 0 && a.libSel < n {
			it := a.libItems[a.libSel]
			if it.isDir {
				a.openLibrary(it.path)
				return true
			}
			if err := a.openBook(it.path); err != nil {
				a.toast = "Failed to open EPUB: " + err.Error()
				return true
			}
			a.view = viewReader
			return true
		}
	case x11.KeyBackSpace, x11.KeyLeft, x11.KeyKPLeft:
		parent := filepath.Dir(a.libDir)
		if parent != a.libDir {
			a.openLibrary(parent)
			return true
		}
	case x11.KeyEscape, x11.KeyKPHome:
		if a.book != nil {
			a.view = viewReader
			return true
		}
		a.menuSel = 0
		a.view = viewMenu
		return true
	case 'r', 'R':
		a.rotateScreen(90)
		return true
	case 't', 'T':
		a.cycleTheme(1)
		return true
	case 'q', 'Q':
		a.quit = true
		return false
	}
	return false
}

func (a *App) handleTOCKey(sym uint32) bool {
	if a.book == nil {
		a.view = viewLibrary
		return true
	}
	n := len(a.book.TOC)
	switch sym {
	case x11.KeyUp, x11.KeyKPUp, 'k':
		if a.tocSel > 0 {
			a.tocSel--
			return true
		}
	case x11.KeyDown, x11.KeyKPDown, 'j':
		if a.tocSel+1 < n {
			a.tocSel++
			return true
		}
	case x11.KeyPageUp, x11.KeyKPPageUp:
		a.tocSel -= 10
		if a.tocSel < 0 {
			a.tocSel = 0
		}
		return true
	case x11.KeyPageDown, x11.KeyKPPageDown:
		a.tocSel += 10
		if a.tocSel >= n {
			a.tocSel = n - 1
		}
		return true
	case x11.KeyReturn, x11.KeyKPEnter, x11.KeyKPEnd, x11.KeySpace:
		if a.tocSel >= 0 && a.tocSel < n {
			entry := a.book.TOC[a.tocSel]
			a.jumpChapter(entry.ChapterIndex, entry.Fragment)
			a.view = viewReader
			return true
		}
	case x11.KeyEscape, x11.KeyKPHome, x11.KeyBackSpace, 'c', 'C':
		a.view = viewReader
		return true
	case 'q', 'Q':
		a.quit = true
		return false
	}
	return false
}

func (a *App) handleBookmarksKey(sym uint32) bool {
	items := a.bookmarkMenuItems()
	n := len(items)
	if a.bmSel >= n {
		a.bmSel = n - 1
	}
	switch sym {
	case x11.KeyUp, x11.KeyKPUp, 'k':
		if a.bmSel > 0 {
			a.bmSel--
			return true
		}
	case x11.KeyDown, x11.KeyKPDown, 'j':
		if a.bmSel+1 < n {
			a.bmSel++
			return true
		}
	case x11.KeyReturn, x11.KeyKPEnter, x11.KeyKPEnd, x11.KeySpace:
		if a.bmSel >= 0 && a.bmSel < n {
			it := items[a.bmSel]
			if it.isAddAction {
				a.toggleCurrentBookmark()
				return true
			}
			bs := a.store.Books[a.book.Meta.ID]
			if bs != nil && it.bmIndex >= 0 && it.bmIndex < len(bs.Bookmarks) {
				bm := bs.Bookmarks[it.bmIndex]
				a.chapterIdx = bm.ChapterIndex
				if err := a.loadChapterLayout(a.chapterIdx); err == nil {
					a.pageIdx = a.curLayout.PageForOffset(bm.CharOffset)
				}
				a.saveProgress()
				_ = a.store.Save()
				a.view = viewReader
				return true
			}
		}
	case 'd', 'D', x11.KeyBackSpace:
		if a.bmSel > 0 && a.bmSel < n && a.book != nil {
			it := items[a.bmSel]
			bs := a.store.Books[a.book.Meta.ID]
			if bs != nil && it.bmIndex >= 0 && it.bmIndex < len(bs.Bookmarks) {
				bs.Bookmarks = append(bs.Bookmarks[:it.bmIndex], bs.Bookmarks[it.bmIndex+1:]...)
				_ = a.store.Save()
				if a.bmSel >= len(bs.Bookmarks)+1 {
					a.bmSel = len(bs.Bookmarks)
				}
				a.toast = "Bookmark deleted"
				return true
			}
		}
	case x11.KeyEscape, x11.KeyKPHome:
		a.view = viewReader
		return true
	case 'q', 'Q':
		a.quit = true
		return false
	}
	return false
}

func (a *App) handleMouse(btn uint8, lx, ly int) bool {
	// Mouse wheel up/down
	if btn == 4 {
		return a.handleKey(x11.KeyUp, 0)
	}
	if btn == 5 {
		return a.handleKey(x11.KeyDown, 0)
	}
	if btn != 1 {
		return false
	}

	w, _ := a.logicalSize()
	switch a.view {
	case viewReader:
		// Click top header bar opens menu; left 35% previous page; right 65% next page
		if ly < 52 {
			a.menuSel = 0
			a.view = viewMenu
			return true
		}
		if lx < (w*35)/100 {
			a.prevPage()
		} else {
			a.nextPage()
		}
		return true
	case viewHelp:
		a.view = a.prevView
		return true
	}
	return false
}
