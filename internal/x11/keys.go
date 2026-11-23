package x11

// Standard X11 KeySyms including OLPC XO-1 bezel D-pad and game buttons.
const (
	KeyBackSpace = 0xFF08
	KeyTab       = 0xFF09
	KeyReturn    = 0xFF0D
	KeyEscape    = 0xFF1B
	KeyHome      = 0xFF50
	KeyLeft      = 0xFF51
	KeyUp        = 0xFF52
	KeyRight     = 0xFF53
	KeyDown      = 0xFF54
	KeyPageUp    = 0xFF55
	KeyPageDown  = 0xFF56
	KeyEnd       = 0xFF57
	KeyF1        = 0xFFBE
	KeyF11       = 0xFFC8

	// OLPC XO-1 Bezel Game Buttons & D-Pad (mapped to X11 KP_* keysyms)
	// Left bezel D-Pad:
	KeyKPHome     = 0xFF95 // XO-1 Square button (□)
	KeyKPLeft     = 0xFF96 // XO-1 D-Pad Left
	KeyKPUp       = 0xFF97 // XO-1 D-Pad Up
	KeyKPRight    = 0xFF98 // XO-1 D-Pad Right
	KeyKPDown     = 0xFF99 // XO-1 D-Pad Down
	KeyKPPageUp   = 0xFF9A // XO-1 Circle button (○)
	KeyKPPageDown = 0xFF9B // XO-1 Cross button (×)
	KeyKPEnd      = 0xFF9C // XO-1 Checkmark button (✓)
	KeyKPEnter    = 0xFF8D
	KeyKPAdd      = 0xFFAB
	KeyKPSubtract = 0xFFAD

	// Numeric keypad digits (in case NumLock is active on XO-1)
	KeyKP2 = 0xFFB2 // Down
	KeyKP4 = 0xFFB4 // Left
	KeyKP6 = 0xFFB6 // Right
	KeyKP8 = 0xFFB8 // Up

	// XF86 Rotate Windows key (XO-1 screen rotate button when mapped)
	KeyXF86RotateWindows = 0x1008FF74

	KeySpace = 0x0020
)

// Modifier bitmasks from X11 KeyPress state field.
const (
	ModShift   uint16 = 1 << 0
	ModLock    uint16 = 1 << 1
	ModControl uint16 = 1 << 2
	ModMod1    uint16 = 1 << 3 // Alt
	ModMod2    uint16 = 1 << 4 // NumLock
)

// RotateDPadKeySym remaps the XO-1's physical bezel D-pad keysyms (KP_Up/Down/Left/Right)
// to match the current in-app screen rotation (0, 90, 180, 270 degrees clockwise).
// That way, when holding the XO-1 vertically in tablet/e-book mode, pressing the
// physical "up" side of the D-pad always moves up on screen!
func RotateDPadKeySym(sym uint32, rotationDeg int) uint32 {
	rot := ((rotationDeg % 360) + 360) % 360
	if rot == 0 {
		return sym
	}
	// Map KP_2/4/6/8 (NumLock) onto directional KP keys too
	switch sym {
	case KeyKP8:
		sym = KeyKPUp
	case KeyKP6:
		sym = KeyKPRight
	case KeyKP2:
		sym = KeyKPDown
	case KeyKP4:
		sym = KeyKPLeft
	}
	order := [4]uint32{KeyKPUp, KeyKPRight, KeyKPDown, KeyKPLeft}
	idx := -1
	for i, k := range order {
		if sym == k {
			idx = i
			break
		}
	}
	if idx < 0 {
		return sym
	}
	// Rotating the rendered image clockwise by 90 deg means physical Left becomes logical Up.
	steps := rot / 90
	return order[(idx+steps)%4]
}
