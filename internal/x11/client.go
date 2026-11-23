package x11

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

// EventType identifies high-level X11 window events delivered to the UI.
type EventType uint8

const (
	EventExpose EventType = iota
	EventResize
	EventKeyPress
	EventButtonPress
	EventClose
)

// Event is a decoded X11 input or window event.
type Event struct {
	Type    EventType
	Width   int    // EventResize
	Height  int    // EventResize
	KeySym  uint32 // EventKeyPress
	KeyCode uint8  // EventKeyPress
	Mod     uint16 // EventKeyPress modifier bitmask
	Button  uint8  // EventButtonPress (1=left, 4=wheelUp, 5=wheelDown)
	X, Y    int    // EventButtonPress (physical window coordinates)
}

// Window is a minimal, zero-CGo pure-Go X11 window client.
type Window struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer

	resIDBase  uint32
	resIDMask  uint32
	nextResID  uint32
	seq        uint16
	maxReqSize int // in bytes

	rootWin      uint32
	rootVisual   uint32
	rootDepth    uint8
	screenWidth  int
	screenHeight int

	winID uint32
	gcID  uint32
	Width int
	Height int

	PixFmt PixelFormat

	// Keyboard mapping table
	minKeycode    uint8
	maxKeycode    uint8
	keysymsPerKey int
	keysyms       []uint32

	// Atoms for ICCCM / EWMH window management (GNOME on Fedora 18)
	atomWMProtocols     uint32
	atomWMDeleteWindow  uint32
	atomNetWMName       uint32
	atomUTF8String      uint32
	atomNetWMState      uint32
	atomNetWMFullscreen uint32

	pendingEvents [][]byte
	stripBuf      []byte
	closed        bool
}

// OpenWindow connects to the X11 server from $DISPLAY and creates a top-level window.
func OpenWindow(title string, width, height int, fullscreen bool) (*Window, error) {
	display := os.Getenv("DISPLAY")
	if display == "" {
		display = ":0"
	}
	conn, host, displayNum, screenNum, err := dialDisplay(display)
	if err != nil {
		return nil, fmt.Errorf("connect X11 display %q: %w", display, err)
	}

	w := &Window{
		conn:   conn,
		reader: bufio.NewReaderSize(conn, 32*1024),
		writer: bufio.NewWriterSize(conn, 256*1024),
	}

	authName, authData := readXAuthority(host, displayNum)
	if err := w.handshake(authName, authData, screenNum); err != nil {
		_ = conn.Close()
		return nil, err
	}

	// Clamp requested window size to screen dimensions
	if width <= 0 || width > w.screenWidth {
		width = w.screenWidth
	}
	if height <= 0 || height > w.screenHeight {
		height = w.screenHeight
	}
	w.Width = width
	w.Height = height

	// Fetch keyboard mapping for translating keycodes -> keysyms
	if err := w.refreshKeyboardMapping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("get keyboard mapping: %w", err)
	}

	// Intern atoms needed for GNOME / EWMH integration
	w.atomWMProtocols, _ = w.internAtom("WM_PROTOCOLS")
	w.atomWMDeleteWindow, _ = w.internAtom("WM_DELETE_WINDOW")
	w.atomNetWMName, _ = w.internAtom("_NET_WM_NAME")
	w.atomUTF8String, _ = w.internAtom("UTF8_STRING")
	w.atomNetWMState, _ = w.internAtom("_NET_WM_STATE")
	w.atomNetWMFullscreen, _ = w.internAtom("_NET_WM_STATE_FULLSCREEN")

	// Create top-level window
	w.winID = w.allocID()
	w.gcID = w.allocID()
	if err := w.createWindow(width, height); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := w.createGC(); err != nil {
		_ = conn.Close()
		return nil, err
	}

	w.SetTitle(title)
	w.setWMClass("xo1-reader", "XO1Reader")
	if w.atomWMProtocols != 0 && w.atomWMDeleteWindow != 0 {
		var buf [4]byte
		binary.LittleEndian.PutUint32(buf[:], w.atomWMDeleteWindow)
		w.changeProperty(w.atomWMProtocols, 4 /* XA_ATOM */, 32, buf[:])
	}
	if fullscreen && w.atomNetWMState != 0 && w.atomNetWMFullscreen != 0 {
		var buf [4]byte
		binary.LittleEndian.PutUint32(buf[:], w.atomNetWMFullscreen)
		w.changeProperty(w.atomNetWMState, 4 /* XA_ATOM */, 32, buf[:])
	}

	w.mapWindow()
	if err := w.writer.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return w, nil
}

