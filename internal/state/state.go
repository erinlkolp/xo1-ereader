package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Bookmark records a saved location inside a book.
type Bookmark struct {
	ChapterIndex int       `json:"chapter_index"`
	CharOffset   int       `json:"char_offset"`
	Label        string    `json:"label"`
	CreatedAt    time.Time `json:"created_at"`
}

// BookState stores reading progress and bookmarks for a single book.
type BookState struct {
	Path         string     `json:"path"`
	Title        string     `json:"title"`
	ChapterIndex int        `json:"chapter_index"`
	CharOffset   int        `json:"char_offset"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Bookmarks    []Bookmark `json:"bookmarks,omitempty"`
}

// Prefs stores global reader display preferences.
type Prefs struct {
	FontSize   int    `json:"font_size"`   // base body size in px (default 28 for XO-1's 200 DPI screen)
	FontFamily string `json:"font_family"` // "serif" or "sans"
	Theme      string `json:"theme"`       // "paper", "sunlight", "night"
	Rotation   int    `json:"rotation"`    // 0, 90, 180, 270
	Fullscreen bool   `json:"fullscreen"`
	LibraryDir string `json:"library_dir"`
}

// Store holds all persistent application state.
type Store struct {
	Prefs    Prefs                 `json:"prefs"`
	LastBook string                `json:"last_book"`
	Books    map[string]*BookState `json:"books"`
	filePath string
}

// DefaultPath returns ~/.config/xo1-reader/state.json.
func DefaultPath() string {
	cfgDir, err := os.UserConfigDir()
	if err != nil || cfgDir == "" {
		home, _ := os.UserHomeDir()
		cfgDir = filepath.Join(home, ".config")
	}
	return filepath.Join(cfgDir, "xo1-reader", "state.json")
}

// DefaultPrefs returns sensible defaults tuned for the OLPC XO-1 (1200x900 @ 200 DPI).
func DefaultPrefs() Prefs {
	home, _ := os.UserHomeDir()
	return Prefs{
		FontSize:   28,
		FontFamily: "serif",
		Theme:      "sunlight",
		Rotation:   0,
		Fullscreen: false,
		LibraryDir: home,
	}
}

// Load reads state from path, returning defaults if the file does not yet exist.
func Load(path string) *Store {
	if path == "" {
		path = DefaultPath()
	}
	st := &Store{
		Prefs:    DefaultPrefs(),
		Books:    make(map[string]*BookState),
		filePath: path,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	if err := json.Unmarshal(data, st); err != nil {
		return st
	}
	if st.Books == nil {
		st.Books = make(map[string]*BookState)
	}
	// Clamp / validate preferences
	if st.Prefs.FontSize < 14 || st.Prefs.FontSize > 60 {
		st.Prefs.FontSize = 28
	}
	if st.Prefs.FontFamily != "serif" && st.Prefs.FontFamily != "sans" {
		st.Prefs.FontFamily = "serif"
	}
	if st.Prefs.Theme != "paper" && st.Prefs.Theme != "sunlight" && st.Prefs.Theme != "night" {
		st.Prefs.Theme = "sunlight"
	}
	st.Prefs.Rotation = ((st.Prefs.Rotation % 360) + 360) % 360
	if st.Prefs.Rotation%90 != 0 {
		st.Prefs.Rotation = 0
	}
	return st
}

// Save atomically writes state to disk.
func (s *Store) Save() error {
	if s.filePath == "" {
		s.filePath = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}

// Book returns (or creates) the BookState for bookID.
func (s *Store) Book(bookID, bookPath, title string) *BookState {
	bs, ok := s.Books[bookID]
	if !ok {
		bs = &BookState{
			Path:  bookPath,
			Title: title,
		}
		s.Books[bookID] = bs
	} else {
		if bookPath != "" {
			bs.Path = bookPath
		}
		if title != "" {
			bs.Title = title
		}
	}
	return bs
}

// UpdatePosition saves the current reading location for a book.
func (s *Store) UpdatePosition(bookID, bookPath, title string, chapterIdx, charOffset int) {
	bs := s.Book(bookID, bookPath, title)
	bs.ChapterIndex = chapterIdx
	bs.CharOffset = charOffset
	bs.UpdatedAt = time.Now()
	s.LastBook = bookPath
}

// ToggleBookmark adds a bookmark at (chapterIdx, charOffset) or removes an existing
// bookmark within +/- 120 chars of that position. Returns true if added, false if removed.
func (s *Store) ToggleBookmark(bookID, bookPath, title string, chapterIdx, charOffset int, label string) bool {
	bs := s.Book(bookID, bookPath, title)
	for i, bm := range bs.Bookmarks {
		if bm.ChapterIndex == chapterIdx {
			diff := bm.CharOffset - charOffset
			if diff < 0 {
				diff = -diff
			}
			if diff <= 120 {
				bs.Bookmarks = append(bs.Bookmarks[:i], bs.Bookmarks[i+1:]...)
				return false
			}
		}
	}
	bs.Bookmarks = append(bs.Bookmarks, Bookmark{
		ChapterIndex: chapterIdx,
		CharOffset:   charOffset,
		Label:        label,
		CreatedAt:    time.Now(),
	})
	return true
}

// HasBookmarkNear reports whether a bookmark exists within the given range in chapterIdx.
func (s *Store) HasBookmarkNear(bookID string, chapterIdx, startOffset, endOffset int) bool {
	bs, ok := s.Books[bookID]
	if !ok {
		return false
	}
	for _, bm := range bs.Bookmarks {
		if bm.ChapterIndex == chapterIdx && bm.CharOffset >= startOffset && bm.CharOffset <= endOffset {
			return true
		}
	}
	return false
}
