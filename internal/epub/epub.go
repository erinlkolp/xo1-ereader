package epub

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// Metadata holds basic descriptive info for an EPUB book.
type Metadata struct {
	ID       string
	Title    string
	Author   string
	Language string
}

// SpineItem represents one linear document in the book's reading order.
type SpineItem struct {
	ID   string
	Href string // normalized ZIP path
}

// TOCEntry represents a flattened table-of-contents item with nesting depth.
type TOCEntry struct {
	Title        string
	Href         string // normalized ZIP path (without fragment)
	Fragment     string // optional anchor ID (without '#')
	ChapterIndex int    // resolved index into Book.Spine (-1 if unresolved)
	Depth        int    // nesting level (0 = top-level)
}

// Book represents an open EPUB archive ready for lazy chapter loading.
type Book struct {
	Path      string
	Meta      Metadata
	Spine     []SpineItem
	TOC       []TOCEntry
	zipReader *zip.ReadCloser
	fileIndex map[string]*zip.File
}

// LibraryEntry holds lightweight metadata for the library file picker.
type LibraryEntry struct {
	Path   string
	Title  string
	Author string
	Size   int64
}

// Open opens an EPUB file and parses its container, OPF metadata, spine, and TOC.
// Individual chapter XHTML files are parsed lazily via LoadChapter to keep memory
// usage minimal on the OLPC XO-1.
func Open(epubPath string) (*Book, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, fmt.Errorf("open epub zip: %w", err)
	}

	b := &Book{
		Path:      epubPath,
		zipReader: zr,
		fileIndex: make(map[string]*zip.File, len(zr.File)),
	}
	for _, f := range zr.File {
		norm := normalizeZipPath(f.Name)
		b.fileIndex[norm] = f
	}

	if err := b.parsePackage(); err != nil {
		_ = zr.Close()
		return nil, err
	}
	return b, nil
}

// Close closes the underlying ZIP archive.
func (b *Book) Close() error {
	if b.zipReader != nil {
		return b.zipReader.Close()
	}
	return nil
}

// LoadChapter parses and returns the structured blocks for the given spine index.
func (b *Book) LoadChapter(index int) ([]Block, error) {
	if index < 0 || index >= len(b.Spine) {
		return nil, fmt.Errorf("chapter index %d out of range [0, %d)", index, len(b.Spine))
	}
	data, err := b.readZipFile(b.Spine[index].Href)
	if err != nil {
		return nil, fmt.Errorf("read chapter %q: %w", b.Spine[index].Href, err)
	}
	blocks, err := ParseXHTML(data)
	if err != nil {
		return nil, fmt.Errorf("parse chapter %q: %w", b.Spine[index].Href, err)
	}
	if len(blocks) == 0 {
		blocks = []Block{{
			Kind:  BlockParagraph,
			Spans: []Span{{Text: "(Empty chapter)"}},
		}}
	}
	return blocks, nil
}

// ChapterTitle returns the best human-readable title for the given spine index.
func (b *Book) ChapterTitle(index int) string {
	for _, entry := range b.TOC {
		if entry.ChapterIndex == index && entry.Title != "" {
			return entry.Title
		}
	}
	return fmt.Sprintf("Section %d of %d", index+1, len(b.Spine))
}

// ScanLibrary scans dir (non-recursively plus 1 level of subdirectories) for .epub files.
func ScanLibrary(dir string) ([]LibraryEntry, error) {
	var paths []string
	walkErr := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // ignore unreadable subdirs
		}
		if d.IsDir() {
			// Skip hidden directories and limit depth to 3 levels so scanning stays fast.
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(dir, p)
			if rel != "." && strings.Count(rel, string(os.PathSeparator)) >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".epub") {
			paths = append(paths, p)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	entries := make([]LibraryEntry, 0, len(paths))
	for _, p := range paths {
		info, _ := os.Stat(p)
		var sz int64
		if info != nil {
			sz = info.Size()
		}
		title := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		author := ""
		if b, err := Open(p); err == nil {
			if b.Meta.Title != "" {
				title = b.Meta.Title
			}
			author = b.Meta.Author
			_ = b.Close()
		}
		entries = append(entries, LibraryEntry{
			Path:   p,
			Title:  title,
			Author: author,
			Size:   sz,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Title) < strings.ToLower(entries[j].Title)
	})
	return entries, nil
}