// Close closes the X11 connection.
func (w *Window) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.conn.Close()
}

// ScreenSize returns the root screen dimensions (e.g. 1200x900 on OLPC XO-1).
func (w *Window) ScreenSize() (int, int) {
	return w.screenWidth, w.screenHeight
}

// SetTitle updates both WM_NAME (legacy ICCCM) and _NET_WM_NAME (UTF-8).
func (w *Window) SetTitle(title string) {
	if title == "" {
		title = "XO-1 Reader"
	}
	// XA_WM_NAME = 39, XA_STRING = 31
	w.changeProperty(39, 31, 8, []byte(title))
	if w.atomNetWMName != 0 && w.atomUTF8String != 0 {
		w.changeProperty(w.atomNetWMName, w.atomUTF8String, 8, []byte(title))
	}
	_ = w.writer.Flush()
}

// SetFullscreen sends an EWMH _NET_WM_STATE ClientMessage to toggle fullscreen under GNOME.
func (w *Window) SetFullscreen(enable bool) {
	if w.atomNetWMState == 0 || w.atomNetWMFullscreen == 0 {
		return
	}
	var ev [32]byte
	ev[0] = 33 // ClientMessage
	ev[1] = 32 // format = 32-bit
	binary.LittleEndian.PutUint32(ev[4:8], w.winID)
	binary.LittleEndian.PutUint32(ev[8:12], w.atomNetWMState)
	var action uint32
	if enable {
		action = 1 // _NET_WM_STATE_ADD
	} else {
		action = 0 // _NET_WM_STATE_REMOVE
	}
	binary.LittleEndian.PutUint32(ev[12:16], action)
	binary.LittleEndian.PutUint32(ev[16:20], w.atomNetWMFullscreen)
	binary.LittleEndian.PutUint32(ev[20:24], 0)
	binary.LittleEndian.PutUint32(ev[24:28], 1) // normal application source indication

	// SendEvent opcode 25 to root window with SubstructureRedirect|SubstructureNotify (0x00180000)
	var req [44]byte
	req[0] = 25
	req[1] = 0 // propagate = false
	binary.LittleEndian.PutUint16(req[2:4], 11)
	binary.LittleEndian.PutUint32(req[4:8], w.rootWin)
	binary.LittleEndian.PutUint32(req[8:12], 0x00180000)
	copy(req[12:44], ev[:])
	w.writeRequest(req[:])
	_ = w.writer.Flush()
}

// Present blits the logical RGBA image to the X11 window, applying in-app rotation
// and pixel format conversion (16-bit RGB565 on XO-1 or 32-bit BGRX on desktop),
// chunked safely under the X server's maximum request length.
func (w *Window) Present(img *image.RGBA, rotation int) error {
	if w.closed || w.Width <= 0 || w.Height <= 0 {
		return nil
	}
	stride := w.PixFmt.StrideBytes(w.Width)
	if stride <= 0 {
		return nil
	}
	// Reserve 24 bytes for PutImage header + safety margin
	maxPayload := w.maxReqSize - 64
	if maxPayload < stride {
		maxPayload = stride
	}
	maxStripH := maxPayload / stride
	if maxStripH < 1 {
		maxStripH = 1
	}
	if maxStripH > w.Height {
		maxStripH = w.Height
	}

	needBuf := stride * maxStripH
	if len(w.stripBuf) < needBuf {
		w.stripBuf = make([]byte, needBuf)
	}

	for y := 0; y < w.Height; y += maxStripH {
		sh := maxStripH
		if y+sh > w.Height {
			sh = w.Height - y
		}
		payloadLen := stride * sh
		w.PixFmt.EncodeStrip(w.stripBuf[:payloadLen], img, w.Width, w.Height, y, sh, rotation)

		padLen := (4 - (payloadLen % 4)) % 4
		reqWords := (24 + payloadLen + padLen) / 4

		var hdr [24]byte
		hdr[0] = 72 // PutImage
		hdr[1] = 2  // ZPixmap
		binary.LittleEndian.PutUint16(hdr[2:4], uint16(reqWords))
		binary.LittleEndian.PutUint32(hdr[4:8], w.winID)
		binary.LittleEndian.PutUint32(hdr[8:12], w.gcID)
		binary.LittleEndian.PutUint16(hdr[12:14], uint16(w.Width))
		binary.LittleEndian.PutUint16(hdr[14:16], uint16(sh))
		binary.LittleEndian.PutUint16(hdr[16:18], 0)
		binary.LittleEndian.PutUint16(hdr[18:20], uint16(y))
		hdr[20] = 0 // left-pad
		hdr[21] = w.rootDepth
		w.seq++
		_, _ = w.writer.Write(hdr[:])
		_, _ = w.writer.Write(w.stripBuf[:payloadLen])
		if padLen > 0 {
			var z [4]byte
			_, _ = w.writer.Write(z[:padLen])
		}
	}
	return w.writer.Flush()
}

