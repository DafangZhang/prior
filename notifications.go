package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *app) notificationsView(c *ui.Context, p palette) {
	unread := a.unreadNotes()

	a.pageHeader(c, p, T("Notifications"), func() {
		ui.Textf(c, "%d/%d", unread, len(a.notes)).FontSize(12.5).FontWeight(600).FontFeatures("tnum").
			Padding(2, 9).Radius(11).Border(1, p.border).SingleLine()
		ui.Spacer(c)
		a.toolbar(c, p, []pageAction{
			{"check", T("Mark all read"), false, nil, func() { a.markAllNotes(true) }},
			{"ellipsis", T("More actions"), false, func(c *ui.Context, m *ui.Menu) {
				if m.Item(T("Mark all as read")).Disabled(len(a.notes) == 0).Chosen() {
					a.markAllNotes(true)
				}
				if m.Item(T("Mark all as unread")).Disabled(len(a.notes) == 0).Chosen() {
					a.markAllNotes(false)
				}
				m.Separator()
				if m.Item(T("Copy all titles")).Disabled(len(a.notes) == 0).Chosen() {
					var b strings.Builder
					for _, n := range a.notes {
						b.WriteString(n.Title + "\t" + n.Time + "\n")
					}
					c.WriteClipboard(b.String())
					a.flash = T("Copied") + " " + itoa(len(a.notes)) + " " + T("notifications")
				}
			}, nil},
		})
	})
	ui.Divider(c).Background(p.border)

	if len(a.notes) == 0 {
		emptyState(c, p, "bell", T("No notifications"), T("Finished analyses show up here"))
		return
	}

	ui.List(c.Key("notes"), &a.noteList, len(a.notes), func(i int) {
		a.noteRow(c, p, a.notes[i])
	}).Grow(1)
	a.statusBar(c, p, []statusItem{
		{T("Unread") + " " + itoa(unread), p.accent},
		{T("Notifications") + " " + itoa(len(a.notes)), ui.Color{}},
	}, nil)
}

// unreadNotes is how many notifications have not been read, which is what the
// sidebar's badge counts: a badge says how much still wants attention, not how
// long the list is.
func (a *app) unreadNotes() int {
	n := 0
	for _, note := range a.notes {
		if !note.Read {
			n++
		}
	}
	return n
}

// markAllNotes sets every notification's read state, as the header's
// button and menu do.
func (a *app) markAllNotes(read bool) {
	for i := range a.notes {
		a.notes[i].Read = read
	}
	if read {
		a.flash = T("All marked as read")
		return
	}
	a.flash = T("All marked as unread")
}

// noteMenu fills the menu of a notification, from its row's context menu.
func (a *app) noteMenu(c *ui.Context, m *ui.Menu, n note) {
	if m.Item(T("View report")).Chosen() {
		a.router.Push("/report/" + n.ReportID)
	}
	if m.Item(T("Copy title")).Chosen() {
		c.WriteClipboard(n.Title)
		a.flash = T("Title copied")
	}
	m.Separator()
	if m.Item(ternary(n.Read, T("Mark as unread"), T("Mark as read"))).Chosen() {
		a.markNote(n.ID, !n.Read)
	}
}

// markNote sets a notification's read state, wherever the note is held.
func (a *app) markNote(id int64, read bool) {
	for i := range a.notes {
		if a.notes[i].ID == id {
			a.notes[i].Read = read
		}
	}
}

// noteRow is one notification, on a hairline of the list.
func (a *app) noteRow(c *ui.Context, p palette, n note) {
	tn := p.tone("blue")
	iconName := "bell"
	if n.Read {
		tn = p.tone("slate")
		iconName = "check"
	}
	row := ui.Row(c.Key(n.ID)).Padding(12, 24).Gap(14).AlignItems(ui.Start).
		BorderWidth(0, 0, 1, 0).BorderColor(p.border)
	if row.Hovered() {
		row.Background(p.hover)
	}
	row.ContextMenu(func(m *ui.Menu) { a.noteMenu(c, m, n) })
	row.Children(func() {
		ui.Icon(c, icon(iconName)).FontSize(20).TextColor(tn.bar).Margin(2, 0, 0, 0)
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, n.Title).FontSize(14).FontWeight(600)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				statusPill(c, p, tn, ternary(n.Read, T("Read"), T("Unread")))
				ui.Text(c, n.Time).FontSize(12.5).TextColor(p.muted).FontFeatures("tnum").SingleLine()
			})
		})
		ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
			iconButton(c, p, "chevron-right", T("View report")).OnClick(func() {
				a.router.Push("/report/" + n.ReportID)
			})
			iconButton(c, p, "ellipsis", T("More actions")).Menu(func(m *ui.Menu) { a.noteMenu(c, m, n) })
		})
	})
	if row.Clicked() {
		a.markNote(n.ID, true)
	}
}

func ternary(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}
