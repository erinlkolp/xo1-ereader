package state

import (
	"path/filepath"
	"testing"
)

func TestStatePersistenceAndBookmarks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	st := Load(p)
	st.Prefs.FontSize = 32
	st.Prefs.Theme = "paper"
	st.Prefs.Rotation = 90

	st.UpdatePosition("book-1", "/home/olpc/Books/test.epub", "Test Book", 3, 1450)
	if added := st.ToggleBookmark("book-1", "/home/olpc/Books/test.epub", "Test Book", 3, 1450, "Opening line"); !added {
		t.Fatal("expected bookmark to be added")
	}
	if !st.HasBookmarkNear("book-1", 3, 1400, 1500) {
		t.Fatal("expected bookmark near offset 1450")
	}

	if err := st.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	reloaded := Load(p)
	if reloaded.Prefs.FontSize != 32 || reloaded.Prefs.Theme != "paper" || reloaded.Prefs.Rotation != 90 {
		t.Fatalf("prefs mismatch after reload: %+v", reloaded.Prefs)
	}
	bs := reloaded.Books["book-1"]
	if bs == nil || bs.ChapterIndex != 3 || bs.CharOffset != 1450 || len(bs.Bookmarks) != 1 {
		t.Fatalf("book state mismatch after reload: %+v", bs)
	}

	// Toggling near the same offset removes the bookmark
	if added := reloaded.ToggleBookmark("book-1", bs.Path, bs.Title, 3, 1480, "Opening line"); added {
		t.Fatal("expected nearby toggle to remove bookmark")
	}
	if len(reloaded.Books["book-1"].Bookmarks) != 0 {
		t.Fatal("expected 0 bookmarks after removal")
	}
}