// NextEvent blocks until the next relevant X11 event arrives.
func (w *Window) NextEvent() (Event, error) {
	for {
		pkt, err := w.nextRawEvent()
		if err != nil {
			return Event{}, err
		}
		evType := pkt[0] & 0x7F
		switch evType {
		case 2: // KeyPress
			keycode := pkt[1]
			mod := binary.LittleEndian.Uint16(pkt[28:30])
			sym := w.lookupKeySym(keycode, mod)
			return Event{
				Type:    EventKeyPress,
				KeySym:  sym,
				KeyCode: keycode,
				Mod:     mod,
			}, nil

		case 4: // ButtonPress
			btn := pkt[1]
			ex := int(int16(binary.LittleEndian.Uint16(pkt[24:26])))
			ey := int(int16(binary.LittleEndian.Uint16(pkt[26:28])))
			return Event{
				Type:   EventButtonPress,
				Button: btn,
				X:      ex,
				Y:      ey,
			}, nil

		case 12: // Expose
			count := binary.LittleEndian.Uint16(pkt[16:18])
			if count == 0 {
				return Event{Type: EventExpose}, nil
			}

		case 22: // ConfigureNotify
			nw := int(binary.LittleEndian.Uint16(pkt[20:22]))
			nh := int(binary.LittleEndian.Uint16(pkt[22:24]))
			if nw > 0 && nh > 0 && (nw != w.Width || nh != w.Height) {
				w.Width = nw
				w.Height = nh
				return Event{Type: EventResize, Width: nw, Height: nh}, nil
			}

		case 33: // ClientMessage
			msgType := binary.LittleEndian.Uint32(pkt[8:12])
			data0 := binary.LittleEndian.Uint32(pkt[12:16])
			if msgType == w.atomWMProtocols && data0 == w.atomWMDeleteWindow {
				return Event{Type: EventClose}, nil
			}

		case 34: // MappingNotify
			if pkt[4] == 1 { // MappingKeyboard
				_ = w.refreshKeyboardMapping()
			}
		}
	}
}

func (w *Window) nextRawEvent() ([]byte, error) {
	if len(w.pendingEvents) > 0 {
		pkt := w.pendingEvents[0]
		w.pendingEvents = w.pendingEvents[1:]
		return pkt, nil
	}
	for {
		var pkt [32]byte
		if _, err := io.ReadFull(w.reader, pkt[:]); err != nil {
			return nil, err
		}
		if pkt[0] == 1 {
			// Unexpected reply: drain extra bytes
			extra := binary.LittleEndian.Uint32(pkt[4:8])
			if extra > 0 {
				_, _ = io.CopyN(io.Discard, w.reader, int64(extra)*4)
			}
			continue
		}
		if pkt[0] == 0 {
			// X11 error packet; log if debugging enabled, otherwise ignore non-fatal async errors
			if os.Getenv("XO1_X11_DEBUG") != "" {
				fmt.Fprintf(os.Stderr, "x11 async error: code=%d seq=%d minor=%d major=%d\n",
					pkt[1], binary.LittleEndian.Uint16(pkt[2:4]),
					binary.LittleEndian.Uint16(pkt[8:10]), pkt[10])
			}
			continue
		}
		out := make([]byte, 32)
		copy(out, pkt[:])
		return out, nil
	}
}

func (w *Window) allocID() uint32 {
	id := w.resIDBase | (w.nextResID & w.resIDMask)
	w.nextResID++
	return id
}

func (w *Window) writeRequest(req []byte) uint16 {
	w.seq++
	_, _ = w.writer.Write(req)
	return w.seq
}

