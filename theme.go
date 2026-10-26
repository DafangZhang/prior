package main

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// palette is the colors of the interface, in the light or the dark
// appearance, tinted by the sidebar color. It follows the design of
// motrix-mygo: a tinted window behind a translucent panel, one accent
// color, and status tones.
type palette struct {
	dark bool
	// appBg is the window's own color, below the sidebar and the panel.
	appBg ui.Color
	// windowTop and windowBottom are the gradient behind the sidebar.
	windowTop, windowBottom ui.Color
	// card is the panel pages lie on, tile a statistic or a control group.
	card, tile  ui.Color
	text, muted ui.Color
	border      ui.Color
	// hover and active wash what the pointer is on and what is chosen.
	hover, active  ui.Color
	accent         ui.Color
	accentText     ui.Color
	down, downFill ui.Color
	up, upFill     ui.Color
	shadow         ui.Color
	success        ui.Color
	// severities S0 to S3.
	s0, s1, s2, s3 ui.Color
}

type sidebarColor struct {
	name string
	hue  float32
	gray bool
}

var sidebarColors = []sidebarColor{
	{"blue", 215, false},
	{"violet", 264, false},
	{"pink", 334, false},
	{"orange", 25, false},
	{"gold", 45, false},
	{"green", 145, false},
	{"cyan", 186, false},
	{"gray", 220, true},
}

func findSidebarColor(name string) sidebarColor {
	for _, sc := range sidebarColors {
		if sc.name == name {
			return sc
		}
	}
	return sidebarColors[0]
}

func (sc sidebarColor) swatch() ui.Color {
	if sc.gray {
		return hsl(sc.hue, 0.05, 0.62)
	}
	return hsl(sc.hue, 0.78, 0.6)
}

func newPalette(dark bool, sc sidebarColor) palette {
	sat := float32(0.58)
	if sc.gray {
		sat = 0.04
	}
	p := palette{dark: dark}
	if dark {
		p.windowTop = hsl(sc.hue+40, sat*0.72, 0.22)
		p.windowBottom = hsl(sc.hue, sat*0.72, 0.17)
		p.card = ui.RGBA(17, 17, 26, 0.86)
		p.tile = ui.RGBA(24, 24, 36, 0.82)
		p.text = ui.RGB(250, 250, 250)
		p.muted = ui.RGB(161, 161, 170)
		p.border = ui.RGBA(255, 255, 255, 0.1)
		p.hover = ui.RGBA(255, 255, 255, 0.06)
		p.active = ui.RGBA(255, 255, 255, 0.14)
		p.accent = ui.Hex("#3b82f6")
		p.down = hsl(193, 0.83, 0.55)
		p.downFill = hsl(193, 0.83, 0.55).Alpha(0.18)
		p.up = hsl(243, 0.92, 0.72)
		p.upFill = hsl(243, 0.92, 0.72).Alpha(0.18)
		p.shadow = ui.RGBA(0, 0, 0, 0.35)
		p.success = ui.Hex("#22c55e")
		p.s0 = ui.Hex("#f87171")
		p.s1 = ui.Hex("#fbbf24")
		p.s2 = ui.Hex("#60a5fa")
		p.s3 = ui.RGB(161, 161, 170)
	} else {
		p.windowTop = hsl(sc.hue, sat, 0.96)
		p.windowBottom = hsl(sc.hue, sat, 0.9)
		p.card = ui.RGBA(255, 255, 255, 0.92)
		p.tile = ui.RGB(255, 255, 255)
		p.text = ui.RGB(10, 10, 10)
		p.muted = ui.RGB(115, 115, 115)
		p.border = ui.RGBA(0, 0, 0, 0.09)
		p.hover = ui.RGBA(0, 0, 0, 0.04)
		p.active = ui.RGBA(0, 0, 0, 0.09)
		p.accent = ui.Hex("#2f7df6")
		p.down = hsl(193, 0.83, 0.5)
		p.downFill = ui.Hex("#e6f8fc")
		p.up = hsl(243, 0.96, 0.66)
		p.upFill = ui.Hex("#f1f0ff")
		p.shadow = ui.RGBA(15, 23, 42, 0.08)
		p.success = ui.Hex("#10b981")
		p.s0 = ui.Hex("#ef4444")
		p.s1 = ui.Hex("#f59e0b")
		p.s2 = ui.Hex("#3b82f6")
		p.s3 = ui.RGB(100, 116, 139)
	}
	p.accentText = ui.RGB(255, 255, 255)
	p.appBg = p.windowBottom
	return p
}