func (b *Book) readZipFile(zipPath string) ([]byte, error) {
	norm := normalizeZipPath(zipPath)
	f, ok := b.fileIndex[norm]
	if !ok {
		// Case-insensitive fallback for messy EPUBs
		lower := strings.ToLower(norm)
		for k, candidate := range b.fileIndex {
			if strings.ToLower(k) == lower {
				f = candidate
				ok = true
				break
			}
		}
	}
	if !ok {
		return nil, fmt.Errorf("file not found in epub: %s", zipPath)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// Limit single file read to 16 MB for safety on 256 MB RAM XO-1
	return io.ReadAll(io.LimitReader(rc, 16<<20))
}

type containerXML struct {
	Rootfiles []struct {
		FullPath  string `xml:"full-path,attr"`
		MediaType string `xml:"media-type,attr"`
	} `xml:"rootfiles>rootfile"`
}

type opfPackage struct {
	Metadata struct {
		Titles      []string `xml:"title"`
		Creators    []string `xml:"creator"`
		Languages   []string `xml:"language"`
		Identifiers []string `xml:"identifier"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Toc      string `xml:"toc,attr"`
		Itemrefs []struct {
			IDRef  string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func (b *Book) parsePackage() error {
	// 1. Locate OPF via META-INF/container.xml
	opfPath := ""
	if data, err := b.readZipFile("META-INF/container.xml"); err == nil {
		var c containerXML
		if err := xml.Unmarshal(data, &c); err == nil {
			for _, rf := range c.Rootfiles {
				if rf.FullPath != "" {
					opfPath = normalizeZipPath(rf.FullPath)
					break
				}
			}
		}
	}
	// Fallback: search for any .opf file in archive
	if opfPath == "" {
		for k := range b.fileIndex {
			if strings.HasSuffix(strings.ToLower(k), ".opf") {
				opfPath = k
				break
			}
		}
	}
	if opfPath == "" {
		return errors.New("could not find OPF package file in epub")
	}

	opfData, err := b.readZipFile(opfPath)
	if err != nil {
		return fmt.Errorf("read opf %q: %w", opfPath, err)
	}

	var pkg opfPackage
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return fmt.Errorf("parse opf %q: %w", opfPath, err)
	}

	// Metadata
	if len(pkg.Metadata.Titles) > 0 {
		b.Meta.Title = strings.TrimSpace(pkg.Metadata.Titles[0])
	}
	if b.Meta.Title == "" {
		b.Meta.Title = strings.TrimSuffix(filepath.Base(b.Path), filepath.Ext(b.Path))
	}
	if len(pkg.Metadata.Creators) > 0 {
		authors := make([]string, 0, len(pkg.Metadata.Creators))
		for _, c := range pkg.Metadata.Creators {
			if t := strings.TrimSpace(c); t != "" {
				authors = append(authors, t)
			}
		}
		b.Meta.Author = strings.Join(authors, ", ")
	}
	if len(pkg.Metadata.Languages) > 0 {
		b.Meta.Language = strings.TrimSpace(pkg.Metadata.Languages[0])
	}
	if len(pkg.Metadata.Identifiers) > 0 && strings.TrimSpace(pkg.Metadata.Identifiers[0]) != "" {
		b.Meta.ID = strings.TrimSpace(pkg.Metadata.Identifiers[0])
	} else {
		h := sha256.Sum256([]byte(filepath.Base(b.Path) + "|" + b.Meta.Title))
		b.Meta.ID = hex.EncodeToString(h[:12])
	}

	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}

	type manifestItem struct {
		href       string
		mediaType  string
		properties string
	}
	manifest := make(map[string]manifestItem, len(pkg.Manifest.Items))
	var navHref, ncxHref string

	for _, it := range pkg.Manifest.Items {
		resolved := resolveZipPath(opfDir, it.Href)
		manifest[it.ID] = manifestItem{
			href:       resolved,
			mediaType:  strings.ToLower(strings.TrimSpace(it.MediaType)),
			properties: it.Properties,
		}
		for _, prop := range strings.Fields(it.Properties) {
			if prop == "nav" && navHref == "" {
				navHref = resolved
			}
		}
		if strings.Contains(strings.ToLower(it.MediaType), "dtbncx") && ncxHref == "" {
			ncxHref = resolved
		}
	}
	if pkg.Spine.Toc != "" {
		if it, ok := manifest[pkg.Spine.Toc]; ok {
			ncxHref = it.href
		}
	}

	// Build spine
	spineIndexByHref := make(map[string]int, len(pkg.Spine.Itemrefs))
	for _, ref := range pkg.Spine.Itemrefs {
		it, ok := manifest[ref.IDRef]
		if !ok {
			continue
		}
		idx := len(b.Spine)
		b.Spine = append(b.Spine, SpineItem{
			ID:   ref.IDRef,
			Href: it.href,
		})
		spineIndexByHref[strings.ToLower(it.href)] = idx
	}
	if len(b.Spine) == 0 {
		return errors.New("epub spine contains no readable chapters")
	}

	// Parse TOC: prefer EPUB3 nav, then EPUB2 NCX, then fallback to spine
	if navHref != "" {
		if entries, err := b.parseEPUB3Nav(navHref, spineIndexByHref); err == nil && len(entries) > 0 {
			b.TOC = entries
		}
	}
	if len(b.TOC) == 0 && ncxHref != "" {
		if entries, err := b.parseNCX(ncxHref, spineIndexByHref); err == nil && len(entries) > 0 {
			b.TOC = entries
		}
	}
	if len(b.TOC) == 0 {
		for i, sp := range b.Spine {
			b.TOC = append(b.TOC, TOCEntry{
				Title:        fmt.Sprintf("Chapter %d", i+1),
				Href:         sp.Href,
				ChapterIndex: i,
				Depth:        0,
			})
		}
	}
	return nil
}

type ncxDoc struct {
	NavMap []ncxNavPoint `xml:"navMap>navPoint"`
}

type ncxNavPoint struct {
	Label   string `xml:"navLabel>text"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Children []ncxNavPoint `xml:"navPoint"`
}

func (b *Book) parseNCX(ncxHref string, spineIndex map[string]int) ([]TOCEntry, error) {
	data, err := b.readZipFile(ncxHref)
	if err != nil {
		return nil, err
	}
	var doc ncxDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	baseDir := path.Dir(ncxHref)
	if baseDir == "." {
		baseDir = ""
	}
	var out []TOCEntry
	var walk func(points []ncxNavPoint, depth int)
	walk = func(points []ncxNavPoint, depth int) {
		for _, pt := range points {
			title := collapseSpace(pt.Label)
			href, frag := splitHref(pt.Content.Src)
			resolved := resolveZipPath(baseDir, href)
			chIdx := -1
			if idx, ok := spineIndex[strings.ToLower(resolved)]; ok {
				chIdx = idx
			}
			if title != "" && chIdx >= 0 {
				out = append(out, TOCEntry{
					Title:        title,
					Href:         resolved,
					Fragment:     frag,
					ChapterIndex: chIdx,
					Depth:        depth,
				})
			}
			if len(pt.Children) > 0 {
				walk(pt.Children, depth+1)
			}
		}
	}
	walk(doc.NavMap, 0)
	return out, nil
}

func (b *Book) parseEPUB3Nav(navHref string, spineIndex map[string]int) ([]TOCEntry, error) {
	data, err := b.readZipFile(navHref)
	if err != nil {
		return nil, err
	}
	root, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	baseDir := path.Dir(navHref)
	if baseDir == "." {
		baseDir = ""
	}

	// Find <nav> with epub:type="toc" (or first <nav>)
	var tocNav *html.Node
	var firstNav *html.Node
	var findNav func(n *html.Node)
	findNav = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "nav" {
			if firstNav == nil {
				firstNav = n
			}
			for _, a := range n.Attr {
				if (a.Key == "epub:type" || a.Key == "type" || a.Key == "role") &&
					strings.Contains(strings.ToLower(a.Val), "toc") {
					tocNav = n
					return
				}
			}
		}
		for c := n.FirstChild; c != nil && tocNav == nil; c = c.NextSibling {
			findNav(c)
		}
	}
	findNav(root)
	if tocNav == nil {
		tocNav = firstNav
	}
	if tocNav == nil {
		return nil, errors.New("no nav element found")
	}

	var out []TOCEntry
	var walkList func(n *html.Node, depth int)
	walkList = func(n *html.Node, depth int) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "ol", "ul":
				walkList(c, depth)
			case "li":
				var linkHref, linkText string
				for lc := c.FirstChild; lc != nil; lc = lc.NextSibling {
					if lc.Type == html.ElementNode && lc.Data == "a" {
						for _, a := range lc.Attr {
							if a.Key == "href" {
								linkHref = a.Val
							}
						}
						linkText = collapseSpace(nodeText(lc))
						break
					}
				}
				if linkText != "" && linkHref != "" {
					href, frag := splitHref(linkHref)
					resolved := resolveZipPath(baseDir, href)
					if idx, ok := spineIndex[strings.ToLower(resolved)]; ok {
						out = append(out, TOCEntry{
							Title:        linkText,
							Href:         resolved,
							Fragment:     frag,
							ChapterIndex: idx,
							Depth:        depth,
						})
					}
				}
				for lc := c.FirstChild; lc != nil; lc = lc.NextSibling {
					if lc.Type == html.ElementNode && (lc.Data == "ol" || lc.Data == "ul") {
						walkList(lc, depth+1)
					}
				}
			}
		}
	}
	walkList(tocNav, 0)
	return out, nil
}

func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur.Type == html.TextNode {
			sb.WriteString(cur.Data)
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func splitHref(raw string) (string, string) {
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		return raw[:i], raw[i+1:]
	}
	return raw, ""
}

func normalizeZipPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if decoded, err := url.PathUnescape(p); err == nil {
		p = decoded
	}
	p = path.Clean("/" + p)
	return strings.TrimPrefix(p, "/")
}

func resolveZipPath(baseDir, rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	if decoded, err := url.PathUnescape(rel); err == nil {
		rel = decoded
	}
	if strings.HasPrefix(rel, "/") {
		return normalizeZipPath(rel)
	}
	if baseDir == "" {
		return normalizeZipPath(rel)
	}
	return normalizeZipPath(path.Join(baseDir, rel))
}
