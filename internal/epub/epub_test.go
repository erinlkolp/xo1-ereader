package epub

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"xo1-ereader/internal/fonts"
)

func buildTestEPUB(t *testing.T, epub3 bool) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "sample.epub")

	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	writeEntry := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	writeEntry("mimetype", "application/epub+zip")
	writeEntry("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`)

	if epub3 {
		writeEntry("OEBPS/content.opf", `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>XO-1 Field Guide</dc:title>
    <dc:creator>Ada Lovelace</dc:creator>
    <dc:identifier>urn:uuid:xo1-epub3-test</dc:identifier>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ch1" href="text/ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="text/ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>`)
		writeEntry("OEBPS/nav.xhtml", `<!DOCTYPE html>
<html><body>
<nav epub:type="toc">
  <ol>
    <li><a href="text/ch1.xhtml">Chapter One</a>
      <ol><li><a href="text/ch1.xhtml#sec-notes">Field Notes</a></li></ol>
    </li>
    <li><a href="text/ch2.xhtml">Chapter Two</a></li>
  </ol>
</nav>
</body></html>`)
	} else {
		writeEntry("OEBPS/content.opf", `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>XO-1 Classic</dc:title>
    <dc:creator>Grace Hopper</dc:creator>
    <dc:identifier>urn:uuid:xo1-epub2-test</dc:identifier>
  </metadata>
  <manifest>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="ch1" href="text/ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="text/ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine toc="ncx">
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>`)
		writeEntry("OEBPS/toc.ncx", `<?xml version="1.0"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <navMap>
    <navPoint id="n1" playOrder="1">
      <navLabel><text>Chapter One</text></navLabel>
      <content src="text/ch1.xhtml"/>
      <navPoint id="n1a" playOrder="2">
        <navLabel><text>Field Notes</text></navLabel>
        <content src="text/ch1.xhtml#sec-notes"/>
      </navPoint>
    </navPoint>
    <navPoint id="n2" playOrder="3">
      <navLabel><text>Chapter Two</text></navLabel>
      <content src="text/ch2.xhtml"/>
    </navPoint>
  </navMap>
</ncx>`)
	}

	writeEntry("OEBPS/text/ch1.xhtml", `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter One</title><style>p { color: red; }</style></head>
<body>
  <h1>Chapter One</h1>
  <p>Hello <b>bold</b> and <i>italic</i> and <b><i>both</i></b> world.</p>
  <blockquote id="sec-notes"><p>Sunlight readable displays are wonderful.</p></blockquote>
  <ul><li>First item</li><li>Second item</li></ul>
  <pre>CGO_ENABLED=0 GOARCH=386 GO386=softfloat</pre>
  <hr/>
</body>
</html>`)

	writeEntry("OEBPS/text/ch2.xhtml", `<html><body><h2>Chapter Two</h2><p>Second chapter text.</p></body></html>`)

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOpenEPUB2AndEPUB3(t *testing.T) {
	for _, epub3 := range []bool{false, true} {
		path := buildTestEPUB(t, epub3)
		b, err := Open(path)
		if err != nil {
			t.Fatalf("Open(epub3=%v) failed: %v", epub3, err)
		}
		defer b.Close()

		if len(b.Spine) != 2 {
			t.Fatalf("expected 2 spine items, got %d", len(b.Spine))
		}
		if len(b.TOC) != 3 {
			t.Fatalf("expected 3 TOC entries, got %d: %+v", len(b.TOC), b.TOC)
		}
		if b.TOC[1].Fragment != "sec-notes" || b.TOC[1].Depth != 1 {
			t.Fatalf("unexpected nested TOC entry: %+v", b.TOC[1])
		}

		blocks, err := b.LoadChapter(0)
		if err != nil {
			t.Fatalf("LoadChapter(0) failed: %v", err)
		}
		if len(blocks) < 6 {
			t.Fatalf("expected >=6 parsed blocks, got %d: %+v", len(blocks), blocks)
		}
		if blocks[0].Kind != BlockHeading || blocks[0].Level != 1 {
			t.Fatalf("expected h1 heading first, got %+v", blocks[0])
		}
		// Verify bold+italic span exists in paragraph
		foundBI := false
		for _, sp := range blocks[1].Spans {
			if sp.Style == fonts.StyleBoldItalic && sp.Text == "both" {
				foundBI = true
			}
		}
		if !foundBI {
			t.Fatalf("expected bold+italic span 'both' in %+v", blocks[1].Spans)
		}
		// Verify anchor on blockquote
		if blocks[2].Kind != BlockQuote || len(blocks[2].Anchors) == 0 || blocks[2].Anchors[0] != "sec-notes" {
			t.Fatalf("expected blockquote with anchor sec-notes, got %+v", blocks[2])
		}
	}
}
