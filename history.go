package main

import (
	"github.com/egoist/mygo/ui"
)

func (a *app) historyView(c *ui.Context, p palette) {
	done, failed, degraded := 0, 0, 0
	for _, r := range a.runs {
		switch r.Status {
		case "succeeded", "completed":
			done++
		case "degraded":
			degraded++
		case "failed":
			failed++
		}
	}

	a.pageHeader(c, p, T("History"), func() {
		ui.Textf(c, "%d/%d", done, len(a.runs)).FontSize(12.5).FontWeight(600).FontFeatures("tnum").
			Padding(2, 9).Radius(11).Border(1, p.border).SingleLine()
		ui.Spacer(c)
		a.toolbar(c, p, []pageAction{
			{"history", T("Rerun all"), false, nil, func() { a.flash = T("Rerunning is not available yet") }},
		})
	})
	ui.Divider(c).Background(p.border)

	if len(a.runs) == 0 {
		emptyState(c, p, "history", T("No analysis history"), T("Runs show up here after an analysis"))
		return
	}

	ui.List(c.Key("runs"), &a.runList, len(a.runs), func(i int) {
		a.runRow(c, p, a.runs[i])
	}).Grow(1)

	a.statusBar(c, p, []statusItem{
		{T("Runs") + " " + itoa(len(a.runs)), ui.Color{}},
		{T("Completed") + " " + itoa(done), p.success},
		{T("Degraded") + " " + itoa(degraded), p.tone("amber").bar},
		{T("Failed") + " " + itoa(failed), p.muted},
	}, nil)
}

// runMenu fills the menu of a run, from its row's context menu.
func (a *app) runMenu(c *ui.Context, m *ui.Menu, r run) {
	if m.Item(T("Open report")).Chosen() {
		a.router.Push("/report/" + itoa64(r.ID))
	}
	if m.Item(T("Rerun")).Chosen() {
		a.flash = T("Rerunning is not available yet")
	}
	if m.Item(T("Copy title")).Chosen() {
		c.WriteClipboard(r.Title)
		a.flash = T("Title copied")
	}
	m.Separator()
	if m.Item(T("Delete")).Chosen() {
		a.flash = T("Deleting is not available yet")
	}
}

// runRow is one analysis run, on a hairline of the list.
func (a *app) runRow(c *ui.Context, p palette, r run) {
	tn := runTone(p, r.Status)
	row := ui.Row(c.Key(r.ID)).Padding(12, 24).Gap(14).AlignItems(ui.Start).
		BorderWidth(0, 0, 1, 0).BorderColor(p.border)
	if row.Hovered() {
		row.Background(p.hover)
	}
	row.ContextMenu(func(m *ui.Menu) { a.runMenu(c, m, r) })
	row.Children(func() {
		statusPill(c, p, tn, runStatusLabel(r.Status))
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, r.Title).FontSize(14).FontWeight(600)
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, r.StartedAt).FontSize(12.5).TextColor(p.muted).FontFeatures("tnum").SingleLine()
				ui.Text(c, T("Elapsed")+" "+r.Elapsed).FontSize(12.5).TextColor(p.muted).FontFeatures("tnum").SingleLine()
				for sev, count := range r.Findings {
					if count == 0 {
						continue
					}
					statusText(c, p, p.severity("S"+itoa(sev)), "S"+itoa(sev)+" "+itoa(count))
				}
			})
		})
		ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
			iconButton(c, p, "copy", T("Copy title")).OnClick(func() {
				c.WriteClipboard(r.Title)
				a.flash = T("Title copied")
			})
			iconButton(c, p, "ellipsis", T("More actions")).Menu(func(m *ui.Menu) { a.runMenu(c, m, r) })
		})
	})
	if row.Clicked() {
		a.router.Push("/report/" + itoa64(r.ID))
	}
}

// runTone is the tone a run's status reads in.
func runTone(p palette, status string) tone {
	switch status {
	case "succeeded", "completed":
		return p.tone("green")
	case "degraded":
		return p.tone("amber")
	case "failed":
		return p.tone("red")
	case "canceled":
		return p.tone("amber")
	default:
		return p.tone("slate")
	}
}