func (w *Window) readReply(expectSeq uint16) ([]byte, error) {
	if err := w.writer.Flush(); err != nil {
		return nil, err
	}
	for {
		var hdr [32]byte
		if _, err := io.ReadFull(w.reader, hdr[:]); err != nil {
			return nil, err
		}
		switch hdr[0] {
		case 0: // Error
			errSeq := binary.LittleEndian.Uint16(hdr[2:4])
			if errSeq == expectSeq {
				return nil, fmt.Errorf("X11 protocol error code=%d opcode=%d", hdr[1], hdr[10])
			}
		case 1: // Reply
			extra := binary.LittleEndian.Uint32(hdr[4:8])
			body := make([]byte, 32+int(extra)*4)
			copy(body[:32], hdr[:])
			if extra > 0 {
				if _, err := io.ReadFull(w.reader, body[32:]); err != nil {
					return nil, err
				}
			}
			repSeq := binary.LittleEndian.Uint16(hdr[2:4])
			if repSeq == expectSeq {
				return body, nil
			}
		default: // Event arrived before reply
			ev := make([]byte, 32)
			copy(ev, hdr[:])
			w.pendingEvents = append(w.pendingEvents, ev)
		}
	}
}

func (w *Window) internAtom(name string) (uint32, error) {
	nameBytes := []byte(name)
	padLen := (4 - (len(nameBytes) % 4)) % 4
	reqLen := 8 + len(nameBytes) + padLen
	req := make([]byte, reqLen)
	req[0] = 16 // InternAtom
	req[1] = 0  // only-if-exists = false
	binary.LittleEndian.PutUint16(req[2:4], uint16(reqLen/4))
	binary.LittleEndian.PutUint16(req[4:6], uint16(len(nameBytes)))
	copy(req[8:], nameBytes)
	seq := w.writeRequest(req)
	rep, err := w.readReply(seq)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(rep[8:12]), nil
}

func (w *Window) changeProperty(prop, propType uint32, format uint8, data []byte) {
	padLen := (4 - (len(data) % 4)) % 4
	reqLen := 24 + len(data) + padLen
	req := make([]byte, reqLen)
	req[0] = 18 // ChangeProperty
	req[1] = 0  // Replace
	binary.LittleEndian.PutUint16(req[2:4], uint16(reqLen/4))
	binary.LittleEndian.PutUint32(req[4:8], w.winID)
	binary.LittleEndian.PutUint32(req[8:12], prop)
	binary.LittleEndian.PutUint32(req[12:16], propType)
	req[16] = format
	numItems := uint32(len(data))
	if format == 16 {
		numItems /= 2
	} else if format == 32 {
		numItems /= 4
	}
	binary.LittleEndian.PutUint32(req[20:24], numItems)
	copy(req[24:], data)
	w.writeRequest(req)
}

func (w *Window) setWMClass(instance, class string) {
	data := append([]byte(instance), 0)
	data = append(data, []byte(class)...)
	data = append(data, 0)
	// XA_WM_CLASS = 67, XA_STRING = 31
	w.changeProperty(67, 31, 8, data)
}

func (w *Window) createWindow(width, height int) error {
	// Value mask: CWBackPixel (0x2) | CWEventMask (0x800)
	const (
		cwBackPixel = 0x00000002
		cwEventMask = 0x00000800
		evKeyPress  = 0x00000001
		evBtnPress  = 0x00000004
		evExpose    = 0x00008000
		evStructure = 0x00020000
	)
	var req [40]byte // 32-byte header + 2 uint32 values
	req[0] = 1       // CreateWindow
	req[1] = w.rootDepth
	binary.LittleEndian.PutUint16(req[2:4], 10)
	binary.LittleEndian.PutUint32(req[4:8], w.winID)
	binary.LittleEndian.PutUint32(req[8:12], w.rootWin)
	binary.LittleEndian.PutUint16(req[12:14], 0) // x
	binary.LittleEndian.PutUint16(req[14:16], 0) // y
	binary.LittleEndian.PutUint16(req[16:18], uint16(width))
	binary.LittleEndian.PutUint16(req[18:20], uint16(height))
	binary.LittleEndian.PutUint16(req[20:22], 0) // border width
	binary.LittleEndian.PutUint16(req[22:24], 1) // InputOutput
	binary.LittleEndian.PutUint32(req[24:28], w.rootVisual)
	binary.LittleEndian.PutUint32(req[28:32], cwBackPixel|cwEventMask)
	binary.LittleEndian.PutUint32(req[32:36], 0xFFFFFFFF) // white-ish initial bg
	binary.LittleEndian.PutUint32(req[36:40], evKeyPress|evBtnPress|evExpose|evStructure)
	w.writeRequest(req[:])
	return nil
}

