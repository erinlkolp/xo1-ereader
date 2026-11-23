package x11

import (
	"image"
	"math/bits"
)

// PixelFormat describes how the X11 server's root visual encodes RGB pixels.
type PixelFormat struct {
	Depth          uint8
	BitsPerPixel   uint8
	ScanlinePad    uint8 // in bits (usually 32)
	ImageByteOrder uint8 // 0 = LSBFirst, 1 = MSBFirst
	RedMask        uint32
	GreenMask      uint32
	BlueMask       uint32

	// Precomputed bit shifts & widths for generic TrueColor encoding
	rShift, gShift, bShift uint8
	rBits, gBits, bBits    uint8
	isRGB565LE             bool
	isBGRX32LE             bool
}

func (pf *PixelFormat) init() {
	pf.rShift, pf.rBits = maskShiftBits(pf.RedMask)
	pf.gShift, pf.gBits = maskShiftBits(pf.GreenMask)
	pf.bShift, pf.bBits = maskShiftBits(pf.BlueMask)

	pf.isRGB565LE = pf.BitsPerPixel == 16 &&
		pf.ImageByteOrder == 0 &&
		pf.RedMask == 0xF800 &&
		pf.GreenMask == 0x07E0 &&
		pf.BlueMask == 0x001F

	pf.isBGRX32LE = pf.BitsPerPixel == 32 &&
		pf.ImageByteOrder == 0 &&
		pf.RedMask == 0x00FF0000 &&
		pf.GreenMask == 0x0000FF00 &&
		pf.BlueMask == 0x000000FF
}

func maskShiftBits(mask uint32) (shift, width uint8) {
	if mask == 0 {
		return 0, 0
	}
	tz := bits.TrailingZeros32(mask)
	ones := bits.OnesCount32(mask)
	return uint8(tz), uint8(ones)
}

// StrideBytes returns the X11-padded byte stride for a row of width pixels.
func (pf *PixelFormat) StrideBytes(width int) int {
	rawBits := width * int(pf.BitsPerPixel)
	pad := int(pf.ScanlinePad)
	if pad <= 0 {
		pad = 32
	}
	paddedBits := ((rawBits + pad - 1) / pad) * pad
	return paddedBits / 8
}

// EncodeStrip converts rows [yStart, yStart+stripH) of the physical X11 window
// from the logical image src (applying 0/90/180/270 degree clockwise rotation)
// into dstBuf using the X11 server's native pixel format.
//
// Logical image dimensions:
//   - rotation 0 or 180: src is (winW x winH)
//   - rotation 90 or 270: src is (winH x winW)
func (pf *PixelFormat) EncodeStrip(dstBuf []byte, src *image.RGBA, winW, winH, yStart, stripH, rotation int) {
	stride := pf.StrideBytes(winW)
	srcW := src.Rect.Dx()
	srcH := src.Rect.Dy()
	rot := ((rotation % 360) + 360) % 360

	// Fast path: 0-degree rotation (landscape)
	if rot == 0 && srcW == winW && srcH == winH {
		if pf.isRGB565LE {
			for row := 0; row < stripH; row++ {
				srcRow := (yStart + row) * src.Stride
				dstRow := row * stride
				for x := 0; x < winW; x++ {
					so := srcRow + x*4
					r := uint16(src.Pix[so])
					g := uint16(src.Pix[so+1])
					b := uint16(src.Pix[so+2])
					v := ((r >> 3) << 11) | ((g >> 2) << 5) | (b >> 3)
					do := dstRow + x*2
					dstBuf[do] = byte(v)
					dstBuf[do+1] = byte(v >> 8)
				}
			}
			return
		}
		if pf.isBGRX32LE {
			for row := 0; row < stripH; row++ {
				srcRow := (yStart + row) * src.Stride
				dstRow := row * stride
				for x := 0; x < winW; x++ {
					so := srcRow + x*4
					do := dstRow + x*4
					dstBuf[do] = src.Pix[so+2]   // B
					dstBuf[do+1] = src.Pix[so+1] // G
					dstBuf[do+2] = src.Pix[so]   // R
					dstBuf[do+3] = 0
				}
			}
			return
		}
	}

	// Rotated or generic pixel-format path
	bppBytes := int(pf.BitsPerPixel) / 8
	for row := 0; row < stripH; row++ {
		py := yStart + row
		dstRow := row * stride
		for px := 0; px < winW; px++ {
			var sx, sy int
			switch rot {
			case 90: // 90° clockwise: logical (sx, sy) -> physical (winW-1-sy, sx)
				sx = py
				sy = winW - 1 - px
			case 180:
				sx = winW - 1 - px
				sy = winH - 1 - py
			case 270: // 270° clockwise (90° CCW): logical (sx, sy) -> physical (sy, winH-1-sx)
				sx = winH - 1 - py
				sy = px
			default:
				sx = px
				sy = py
			}

			var r, g, b uint8
			if sx >= 0 && sx < srcW && sy >= 0 && sy < srcH {
				so := sy*src.Stride + sx*4
				r = src.Pix[so]
				g = src.Pix[so+1]
				b = src.Pix[so+2]
			}

			do := dstRow + px*bppBytes
			if pf.isRGB565LE {
				v := ((uint16(r) >> 3) << 11) | ((uint16(g) >> 2) << 5) | (uint16(b) >> 3)
				dstBuf[do] = byte(v)
				dstBuf[do+1] = byte(v >> 8)
			} else if pf.isBGRX32LE {
				dstBuf[do] = b
				dstBuf[do+1] = g
				dstBuf[do+2] = r
				dstBuf[do+3] = 0
			} else {
				v := pf.packGeneric(r, g, b)
				writePixelBytes(dstBuf[do:do+bppBytes], v, bppBytes, pf.ImageByteOrder == 1)
			}
		}
	}
}

func (pf *PixelFormat) packGeneric(r, g, b uint8) uint32 {
	var v uint32
	if pf.rBits > 0 {
		v |= (uint32(r) >> (8 - pf.rBits)) << pf.rShift
	}
	if pf.gBits > 0 {
		v |= (uint32(g) >> (8 - pf.gBits)) << pf.gShift
	}
	if pf.bBits > 0 {
		v |= (uint32(b) >> (8 - pf.bBits)) << pf.bShift
	}
	return v
}

func writePixelBytes(dst []byte, v uint32, n int, bigEndian bool) {
	if bigEndian {
		for i := 0; i < n; i++ {
			dst[n-1-i] = byte(v >> (8 * i))
		}
		return
	}
	for i := 0; i < n; i++ {
		dst[i] = byte(v >> (8 * i))
	}
}

// LogicalSize returns the logical canvas dimensions for a window of (winW, winH)
// at the given clockwise rotation in degrees.
func LogicalSize(winW, winH, rotation int) (int, int) {
	rot := ((rotation % 360) + 360) % 360
	if rot == 90 || rot == 270 {
		return winH, winW
	}
	return winW, winH
}

// PhysicalToLogicalPoint maps a mouse click in physical window coordinates into
// logical (rotated) UI coordinates.
func PhysicalToLogicalPoint(px, py, winW, winH, rotation int) (int, int) {
	rot := ((rotation % 360) + 360) % 360
	switch rot {
	case 90:
		return py, winW - 1 - px
	case 180:
		return winW - 1 - px, winH - 1 - py
	case 270:
		return winH - 1 - py, px
	default:
		return px, py
	}
}
