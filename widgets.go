package main

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// iconButton is a round button showing only an icon, named by label for
// assistive technology and its tooltip.
func iconButton(c *ui.Context, p palette, name, label string) ui.Element {
	b := ui.ButtonBase(c).Size(32, 32).Radius(16).Center().Label(label).Tooltip(label).TextColor(p.text)
	if b.Hovered() {
		b.Background(p.hover)
	}
	return b.Children(func() { ui.Icon(c, icon(name)).FontSize(17) })
}

// pill is a rounded box of the status bar and the page headers.
func pill(c *ui.Context, p palette) ui.Element {
	return ui.Row(c).Height(28).Padding(0, 12).Gap(7).Radius(14).AlignItems(ui.Center).
		Background(p.hover).FontSize(12.5).FontWeight(500)
}

// dot is a small round light, as of a severity or a state.
func dot(c *ui.Context, color ui.Color) ui.Element {
	return ui.Box(c).Size(8, 8).Radius(4).Background(color)
}

// caption is the small capital label that heads a tile.
func caption(c *ui.Context, p palette, s string) ui.Element {
	return ui.Text(c, s).FontSize(11).FontWeight(600).LetterSpacing(0.8).TextColor(p.muted).SingleLine()
}

// tile is a card of the dashboard and the overview: a raised panel.
func tile(c *ui.Context, p palette) ui.Element {
	return ui.Column(c).MinHeight(150).Padding(16).Radius(18).Background(p.tile).Clip().
		Shadow(0, 1, 2, 0, p.shadow).Shadow(0, 10, 28, -12, p.shadow)
}

// statusPill is a status in a washed pill of its tone, as Motrix's rows.
func statusPill(c *ui.Context, p palette, t tone, label string) ui.Element {
	return ui.Text(c, label).FontSize(11).FontWeight(500).Padding(2, 8).Radius(10).
		Background(t.bg).TextColor(t.text).SingleLine()
}

// sepLine is a hairline.
func sepLine(c *ui.Context, p palette) {
	ui.Divider(c).Background(p.border)
}

// statusText is a severity dot with its label, for lines without room
// for a pill.
func statusText(c *ui.Context, p palette, color ui.Color, label string) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		dot(c, color)
		ui.Text(c, label).FontSize(12.5).TextColor(p.muted).SingleLine()
	})
}

// detail is a label over a value, as an inspector's block.
func detail(c *ui.Context, p palette, label, value string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, label).FontSize(11.5).TextColor(p.muted)
		ui.Text(c, value).FontSize(12.5).Selectable().LineHeight(1.35)
	})
}

// sectionTitle is a caption over a form or a block.
func sectionTitle(c *ui.Context, p palette, s string) {
	caption(c, p, s).Margin(0, 0, 8, 0)
}

// emptyState is a title over a line of explanation, centered in the room
// its parent leaves.
func emptyState(c *ui.Context, p palette, iconName, title, detail string) {
	ui.Column(c).Grow(1).Center().Gap(10).Padding(32).Children(func() {
		if iconName != "" {
			ui.Icon(c, icon(iconName)).FontSize(34).TextColor(p.muted).Margin(0, 0, 6, 0)
		}
		ui.Text(c, title).FontSize(19).FontWeight(600).TextAlign(ui.Center)
		if detail != "" {
			ui.Text(c, detail).FontSize(14.5).TextColor(p.muted).TextAlign(ui.Center).MaxWidth(420).LineHeight(1.45)
		}
	})
}

// emptyMosaic draws the squares of the empty state, in the accent colour
// from the accent to pink, fading away from the middle of the band, drawn
// behind what the page puts over them.
func emptyMosaic(c *ui.Context, p palette, accent, second ui.Color) {
	ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).HeightPercent(52).PassThrough().
		Draw(func(pt *ui.Painter, r ui.Rect) { drawMosaic(pt, r, p.dark, accent, second) })
}

// drawMosaic draws the squares of emptyMosaic, 28 DIPs each, along the
// bottom of r, brightest at the middle of its width.
func drawMosaic(pt *ui.Painter, r ui.Rect, dark bool, accent, second ui.Color) {
	const cell = 28
	strength := float32(0.9)
	if dark {
		strength = 0.55
	}
	cols := int(min(r.W, 620) / cell)
	rows := int(r.H / cell)
	if cols < 2 || rows < 1 {
		return
	}
	left := r.X + (r.W-float32(cols*cell))/2
	bottom := r.Y + r.H
	for gy := range rows {
		for gx := range cols {
			u := (float32(gx) + 0.5) / float32(cols)
			v := (float32(gy) + 0.5) / float32(rows) // 0 at the bottom
			dx := (u - 0.5) * 2
			dist := float32(math.Sqrt(float64(dx*dx + v*v)))
			if dist >= 1 {
				continue
			}
			alpha := float32(math.Pow(float64(1-dist), 1.6)) * strength
			t := min(max((u-0.28)/0.44, 0), 1)
			color := accent.Mix(second, min(t*2, 1))
			if t > 0.5 {
				color = second.Mix(accent.Mix(ui.RGB(255, 255, 255), 0.35), (t-0.5)*2)
			}
			pt.Fill(ui.Rect{X: left + float32(gx*cell), Y: bottom - float32((gy+1)*cell), W: cell, H: cell}, color.Alpha(alpha), 0)
		}
	}
}