func (w *Window) createGC() error {
	// GCGraphicsExposures = 0x10000 set to 0 so PutImage/CopyArea don't flood NoExpose events
	var req [20]byte
	req[0] = 55 // CreateGC
	binary.LittleEndian.PutUint16(req[2:4], 5)
	binary.LittleEndian.PutUint32(req[4:8], w.gcID)
	binary.LittleEndian.PutUint32(req[8:12], w.winID)
	binary.LittleEndian.PutUint32(req[12:16], 0x00010000)
	binary.LittleEndian.PutUint32(req[16:20], 0)
	w.writeRequest(req[:])
	return nil
}

func (w *Window) mapWindow() {
	var req [8]byte
	req[0] = 8 // MapWindow
	binary.LittleEndian.PutUint16(req[2:4], 2)
	binary.LittleEndian.PutUint32(req[4:8], w.winID)
	w.writeRequest(req[:])
}

func (w *Window) refreshKeyboardMapping() error {
	count := int(w.maxKeycode) - int(w.minKeycode) + 1
	if count <= 0 || count > 255 {
		return nil
	}
	var req [8]byte
	req[0] = 101 // GetKeyboardMapping
	binary.LittleEndian.PutUint16(req[2:4], 2)
	req[4] = w.minKeycode
	req[5] = uint8(count)
	seq := w.writeRequest(req[:])
	rep, err := w.readReply(seq)
	if err != nil {
		return err
	}
	kpk := int(rep[1])
	if kpk <= 0 {
		return nil
	}
	numSyms := (len(rep) - 32) / 4
	syms := make([]uint32, numSyms)
	for i := 0; i < numSyms; i++ {
		syms[i] = binary.LittleEndian.Uint32(rep[32+i*4 : 36+i*4])
	}
	w.keysymsPerKey = kpk
	w.keysyms = syms
	return nil
}

func (w *Window) lookupKeySym(keycode uint8, mod uint16) uint32 {
	if keycode < w.minKeycode || keycode > w.maxKeycode || w.keysymsPerKey == 0 {
		return 0
	}
	base := int(keycode-w.minKeycode) * w.keysymsPerKey
	if base >= len(w.keysyms) {
		return 0
	}
	s0 := w.keysyms[base]
	var s1 uint32
	if w.keysymsPerKey > 1 && base+1 < len(w.keysyms) {
		s1 = w.keysyms[base+1]
	}

	shift := (mod & ModShift) != 0
	caps := (mod & ModLock) != 0

	if shift && s1 != 0 {
		return s1
	}
	if caps && s0 >= 'a' && s0 <= 'z' && s1 != 0 {
		return s1
	}
	return s0
}

