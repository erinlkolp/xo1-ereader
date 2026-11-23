package x11

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

func TestEncodeRGB565AndBGRX32AndRotation(t *testing.T) {
	// 4x2 logical image with distinct corner colors
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	src.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})   // pure red
	src.SetRGBA(3, 0, color.RGBA{0, 255, 0, 255})   // pure green
	src.SetRGBA(0, 1, color.RGBA{0, 0, 255, 255})   // pure blue
	src.SetRGBA(3, 1, color.RGBA{255, 255, 255, 255}) // white

	// 1. XO-1 16-bit RGB565 little-endian (0 deg)
	pf16 := PixelFormat{
		Depth: 16, BitsPerPixel: 16, ScanlinePad: 32, ImageByteOrder: 0,
		RedMask: 0xF800, GreenMask: 0x07E0, BlueMask: 0x001F,
	}
	pf16.init()
	if !pf16.isRGB565LE {
		t.Fatal("expected isRGB565LE fast path")
	}
	buf16 := make([]byte, pf16.StrideBytes(4)*2)
	pf16.EncodeStrip(buf16, src, 4, 2, 0, 2, 0)

	if got := binary.LittleEndian.Uint16(buf16[0:2]); got != 0xF800 {
		t.Fatalf("RGB565 red: got 0x%04X, want 0xF800", got)
	}
	if got := binary.LittleEndian.Uint16(buf16[6:8]); got != 0x07E0 {
		t.Fatalf("RGB565 green: got 0x%04X, want 0x07E0", got)
	}
	stride := pf16.StrideBytes(4)
	if got := binary.LittleEndian.Uint16(buf16[stride : stride+2]); got != 0x001F {
		t.Fatalf("RGB565 blue: got 0x%04X, want 0x001F", got)
	}
	if got := binary.LittleEndian.Uint16(buf16[stride+6 : stride+8]); got != 0xFFFF {
		t.Fatalf("RGB565 white: got 0x%04X, want 0xFFFF", got)
	}

	// 2. Modern 32-bit BGRX little-endian (90 deg clockwise into 2x4 physical window)
	pf32 := PixelFormat{
		Depth: 24, BitsPerPixel: 32, ScanlinePad: 32, ImageByteOrder: 0,
		RedMask: 0x00FF0000, GreenMask: 0x0000FF00, BlueMask: 0x000000FF,
	}
	pf32.init()
	if !pf32.isBGRX32LE {
		t.Fatal("expected isBGRX32LE fast path")
	}
	buf32 := make([]byte, pf32.StrideBytes(2)*4)
	pf32.EncodeStrip(buf32, src, 2, 4, 0, 4, 90)
	// At 90 deg CW: logical (0,0) red maps to physical (px=1, py=0)
	off := 1 * 4
	if buf32[off] != 0 || buf32[off+1] != 0 || buf32[off+2] != 255 {
		t.Fatalf("rotated red pixel mismatch: got BGR=(%d,%d,%d)", buf32[off], buf32[off+1], buf32[off+2])
	}
}

func TestRotateDPadKeySym(t *testing.T) {
	if got := RotateDPadKeySym(KeyKPUp, 0); got != KeyKPUp {
		t.Fatalf("0 deg: got 0x%X", got)
	}
	if got := RotateDPadKeySym(KeyKPUp, 90); got != KeyKPRight {
		t.Fatalf("90 deg: got 0x%X, want KeyKPRight", got)
	}
	if got := RotateDPadKeySym(KeyKPUp, 180); got != KeyKPDown {
		t.Fatalf("180 deg: got 0x%X, want KeyKPDown", got)
	}
	if got := RotateDPadKeySym(KeyKPUp, 270); got != KeyKPLeft {
		t.Fatalf("270 deg: got 0x%X, want KeyKPLeft", got)
	}
	// Non-dpad key unchanged
	if got := RotateDPadKeySym(KeyKPHome, 90); got != KeyKPHome {
		t.Fatalf("non-dpad key changed: 0x%X", got)
	}
}

func TestParseXAuthority(t *testing.T) {
	var buf bytes.Buffer
	writeStr := func(s string) {
		_ = binary.Write(&buf, binary.BigEndian, uint16(len(s)))
		buf.WriteString(s)
	}
	_ = binary.Write(&buf, binary.BigEndian, uint16(familyLocal))
	writeStr("xo-laptop")
	writeStr("0")
	writeStr("MIT-MAGIC-COOKIE-1")
	cookie := "0123456789abcdef"
	writeStr(cookie)

	entries := parseXAuthority(&buf)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].name != "MIT-MAGIC-COOKIE-1" || string(entries[0].data) != cookie || entries[0].number != "0" {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
}
