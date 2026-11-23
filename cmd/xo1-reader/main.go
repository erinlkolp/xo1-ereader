package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"xo1-ereader/internal/ui"
)

func main() {
	var (
		libDir     = flag.String("dir", "", "Library folder to browse for .epub files")
		theme      = flag.String("theme", "", "Color theme: sunlight, paper, night")
		fontSize   = flag.Int("font-size", 0, "Base body font size in pixels (16-52)")
		rotate     = flag.Int("rotate", -1, "Initial screen rotation in degrees: 0, 90, 180, 270")
		fullscreen = flag.Bool("fullscreen", false, "Start in fullscreen mode")
		statePath  = flag.String("state", "", "Path to state.json (default ~/.config/xo1-reader/state.json)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "XO-1 E-Reader — Zero-dependency pure-Go EPUB reader for OLPC XO-1 (Fedora 18 / X11)\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n  xo1-reader [flags] [book.epub]\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	hasFull := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "fullscreen" {
			hasFull = true
		}
	})

	cfg := ui.Config{
		InitialBook: flag.Arg(0),
		LibraryDir:  *libDir,
		Theme:       *theme,
		FontSize:    *fontSize,
		Rotation:    *rotate,
		Fullscreen:  *fullscreen,
		HasTheme:    *theme != "",
		HasFontSize: *fontSize > 0,
		HasRotation: *rotate >= 0,
		HasFull:     hasFull,
		StatePath:   *statePath,
	}

	app, err := ui.NewApp(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "xo1-reader: %v\n", err)
		os.Exit(1)
	}

	// Handle SIGINT/SIGTERM gracefully so reading progress is always saved
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		app.Close()
		os.Exit(0)
	}()

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "xo1-reader: %v\n", err)
		os.Exit(1)
	}
}
