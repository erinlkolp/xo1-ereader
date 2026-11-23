package fonts

import (
	_ "embed"
	"image"
	"image/color"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed LiberationSerif-Regular.ttf
var serifRegularTTF []byte

//go:embed LiberationSerif-Bold.ttf
var serifBoldTTF []byte

//go:embed LiberationSerif-Italic.ttf
var serifItalicTTF []byte

//go:embed LiberationSerif-BoldItalic.ttf
var serifBoldItalicTTF []byte

// Family identifies a font family for body reading.
type Family string

const (
	FamilySerif Family = "serif"
	FamilySans  Family = "sans"
)

// Style is a bitmask of text styles.
type Style uint8

const (
	StyleRegular    Style = 0
	StyleBold       Style = 1 << 0
	StyleItalic     Style = 1 << 1
	StyleBoldItalic Style = StyleBold | StyleItalic
	StyleMono       Style = 1 << 2
)

type parsedFonts struct {
	serifRegular    *opentype.Font
	serifBold       *opentype.Font
	serifItalic     *opentype.Font
	serifBoldItalic *opentype.Font
	sansRegular     *opentype.Font
	sansBold        *opentype.Font
	sansItalic      *opentype.Font
	sansBoldItalic  *opentype.Font
	mono            *opentype.Font
}

var (
	initOnce sync.Once
	parsed   parsedFonts
	initErr  error
)

func ensureParsed() error {
	initOnce.Do(func() {
		var err error
		if parsed.serifRegular, err = opentype.Parse(serifRegularTTF); err != nil {
			initErr = err
			return
		}
		if parsed.serifBold, err = opentype.Parse(serifBoldTTF); err != nil {
			initErr = err
			return
		}
		if parsed.serifItalic, err = opentype.Parse(serifItalicTTF); err != nil {
			initErr = err
			return
		}
		if parsed.serifBoldItalic, err = opentype.Parse(serifBoldItalicTTF); err != nil {
			initErr = err
			return
		}
		if parsed.sansRegular, err = opentype.Parse(goregular.TTF); err != nil {
			initErr = err
			return
		}
		if parsed.sansBold, err = opentype.Parse(gobold.TTF); err != nil {
			initErr = err
			return
		}
		if parsed.sansItalic, err = opentype.Parse(goitalic.TTF); err != nil {
			initErr = err
			return
		}
		if parsed.sansBoldItalic, err = opentype.Parse(gobolditalic.TTF); err != nil {
			initErr = err
			return
		}
		if parsed.mono, err = opentype.Parse(gomono.TTF); err != nil {
			initErr = err
			return
		}
	})
	return initErr
}

type cachedGlyph struct {
	dr      image.Rectangle // offset relative to baseline dot (0,0)
	mask    *image.Alpha    // pre-rasterized alpha mask
	advance fixed.Int26_6
	valid   bool
}

// CachedFace wraps a font.Face with an in-memory glyph bitmap & advance cache.
// On the OLPC XO-1's 433 MHz Geode LX (with GO386=softfloat), caching rasterized
// alpha masks avoids repeated vector-outline rasterization and makes page turns
// nearly instantaneous.
type CachedFace struct {
	face    font.Face
	metrics font.Metrics
	glyphs  map[rune]cachedGlyph
	advOnly map[rune]fixed.Int26_6
}

func newCachedFace(otFont *opentype.Font, sizePx int) (*CachedFace, error) {
	if sizePx < 8 {
		sizePx = 8
	}
	f, err := opentype.NewFace(otFont, &opentype.FaceOptions{
		Size:    float64(sizePx),
		DPI:     72, // 1pt == 1px so sizePx is exact pixel height
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	return &CachedFace{
		face:    f,
		metrics: f.Metrics(),
		glyphs:  make(map[rune]cachedGlyph, 128),
		advOnly: make(map[rune]fixed.Int26_6, 128),
	}, nil
}

// Metrics returns the face's font metrics.
func (cf *CachedFace) Metrics() font.Metrics {
	return cf.metrics
}

// Ascent returns the integer ascent in pixels.
func (cf *CachedFace) Ascent() int {
	return cf.metrics.Ascent.Ceil()
}

// Descent returns the integer descent in pixels.
func (cf *CachedFace) Descent() int {
	return cf.metrics.Descent.Ceil()
}

// Height returns the natural line height in pixels.
func (cf *CachedFace) Height() int {
	return cf.metrics.Height.Ceil()
}

// Advance returns the cached advance width of rune r.
func (cf *CachedFace) Advance(r rune) fixed.Int26_6 {
	if adv, ok := cf.advOnly[r]; ok {
		return adv
	}
	adv, ok := cf.face.GlyphAdvance(r)
	if !ok {
		adv, _ = cf.face.GlyphAdvance('?')
	}
	cf.advOnly[r] = adv
	return adv
}

// MeasureString returns the fixed-point width of string s.
func (cf *CachedFace) MeasureString(s string) fixed.Int26_6 {
	var w fixed.Int26_6
	for _, r := range s {
		w += cf.Advance(r)
	}
	return w
}

// MeasureStringPx returns the pixel width (rounded up) of string s.
func (cf *CachedFace) MeasureStringPx(s string) int {
	return cf.MeasureString(s).Ceil()
}

func (cf *CachedFace) getGlyph(r rune) cachedGlyph {
	if g, ok := cf.glyphs[r]; ok {
		return g
	}
	// Cap cache size to bound RAM on the XO-1 (256 MB total).
	if len(cf.glyphs) > 1024 {
		cf.glyphs = make(map[rune]cachedGlyph, 128)
	}
	dr, mask, maskp, adv, ok := cf.face.Glyph(fixed.P(0, 0), r)
	if !ok {
		dr, mask, maskp, adv, ok = cf.face.Glyph(fixed.P(0, 0), '?')
	}
	var cg cachedGlyph
	cg.advance = adv
	cg.dr = dr
	cg.valid = ok
	if ok && mask != nil && !dr.Empty() {
		w, h := dr.Dx(), dr.Dy()
		alpha := image.NewAlpha(image.Rect(0, 0, w, h))
		if aMask, isAlpha := mask.(*image.Alpha); isAlpha {
			for y := 0; y < h; y++ {
				srcOff := aMask.PixOffset(maskp.X, maskp.Y+y)
				dstOff := y * alpha.Stride
				copy(alpha.Pix[dstOff:dstOff+w], aMask.Pix[srcOff:srcOff+w])
			}
		} else {
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					_, _, _, a := mask.At(maskp.X+x, maskp.Y+y).RGBA()
					alpha.Pix[y*alpha.Stride+x] = uint8(a >> 8)
				}
			}
		}
		cg.mask = alpha
	}
	cf.glyphs[r] = cg
	cf.advOnly[r] = adv
	return cg
}

// DrawString draws s onto dst starting at baseline point (x, y) using color col.
// If crispMono is true (for XO-1 sunlight monochrome mode), alpha is thresholded
// for razor-sharp 1-bit readability. Returns the new integer X coordinate.
func (cf *CachedFace) DrawString(dst *image.RGBA, x, y int, s string, col color.RGBA, crispMono bool) int {
	dotX := fixed.I(x)
	bounds := dst.Bounds()
	sr := uint32(col.R)
	sg := uint32(col.G)
	sb := uint32(col.B)

	for _, r := range s {
		cg := cf.getGlyph(r)
		if cg.mask != nil {
			gx := dotX.Round() + cg.dr.Min.X
			gy := y + cg.dr.Min.Y
			mw := cg.mask.Rect.Dx()
			mh := cg.mask.Rect.Dy()

			// Clip to dst bounds
			x0, y0 := 0, 0
			x1, y1 := mw, mh
			if gx < bounds.Min.X {
				x0 = bounds.Min.X - gx
			}
			if gy < bounds.Min.Y {
				y0 = bounds.Min.Y - gy
			}
			if gx+x1 > bounds.Max.X {
				x1 = bounds.Max.X - gx
			}
			if gy+y1 > bounds.Max.Y {
				y1 = bounds.Max.Y - gy
			}

			for my := y0; my < y1; my++ {
				mRow := my * cg.mask.Stride
				dRow := dst.PixOffset(gx+x0, gy+my)
				for mx := x0; mx < x1; mx++ {
					a := uint32(cg.mask.Pix[mRow+mx])
					if a == 0 {
						dRow += 4
						continue
					}
					if crispMono {
						if a >= 108 {
							dst.Pix[dRow] = uint8(sr)
							dst.Pix[dRow+1] = uint8(sg)
							dst.Pix[dRow+2] = uint8(sb)
							dst.Pix[dRow+3] = 255
						}
					} else if a == 255 {
						dst.Pix[dRow] = uint8(sr)
						dst.Pix[dRow+1] = uint8(sg)
						dst.Pix[dRow+2] = uint8(sb)
						dst.Pix[dRow+3] = 255
					} else {
						inv := 255 - a
						dst.Pix[dRow] = uint8((sr*a + uint32(dst.Pix[dRow])*inv + 127) / 255)
						dst.Pix[dRow+1] = uint8((sg*a + uint32(dst.Pix[dRow+1])*inv + 127) / 255)
						dst.Pix[dRow+2] = uint8((sb*a + uint32(dst.Pix[dRow+2])*inv + 127) / 255)
						dst.Pix[dRow+3] = 255
					}
					dRow += 4
				}
			}
		}
		dotX += cg.advance
	}
	return dotX.Round()
}

// Truncate truncates s with an ellipsis if it exceeds maxPx.
func (cf *CachedFace) Truncate(s string, maxPx int) string {
	if maxPx <= 0 {
		return ""
	}
	if cf.MeasureStringPx(s) <= maxPx {
		return s
	}
	ellW := cf.MeasureString("...")
	limit := fixed.I(maxPx) - ellW
	if limit <= 0 {
		return ""
	}
	var w fixed.Int26_6
	lastIdx := 0
	for i, r := range s {
		adv := cf.Advance(r)
		if w+adv > limit {
			break
		}
		w += adv
		lastIdx = i + len(string(r))
	}
	return s[:lastIdx] + "..."
}

type faceKey struct {
	family Family
	style  Style
	sizePx int
}

// Manager creates and caches CachedFaces across families, styles, and pixel sizes.
type Manager struct {
	faces map[faceKey]*CachedFace
}

// NewManager initializes the embedded fonts and returns a Manager.
func NewManager() (*Manager, error) {
	if err := ensureParsed(); err != nil {
		return nil, err
	}
	return &Manager{
		faces: make(map[faceKey]*CachedFace, 24),
	}, nil
}

// Face returns a cached face for the given family, style, and pixel size.
func (m *Manager) Face(family Family, style Style, sizePx int) *CachedFace {
	if style&StyleMono != 0 {
		family = FamilySans
		style = StyleMono
	}
	key := faceKey{family: family, style: style, sizePx: sizePx}
	if f, ok := m.faces[key]; ok {
		return f
	}
	// Evict cache if too many distinct sizes accumulate (e.g. resizing repeatedly).
	if len(m.faces) > 40 {
		m.faces = make(map[faceKey]*CachedFace, 16)
	}
	var ot *opentype.Font
	if style == StyleMono {
		ot = parsed.mono
	} else if family == FamilySans {
		switch style {
		case StyleBold:
			ot = parsed.sansBold
		case StyleItalic:
			ot = parsed.sansItalic
		case StyleBoldItalic:
			ot = parsed.sansBoldItalic
		default:
			ot = parsed.sansRegular
		}
	} else {
		switch style {
		case StyleBold:
			ot = parsed.serifBold
		case StyleItalic:
			ot = parsed.serifItalic
		case StyleBoldItalic:
			ot = parsed.serifBoldItalic
		default:
			ot = parsed.serifRegular
		}
	}
	cf, err := newCachedFace(ot, sizePx)
	if err != nil {
		panic(err)
	}
	m.faces[key] = cf
	return cf
}