func (w *Window) handshake(authName string, authData []byte, screenNum int) error {
	an := []byte(authName)
	anPad := (4 - (len(an) % 4)) % 4
	adPad := (4 - (len(authData) % 4)) % 4

	req := make([]byte, 12+len(an)+anPad+len(authData)+adPad)
	req[0] = 'l' // LSBFirst
	binary.LittleEndian.PutUint16(req[2:4], 11)
	binary.LittleEndian.PutUint16(req[4:6], 0)
	binary.LittleEndian.PutUint16(req[6:8], uint16(len(an)))
	binary.LittleEndian.PutUint16(req[8:10], uint16(len(authData)))
	off := 12
	copy(req[off:], an)
	off += len(an) + anPad
	copy(req[off:], authData)

	if _, err := w.conn.Write(req); err != nil {
		return fmt.Errorf("write X11 setup: %w", err)
	}

	var hdr [8]byte
	if _, err := io.ReadFull(w.reader, hdr[:]); err != nil {
		return fmt.Errorf("read X11 setup header: %w", err)
	}
	extraWords := int(binary.LittleEndian.Uint16(hdr[6:8]))
	body := make([]byte, extraWords*4)
	if _, err := io.ReadFull(w.reader, body); err != nil {
		return fmt.Errorf("read X11 setup body: %w", err)
	}

	if hdr[0] != 1 {
		reasonLen := int(hdr[1])
		if reasonLen > len(body) {
			reasonLen = len(body)
		}
		return fmt.Errorf("X11 connection refused (status=%d): %s", hdr[0], string(body[:reasonLen]))
	}
	if len(body) < 32 {
		return errors.New("X11 setup body too short")
	}

	w.resIDBase = binary.LittleEndian.Uint32(body[4:8])
	w.resIDMask = binary.LittleEndian.Uint32(body[8:12])
	vendorLen := int(binary.LittleEndian.Uint16(body[16:18]))
	numRoots := int(body[20])
	numFormats := int(body[21])
	imageByteOrder := body[22]
	w.minKeycode = body[26]
	w.maxKeycode = body[27]

	maxReqWords := int(binary.LittleEndian.Uint16(body[18:20]))
	if maxReqWords == 0 {
		maxReqWords = 16384
	}
	w.maxReqSize = maxReqWords * 4

	pos := 32 + ((vendorLen + 3) &^ 3)

	type pixFmtEntry struct {
		depth       uint8
		bpp         uint8
		scanlinePad uint8
	}
	formats := make([]pixFmtEntry, 0, numFormats)
	for i := 0; i < numFormats && pos+8 <= len(body); i++ {
		formats = append(formats, pixFmtEntry{
			depth:       body[pos],
			bpp:         body[pos+1],
			scanlinePad: body[pos+2],
		})
		pos += 8
	}

	if screenNum < 0 || screenNum >= numRoots {
		screenNum = 0
	}

	for s := 0; s < numRoots && pos+40 <= len(body); s++ {
		rootWin := binary.LittleEndian.Uint32(body[pos : pos+4])
		widthPx := int(binary.LittleEndian.Uint16(body[pos+20 : pos+22]))
		heightPx := int(binary.LittleEndian.Uint16(body[pos+22 : pos+24]))
		rootVisual := binary.LittleEndian.Uint32(body[pos+32 : pos+36])
		rootDepth := body[pos+38]
		numDepths := int(body[pos+39])
		pos += 40

		var rMask, gMask, bMask uint32
		for d := 0; d < numDepths && pos+8 <= len(body); d++ {
			numVisuals := int(binary.LittleEndian.Uint16(body[pos+2 : pos+4]))
			pos += 8
			for v := 0; v < numVisuals && pos+24 <= len(body); v++ {
				vid := binary.LittleEndian.Uint32(body[pos : pos+4])
				if vid == rootVisual {
					rMask = binary.LittleEndian.Uint32(body[pos+8 : pos+12])
					gMask = binary.LittleEndian.Uint32(body[pos+12 : pos+16])
					bMask = binary.LittleEndian.Uint32(body[pos+16 : pos+20])
				}
				pos += 24
			}
		}

		if s == screenNum {
			w.rootWin = rootWin
			w.rootVisual = rootVisual
			w.rootDepth = rootDepth
			w.screenWidth = widthPx
			w.screenHeight = heightPx

			bpp := uint8(32)
			pad := uint8(32)
			for _, f := range formats {
				if f.depth == rootDepth {
					bpp = f.bpp
					pad = f.scanlinePad
					break
				}
			}
			w.PixFmt = PixelFormat{
				Depth:          rootDepth,
				BitsPerPixel:   bpp,
				ScanlinePad:    pad,
				ImageByteOrder: imageByteOrder,
				RedMask:        rMask,
				GreenMask:      gMask,
				BlueMask:       bMask,
			}
			w.PixFmt.init()
			return nil
		}
	}

	return errors.New("X11 setup did not contain requested screen")
}

func dialDisplay(display string) (conn net.Conn, host, displayNum string, screenNum int, err error) {
	// Format: [host]:display[.screen]
	colon := strings.LastIndexByte(display, ':')
	if colon < 0 {
		return nil, "", "", 0, fmt.Errorf("invalid DISPLAY %q", display)
	}
	host = display[:colon]
	rest := display[colon+1:]
	displayNum = rest
	if dot := strings.IndexByte(rest, '.'); dot >= 0 {
		displayNum = rest[:dot]
		screenNum, _ = strconv.Atoi(rest[dot+1:])
	}

	dispInt, err := strconv.Atoi(displayNum)
	if err != nil {
		return nil, "", "", 0, fmt.Errorf("invalid display number in %q", display)
	}

	if host == "" || host == "unix" {
		sockPath := fmt.Sprintf("/tmp/.X11-unix/X%d", dispInt)
		conn, err = net.Dial("unix", sockPath)
		if err != nil {
			// Fallback to Linux abstract Unix socket
			conn, err = net.Dial("unix", "@"+sockPath)
		}
		if err != nil {
			return nil, "", "", 0, err
		}
		return conn, host, displayNum, screenNum, nil
	}

	addr := net.JoinHostPort(host, strconv.Itoa(6000+dispInt))
	conn, err = net.Dial("tcp", addr)
	if err != nil {
		return nil, "", "", 0, err
	}
	return conn, host, displayNum, screenNum, nil
}
