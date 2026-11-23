package ui

import "image/color"

// Theme defines colors and rendering flags for the e-reader UI.
type Theme struct {
	Name        string
	Label       string
	Bg          color.RGBA
	Fg          color.RGBA
	Muted       color.RGBA
	Rule        color.RGBA
	CodeBg      color.RGBA
	PanelBg     color.RGBA
	PanelBorder color.RGBA
	SelectBg    color.RGBA
	SelectFg    color.RGBA
	Accent      color.RGBA
	CrispMono   bool // Threshold alpha for razor-sharp XO-1 sunlight monochrome legibility
}

var (
	ThemeSunlight = Theme{
		Name:        "sunlight",
		Label:       "Sunlight (XO-1 High Contrast)",
		Bg:          color.RGBA{255, 255, 255, 255},
		Fg:          color.RGBA{0, 0, 0, 255},
		Muted:       color.RGBA{56, 56, 56, 255},
		Rule:        color.RGBA{72, 72, 72, 255},
		CodeBg:      color.RGBA{238, 238, 238, 255},
		PanelBg:     color.RGBA{255, 255, 255, 255},
		PanelBorder: color.RGBA{0, 0, 0, 255},
		SelectBg:    color.RGBA{0, 0, 0, 255},
		SelectFg:    color.RGBA{255, 255, 255, 255},
		Accent:      color.RGBA{0, 0, 0, 255},
		CrispMono:   false,
	}

	ThemePaper = Theme{
		Name:        "paper",
		Label:       "Paper (Warm)",
		Bg:          color.RGBA{247, 242, 231, 255},
		Fg:          color.RGBA{31, 27, 22, 255},
		Muted:       color.RGBA{107, 96, 83, 255},
		Rule:        color.RGBA{196, 185, 168, 255},
		CodeBg:      color.RGBA{235, 228, 214, 255},
		PanelBg:     color.RGBA{252, 249, 242, 255},
		PanelBorder: color.RGBA{120, 108, 92, 255},
		SelectBg:    color.RGBA{46, 39, 31, 255},
		SelectFg:    color.RGBA{252, 249, 242, 255},
		Accent:      color.RGBA{92, 61, 34, 255},
		CrispMono:   false,
	}

	ThemeNight = Theme{
		Name:        "night",
		Label:       "Night (Dark)",
		Bg:          color.RGBA{21, 23, 26, 255},
		Fg:          color.RGBA{222, 220, 212, 255},
		Muted:       color.RGBA{140, 144, 152, 255},
		Rule:        color.RGBA{62, 67, 76, 255},
		CodeBg:      color.RGBA{34, 38, 45, 255},
		PanelBg:     color.RGBA{28, 31, 36, 255},
		PanelBorder: color.RGBA{112, 118, 128, 255},
		SelectBg:    color.RGBA{222, 220, 212, 255},
		SelectFg:    color.RGBA{21, 23, 26, 255},
		Accent:      color.RGBA{180, 198, 220, 255},
		CrispMono:   false,
	}
)

var themeOrder = []Theme{ThemeSunlight, ThemePaper, ThemeNight}

func themeByName(name string) Theme {
	for _, t := range themeOrder {
		if t.Name == name {
			return t
		}
	}
	return ThemeSunlight
}

func nextThemeName(name string) string {
	for i, t := range themeOrder {
		if t.Name == name {
			return themeOrder[(i+1)%len(themeOrder)].Name
		}
	}
	return ThemeSunlight.Name
}

func prevThemeName(name string) string {
	for i, t := range themeOrder {
		if t.Name == name {
			return themeOrder[(i+len(themeOrder)-1)%len(themeOrder)].Name
		}
	}
	return ThemeSunlight.Name
}