var (
	lightPalette = newPalette(false, sidebarColors[0])
	darkPalette  = newPalette(true, sidebarColors[0])
)

func paletteFor(theme *ui.Theme, sc sidebarColor) palette {
	if theme != nil && theme.Dark {
		return newPalette(true, sc)
	}
	return newPalette(false, sc)
}

// theme is the widgets' theme in the palette's colors, so that what the
// framework draws follows the accent and the radius of the design.
func (p palette) theme(base *ui.Theme) *ui.Theme {
	t := *base
	t.Accent = p.accent
	t.AccentHover = p.accent.Mix(ui.RGB(0, 0, 0), 0.08)
	t.AccentPressed = p.accent.Mix(ui.RGB(0, 0, 0), 0.16)
	t.AccentText = p.accentText
	t.Text, t.TextMuted, t.Border = p.text, p.muted, p.border
	t.Radius = 8
	return &t
}

// severity is the color of a severity level, S0 the worst to S3 the least.
func (p palette) severity(sev string) ui.Color {
	switch sev {
	case "S0":
		return p.s0
	case "S1":
		return p.s1
	case "S2":
		return p.s2
	default:
		return p.s3
	}
}

// tone is the colors of a status: of its pill and of its bar.
type tone struct{ bg, text, bar ui.Color }

func (p palette) tone(name string) tone {
	hex := map[string][3]string{
		"blue":  {"#dbeafe", "#1e40af", "#3b82f6"},
		"amber": {"#fef3c7", "#b45309", "#f59e0b"},
		"green": {"#dcfce7", "#15803d", "#22c55e"},
		"slate": {"#f1f5f9", "#475569", "#64748b"},
		"red":   {"#fee2e2", "#b91c1c", "#ef4444"},
	}[name]
	t := tone{ui.Hex(hex[0]), ui.Hex(hex[1]), ui.Hex(hex[2])}
	if p.dark {
		// The bar's color carries the tone in the dark, washed for the pill.
		t.bg, t.text = t.bar.Alpha(0.18), t.bar.Mix(ui.RGB(255, 255, 255), 0.45)
		if name == "slate" {
			t.bg, t.text = ui.RGBA(255, 255, 255, 0.1), ui.RGB(203, 213, 225)
		}
	}
	return t
}

// modalSurface is the color a dialog's panel is painted in: the card's tone,
// fully opaque, so the page behind it cannot show through the dialog.
func modalSurface(p palette) ui.Color {
	if p.dark {
		return ui.RGB(17, 17, 26)
	}
	return ui.RGB(255, 255, 255)
}

// severityTone is the tone a finding's severity reads in.
func (p palette) severityTone(sev string) tone {
	switch sev {
	case "S0":
		return p.tone("red")
	case "S1":
		return p.tone("amber")
	case "S2":
		return p.tone("blue")
	default:
		return p.tone("slate")
	}
}

// hsl is CSS's hsl(): h in degrees, s and l from 0 to 1.
func hsl(h, s, l float32) ui.Color {
	h = float32(math.Mod(float64(h), 360))
	if h < 0 {
		h += 360
	}
	c := (1 - float32(math.Abs(float64(2*l-1)))) * s
	x := c * (1 - float32(math.Abs(math.Mod(float64(h)/60, 2)-1)))
	m := l - c/2
	var r, g, b float32
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	to8 := func(v float32) uint8 { return uint8(math.Round(float64((v + m) * 255))) }
	return ui.RGB(to8(r), to8(g), to8(b))
}
