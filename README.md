# XO-1 E-Reader

A lightweight, **zero-runtime-dependency** pure-Go `.epub` reader built specifically for the **OLPC XO-1** running GNOME on Fedora 18.

## Why It Works Great on the XO-1

- **Single static binary (`CGO_ENABLED=0 GOARCH=386 GO386=softfloat`)** — no GTK, WebKit, SDL, `libX11`, or system fonts required on Fedora 18; avoids SSE2 illegal-instruction crashes on the AMD Geode LX CPU.
- **Native pure-Go X11 client** — connects directly to Xorg (supporting both the XO-1's **16-bit RGB565** display depth and modern 24/32-bit desktops) with EWMH fullscreen & GNOME window integration.
- **Fast on 433 MHz / 256 MB RAM** — lazy per-chapter EPUB loading, integer glyph-bitmap caching, and bounded memory usage.
- **Built for E-Book / Tablet Mode** — full bezel D-pad & game button navigation (`KP_*` keysyms), in-app 0°/90°/180°/270° screen rotation with automatic D-pad direction remapping, and a razor-sharp **Sunlight (Monochrome)** high-contrast theme for the XO-1's reflective 200-DPI screen.
- **Complete reading features** — EPUB 2 & EPUB 3 support, styled text (headings, bold, italic, blockquotes, lists, monospace code), embedded Liberation Serif & Go Sans fonts, adjustable font size, Table of Contents with anchor jumping, built-in Library / folder browser, bookmarks, and saved reading progress (`~/.config/xo1-reader/state.json`).

## Building

### Cross-compile for the OLPC XO-1 (recommended)

```bash
make xo1
```

Or run directly:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=386 GO386=softfloat go build -ldflags="-s -w" -o build/xo1-reader ./cmd/xo1-reader
```

### Build for local Linux testing

```bash
make native
./build/xo1-reader-native /path/to/book.epub
```

### Run tests

```bash
make test
```

## Deploying to Your OLPC XO-1

Copy `build/xo1-reader` to your XO-1 via USB stick, SD card, or `scp`:

```bash
scp build/xo1-reader olpc@<xo1-ip>:~/xo1-reader
```

On the XO-1:

```bash
chmod +x ~/xo1-reader
~/xo1-reader -fullscreen -dir ~/Books
```

### Optional: Add a GNOME Application Launcher on Fedora 18

Create `~/.local/share/applications/xo1-reader.desktop`:

```ini
[Desktop Entry]
Name=XO-1 E-Reader
Comment=Lightweight EPUB e-reader
Exec=/home/olpc/xo1-reader -fullscreen -dir /home/olpc/Books %f
Terminal=false
Type=Application
Categories=Office;Viewer;
MimeType=application/epub+zip;
```

## Controls

### XO-1 Bezel (Tablet / E-Book Mode)

| Button | In Reader | In Menu / Lists |
|---|---|---|
| **D-Pad Right / Down** | Next page (auto-rotates!) | Move down / Increase setting |
| **D-Pad Left / Up** | Previous page | Move up / Decrease setting |
| **Cross (×)** (`KP_Page_Down`) | Next page | Page down |
| **Circle (○)** (`KP_Page_Up`) | Previous page | Page up |
| **Square (□)** (`KP_Home`) | Open Menu | Back / Close |
| **Checkmark (✓)** (`KP_End`) | Toggle bookmark | Confirm / Open |

### Laptop Keyboard & Touchpad

| Input | Action |
|---|---|
| `Space`, `Right`, `Down`, `PgDn`, `j`, `n`, click right side, wheel down | Next page |
| `Backspace`, `Left`, `Up`, `PgUp`, `k`, `p`, click left side, wheel up | Previous page |
| `]` / `[` (or `Shift+Right` / `Shift+Left`) | Next / Previous chapter |
| `m`, `Esc`, `Tab`, click top header | Open Menu |
| `c` | Table of Contents |
| `l` or `o` | Library / Folder browser |
| `b` | Toggle bookmark on current page |
| `+` / `-` | Increase / decrease font size |
| `g` | Toggle Serif (Liberation Serif) / Sans (Go Sans) |
| `t` | Cycle theme: Sunlight (XO-1 Mono) → Paper → Night |
| `r` | Rotate screen 90° clockwise (automatically remaps D-pad) |
| `f` or `F11` | Toggle GNOME fullscreen |
| `?` or `h` | Controls & Help screen |
| `q` | Save position and quit |

## CLI Flags

```text
xo1-reader [flags] [book.epub]
  -dir string        Library folder to browse for .epub files
  -font-size int     Base body font size in pixels (16-52)
  -fullscreen        Start in fullscreen mode
  -rotate int        Initial screen rotation in degrees: 0, 90, 180, 270
  -state string      Path to state.json (default ~/.config/xo1-reader/state.json)
  -theme string      Color theme: sunlight, paper, night
```
