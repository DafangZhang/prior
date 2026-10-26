package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"prior/store"
)

// titleBarHeight is the window's title bar height (main.go): the room the
// window controls take, which the sidebar and the page headers keep clear
// of.
const titleBarHeight = 52

// sidebarWidth is the width the sidebar takes when it is shown.
const sidebarWidth = 200

type app struct {
	win      *mygo.Window
	router   *ui.Router
	set      settings
	docs     []doc
	runs     []run
	findings []finding
	notes    []note

	currentDoc int64
	flash      string

	cmdOpen   bool
	cmdQuery  string
	cmdRow    int
	helpOpen  bool
	searching bool

	// settingsPath is the file the settings save to, empty in tests, which
	// keep off the user's configuration.
	settingsPath string

	// store is the knowledge base, nil until PostgreSQL answers; env is
	// the state of the connections; the jobs below are what the window
	// shows while they run.
	store *store.Store
	// storeDSN is the DSN the store was opened with, which says whether a
	// connection the settings now spell is the one answering.
	storeDSN string
	env      env

	ingest ingestJob

	analyzing     bool
	analyzeDoc    string
	analyzeStage  string
	analyzeLines  []string
	analyzeCancel context.CancelFunc
	// ledger is the open report's change records, and pendingAll is how many
	// findings across every run are still open.
	ledger     []store.ChangeRecord
	pendingAll int
	// currentChunks are the chunks of the document the inspector shows,
	// and reportRun is the run the open report shows.
	currentChunks []store.Chunk
	reportRun     int64
	// reportOpen says the report is showing in its dialog, reportLoaded that
	// its findings were read, reportErr why they could not be, and
	// reportSelected the finding whose detail the document view shows.
	reportOpen     bool
	reportLoaded   bool
	reportErr      string
	reportSelected string
	// preReview is the verdict the report's run drew on its document, and
	// hasPreReview says the run has one: a run from before the pre-review
	// existed has none, and the page says so rather than showing a blank.
	preReview    store.PreReview
	hasPreReview bool
	// reviewDocID is the document the report's text view holds, reviewChunks
	// its chunks, and reviewMarks its found quotes: read once per document
	// rather than every frame.
	reviewDocID   int64
	reviewChunks  []store.Chunk
	reviewText    string
	reviewSpans   []chunkSpan
	reviewMarks   []mark
	reviewMarkKey string

	// deleteDoc is the document a row's delete is confirming, and
	// deleteOpen the alert it is confirmed in.
	deleteDoc  doc
	deleteOpen bool

	// draft is the settings a section's page edits until saved, filled from
	// the saved settings when the page opens; draftFor is that section.
	draft    settings
	draftFor string

	// pgForm is the connection's fields as the Environment page edits them,
	// filled from the draft's DSN when the page opens; pgFormBase is what
	// that DSN spelled, which is what says the page has been edited.
	pgForm     pgFields
	pgFormBase pgFields

	listState     ui.ListState
	reportList    ui.ListState
	reportRunList ui.ListState
	noteList      ui.ListState
	runList       ui.ListState
	settingsList  ui.ListState
}

func newApp(set settings) *app {
	reportMode, reportTab, reportFilter, reportSearch, reportSearching, reportCurrent =
		modeDocument, -1, "all", "", false, -1
	reportSort, reportSortDesc = "severity", false
	reportListQuery, reportListFilter, reportListSearching = "", "all", false
	a := &app{
		set:      set,
		router:   ui.NewRouter("/library"),
		docs:     fixtureDocs(),
		runs:     fixtureRuns(),
		findings: fixtureFindings(),
		notes:    fixtureNotes(),
	}
	for _, r := range a.runs {
		a.pendingAll += r.Pending
	}
	return a
}

func (a *app) update(fn func()) {
	if a.win == nil {
		fn()
		return
	}
	a.win.Update(fn)
}

func (a *app) try(err error) {
	if err != nil {
		a.flash = err.Error()
	}
}

// applyLanguage has the interface follow the language setting and the
// desktop's locale. Tests, running without a window, have no locale.
func (a *app) applyLanguage() {
	locale := ""
	if a.win != nil {
		locale = mygo.App.Locale()
	}
	setLanguage(a.set.Appearance.Language, locale)
}

// saveSettings keeps the settings, as the look of the window follows them.
func (a *app) saveSettings() {
	if a.settingsPath == "" {
		return
	}
	a.set.save(a.settingsPath)
}

func (a *app) view(c *ui.Context) {
	// A report is read against the list it came from; moving to another page
	// puts it away before the page is built, so it never paints over a page
	// it does not belong to.
	if a.reportOpen && !strings.HasPrefix(a.router.Path(), "/report") && a.router.Path() != "/reports" {
		a.closeReport()
	}
	p := paletteFor(c.Theme(), findSidebarColor(a.set.Appearance.SidebarColor))
	c.SetTheme(p.theme(c.Theme()))
	c.Root().Background(p.appBg).TextColor(p.text)
	if a.flash != "" {
		c.Toast(a.flash)
		a.flash = ""
	}

	ui.Row(c).Grow(1).AlignItems(ui.Stretch).Gradient(p.windowTop, p.windowBottom, 180).Children(func() {
		a.sidebar(c, p)
		a.content(c, p)
	})
	a.chrome(c, p)
	a.reportDialog(c, p)
	a.commandBar(c, p)
	a.helpOverlay(c, p)

	if c.Shortcut(ui.Cmd, ui.KeyB) {
		a.set.UI.SidebarHidden = !a.set.UI.SidebarHidden
		a.saveSettings()
	}
}

// chromeLeft is where the buttons beside the window controls start.
func chromeLeft(c *ui.Context) float32 { return max(c.TitleBar().Left, 8) + 10 }

// trailingInset is the room a page's header leaves at its right: its
// padding, or on Windows and Linux the window controls, which sit over the
// top right corner of the window.
func trailingInset(c *ui.Context) float32 {
	if right := c.TitleBar().Right; right > 0 {
		return right + 12
	}
	return 24
}

// chrome builds the buttons that sit beside the window controls: the one
// showing and hiding the sidebar, and the page's main action.
func (a *app) chrome(c *ui.Context, p palette) {
	ui.Row(c).Absolute().Top(0).Left(chromeLeft(c)).Height(titleBarHeight).Gap(4).AlignItems(ui.Center).Children(func() {
		if iconButton(c, p, "panel-left", T("Toggle sidebar (Cmd+Shift+B)")).Clicked() {
			a.set.UI.SidebarHidden = !a.set.UI.SidebarHidden
			a.saveSettings()
		}
		if a.router.Path() == "/library" {
			if iconButton(c, p, "plus", T("Import document")).Clicked() {
				a.flash = T("Importing is not available yet")
			}
		}
	})
}

// sidebarShown is the width the sidebar takes, none while it is hidden.
func (a *app) sidebarShown() float32 {
	if a.set.UI.SidebarHidden {
		return 0
	}
	return sidebarWidth
}

func (a *app) sidebar(c *ui.Context, p palette) {
	side := ui.Column(c).Clip()
	side.Width(side.Animate("width", a.sidebarShown(), 180*time.Millisecond))
	side.Children(func() {
		// As wide as the sidebar when shown, so that it slides out whole.
		ui.Column(c).Width(sidebarWidth).Grow(1).Padding(titleBarHeight+4, 10, 10, 10).Gap(4).DragWindow().Children(func() {
			a.navItem(c, p, "/library", "library", T("Library"), "Cmd+1", 0)
			a.navItem(c, p, "/analyze", "scan", T("Analyze"), "Cmd+2", 0)
			a.navItem(c, p, "/reports", "alert", T("Report"), "Cmd+3", a.pendingAll)
			a.navItem(c, p, "/modules", "layers", T("Modules"), "Cmd+6", 0)
			a.navItem(c, p, "/history", "history", T("History"), "Cmd+4", 0)
			ui.Spacer(c)
			a.navItem(c, p, "/notifications", "bell", T("Notifications"), "Cmd+5", a.unreadNotes())
			ui.Divider(c).Background(p.border).Margin(4, 0)
			a.navItem(c, p, "/settings", "settings", T("Settings"), "Cmd+,", 0)
		})
	})
}

func (a *app) navItem(c *ui.Context, p palette, path, iconName, label, shortcut string, badge int) {
	at := a.router.Path()
	active := at == path || strings.HasPrefix(at, path+"/") ||
		(path == "/reports" && strings.HasPrefix(at, "/report/"))
	b := ui.ButtonBase(c.Key(path)).Height(38).Padding(0, 10).Gap(10).Radius(8).AlignItems(ui.Center).
		TextColor(p.text).Label(label).Tooltip(label + "  " + shortcut)
	switch {
	case active:
		b.Background(p.active)
	case b.Hovered():
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, icon(iconName)).FontSize(18)
		ui.Text(c, label).FontSize(14.5).FontWeight(500).SingleLine().Grow(1)
		if badge > 0 {
			ui.Textf(c, "%d", badge).FontSize(11).FontWeight(600).Padding(1, 6).Radius(9).
				Background(p.accent).TextColor(p.accentText)
		}
	})
	if b.Clicked() && at != path {
		a.router.Push(path)
	}
}

// content builds the page the router shows, on its panel.
func (a *app) content(c *ui.Context, p palette) {
	panel := ui.Column(c).Grow(1).MinWidth(0)
	left := float32(0)
	if a.set.UI.SidebarHidden {
		left = 8
	}
	panel.Margin(8, 8, 8, left).Radius(14).Background(p.card).Clip().Shadow(0, 1, 3, 0, p.shadow)
	panel.Children(func() {
		a.router.View(c, func(r *ui.Route) {
			r.Page().Grow(1)
			switch {
			case r.Match("/library"):
				a.libraryView(c, p)
			case r.Match("/analyze"):
				a.analyzeView(c, p)
			case r.Match("/reports"):
				a.reportListView(c, p)
			case r.Match("/report/{runID}"):
				if id, err := strconv.ParseInt(r.Param("runID"), 10, 64); err == nil {
					a.openReport(id)
				}
				a.reportListView(c, p)
			case r.Match("/history"):
				a.historyView(c, p)
			case r.Match("/modules"):
				a.loadModuleNames()
				a.moduleView(c, p)
			case r.Match("/notifications"):
				a.notificationsView(c, p)
			case r.Match("/settings"):
				a.settingsView(c, p, "")
			case r.Match("/settings/{section}"):
				a.settingsView(c, p, r.Param("section"))
			default:
				a.router.Replace("/library")
			}
		}).Grow(1)
	})
}

// pageHeader builds the band at the top of a page: its title, then what
// trailing builds. It drags the window, as a title bar, and keeps clear of
// the window controls while the sidebar is hidden.
func (a *app) pageHeader(c *ui.Context, p palette, title string, trailing func()) ui.Element {
	row := a.headerRow(c, 76, 24)
	return row.Gap(10).AlignItems(ui.Center).DragWindow().Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(24).Bold().LetterSpacing(-0.4).SingleLine()
		}
		if trailing != nil {
			trailing()
		}
	})
}

// headerRow is the row of a page's header, height tall and padded left
// beside the sidebar. While the sidebar is hidden it moves below the title
// bar, under the window controls and the buttons beside them, and starts at
// the window's edge.
func (a *app) headerRow(c *ui.Context, height, left float32) ui.Element {
	row := ui.Row(c)
	top, right := float32(8), trailingInset(c)
	if a.set.UI.SidebarHidden {
		top, right, left, height = titleBarHeight-8, 24, max(left, 22), height+20
	}
	const d = 180 * time.Millisecond
	return row.Height(row.Animate("height", height, d)).
		Padding(row.Animate("top", top, d), right, 0, row.Animate("left", left, d))
}

// pageAction is one entry of a page header's right-hand toolbar.
type pageAction struct {
	icon    string
	tip     string
	primary bool
	menu    func(c *ui.Context, m *ui.Menu)
	run     func()
}

// toolbar is the group of round buttons at a page header's right, on a
// raised pill.
func (a *app) toolbar(c *ui.Context, p palette, items []pageAction) {
	ui.Row(c).Height(38).Padding(0, 3).Radius(19).AlignItems(ui.Center).Gap(2).
		Background(p.tile).Border(1, p.border).Shadow(0, 1, 2, 0, p.shadow).
		Children(func() {
			for _, it := range items {
				item := it
				if item.primary {
					ui.PrimaryButton(c, item.tip).Height(32).Radius(16).Padding(0, 12).OnClick(item.run)
					continue
				}
				b := iconButton(c, p, item.icon, item.tip)
				if item.menu != nil {
					b.Menu(func(m *ui.Menu) { item.menu(c, m) })
				}
				b.OnClick(item.run)
			}
		})
}

// overlayOpen says a dialog or palette is showing over the page, whose own
// shortcuts must not run under it.
func (a *app) overlayOpen() bool {
	return a.reportOpen || a.cmdOpen || a.helpOpen || a.deleteOpen
}

// searchToggle is the round button that shows and hides a page's search
// field, washed while the search shows. shortcut says the page is the one in
// front: a search field behind a dialog does not take Cmd+F.
func (a *app) searchToggle(c *ui.Context, p palette, searching *bool, query *string, label string, shortcut bool) {
	b := iconButton(c, p, "search", label).Size(38, 38).Radius(19).
		Background(p.tile).Border(1, p.border).Shadow(0, 1, 2, 0, p.shadow)
	if *searching {
		b.Background(p.active)
	}
	if b.Clicked() || (shortcut && c.Shortcut(ui.Cmd, ui.KeyF)) {
		*searching = !*searching
		if !*searching {
			*query = ""
		}
	}
}

// statusBar is the band under a page's content: figures of what the page
// holds, as pills, what trailing builds, and the knowledge base's state at
// its right.
func (a *app) statusBar(c *ui.Context, p palette, items []statusItem, trailing func()) {
	ui.Row(c).Height(52).Padding(0, 24, 4, 24).Gap(8).AlignItems(ui.Center).Children(func() {
		for _, it := range items {
			item := it
			entry := pill(c, p).FontFeatures("tnum")
			if item.color.A > 0 {
				entry.Children(func() {
					dot(c, item.color)
					ui.Text(c, item.label).SingleLine()
				})
				continue
			}
			entry.Children(func() { ui.Text(c, item.label).SingleLine() })
		}
		ui.Spacer(c)
		if trailing != nil {
			trailing()
		}
		a.knowledgePill(c, p)
	})
}

// knowledgePill is the state of the knowledge base the app works on: the
// documents it holds, whether PostgreSQL answers, and whether the two
// models are there. A click goes to the environment page that says how to
// fix what is missing.
func (a *app) knowledgePill(c *ui.Context, p palette) {
	label, light, tip := T("Knowledge base ready"), p.success, T("PostgreSQL and Ollama state")
	switch {
	case a.store == nil:
		label, light = T("Knowledge base not connected"), p.tone("red").bar
		tip = a.env.pgErr
		if tip == "" {
			tip = T("PostgreSQL did not answer; the environment page has the details.")
		}
	case len(a.docs) == 0:
		label, light = T("Knowledge base is empty"), p.muted
	case len(a.env.missing) > 0:
		label, light = T("A model is missing"), p.tone("amber").bar
		tip = "ollama pull " + strings.Join(a.env.missing, " && ollama pull ")
	case a.env.llmErr != "":
		label, light = T("The language model did not answer"), p.tone("amber").bar
		tip = a.env.llmWhere + "\n" + a.env.llmErr
	}
	b := pill(c, p).Label(label).Tooltip(tip).Margin(0, 0, 0, 12)
	width, _ := c.Size()
	b.Children(func() {
		dot(c, light)
		ui.Text(c, label).SingleLine()
		if width < 1120 {
			// The state is what matters at the window's least width; the
			// counts and the model name would be cut to ellipses.
			return
		}
		ui.Text(c, "·").TextColor(p.muted)
		ui.Textf(c, T("%d docs / %d chunks"), len(a.docs), a.chunkCount()).TextColor(p.muted).FontFeatures("tnum").SingleLine()
		if a.store != nil && a.env.pgVector {
			ui.Text(c, "· pgvector").TextColor(p.muted).SingleLine()
		}
		if llmName := llmClient(a.set).Model(); llmName != "" {
			ui.Text(c, "· "+llmName).TextColor(p.muted).SingleLine()
		}
	})
	if b.Clicked() {
		a.router.Push("/settings/env")
	}
}

// jobStrip is what a running ingest or review shows on the page: the stage
// it reached, the lines it wrote, and the way to stop it.
func (a *app) jobStrip(c *ui.Context, p palette) {
	switch {
	case a.ingest.running:
		strip := ui.Row(c).Padding(10, 24).Gap(12).AlignItems(ui.Center).
			Background(p.active).BorderWidth(1, 0, 1, 0).BorderColor(p.border)
		strip.Children(func() {
			ui.Spinner(c)
			ui.Textf(c, T("Ingesting %s"), a.ingest.root).SingleLine()
			ui.Text(c, a.ingest.message).FontSize(12.5).TextColor(p.muted).SingleLine().Grow(1).Basis(0).MinWidth(0)
			if a.ingest.total > 0 {
				ui.Textf(c, "%d/%d", a.ingest.done, a.ingest.total).FontFeatures("tnum").FontSize(12.5).TextColor(p.muted)
			}
			ui.Button(c, T("Cancel")).OnClick(func() {
				if a.ingest.cancel != nil {
					a.ingest.cancel()
				}
			})
		})
	case a.analyzing:
		strip := ui.Row(c).Padding(10, 24).Gap(12).AlignItems(ui.Center).
			Background(p.active).BorderWidth(1, 0, 1, 0).BorderColor(p.border)
		strip.Children(func() {
			ui.Spinner(c)
			ui.Text(c, T("Analyzing")+" "+a.analyzeDoc).SingleLine()
			ui.Text(c, a.analyzeStage+" ").FontSize(12.5).TextColor(p.muted).SingleLine()
			if n := len(a.analyzeLines); n > 0 {
				ui.Text(c, a.analyzeLines[n-1]).FontSize(12.5).TextColor(p.muted).SingleLine().Grow(1).Basis(0).MinWidth(0)
			} else {
				ui.Spacer(c)
			}
			ui.Button(c, T("Cancel")).OnClick(func() {
				if a.analyzeCancel != nil {
					a.analyzeCancel()
				}
			})
		})
	}
}

func (a *app) chunkCount() int {
	n := 0
	for _, d := range a.docs {
		n += d.Chunks
	}
	return n
}

// inspectorToggle is the status bar's control showing and hiding the page's
// details panel.
func (a *app) inspectorToggle(c *ui.Context, p palette) {
	inspector := pill(c, p)
	if a.set.UI.Inspector {
		inspector.Background(p.active)
	}
	inspector.Children(func() {
		ui.Icon(c, icon("panel-right")).FontSize(15)
		ui.Text(c, T("Details panel")).SingleLine()
	})
	if inspector.Clicked() {
		a.set.UI.Inspector = !a.set.UI.Inspector
		a.saveSettings()
	}
}

type statusItem struct {
	label string
	color ui.Color
}

func (a *app) commandBar(c *ui.Context, p palette) {
	if !a.cmdOpen {
		return
	}

	ui.DialogBase(c, &a.cmdOpen, func(backdrop, panel ui.Element) {
		backdrop.OnClick(func() { a.cmdOpen = false })

		panel.
			Width(560).
			MaxHeight(440).
			Radius(16).
			Background(p.card).
			Shadow(0, 4, 24, 0, p.shadow).
			Padding(12).
			Children(func() {
				ui.TextInputBase(c, &a.cmdQuery).
					AutoFocus().
					Placeholder(T("Input a command...")).
					FontSize(14).
					Padding(8, 12).
					Margin(0, 0, 8, 0)

				cmds := a.filterCommands(a.cmdQuery)
				ui.List(c, &a.reportList, len(cmds), func(i int) {
					cmd := cmds[i]
					row := ui.ButtonBase(c).Padding(8, 12).Radius(8).AlignItems(ui.Center).Gap(8)
					if row.Hovered() {
						row.Background(p.hover)
					}
					row.Children(func() {
						ui.Text(c, cmd.title).FontSize(14).TextColor(p.text)
						if cmd.hint != "" {
							ui.Text(c, cmd.hint).FontSize(12.5).TextColor(p.muted)
						}
						ui.Spacer(c)
						if cmd.shortcut != "" {
							ui.Text(c, cmd.shortcut).FontSize(11).TextColor(p.muted)
						}
					}).OnClick(func() {
						a.cmdOpen = false
						cmd.run()
					})
				})
			})
	})
}

func (a *app) helpOverlay(c *ui.Context, p palette) {
	if !a.helpOpen {
		return
	}

	ui.DialogBase(c, &a.helpOpen, func(backdrop, panel ui.Element) {
		backdrop.OnClick(func() { a.helpOpen = false })

		panel.
			Width(480).
			MaxHeight(400).
			Radius(16).
			Background(p.card).
			Shadow(0, 4, 24, 0, p.shadow).
			Padding(16).
			Children(func() {
				ui.Text(c, T("Keyboard shortcuts")).FontSize(15).FontWeight(600).Margin(0, 0, 12, 0)

				shortcuts := [][2]string{
					{T("Command palette"), "Cmd+K"},
					{T("Library"), "Cmd+1"},
					{T("Analyze"), "Cmd+2"},
					{T("Report"), "Cmd+3"},
					{T("History"), "Cmd+4"},
					{T("Notifications"), "Cmd+5"},
					{T("Search"), "Cmd+F"},
					{"Toggle sidebar", "Cmd+Shift+B"},
					{"Details panel", "Cmd+I"},
					{T("Previous or next finding"), "J / K"},
					{T("Save"), "Cmd+S"},
					{T("Help"), "Shift+?"},
				}
				for _, s := range shortcuts {
					ui.Row(c).
						Padding(6, 0).
						AlignItems(ui.Center).
						Justify(ui.SpaceBetween).
						Children(func() {
							ui.Text(c, s[0]).FontSize(12.5).TextColor(p.text)
							ui.Text(c, s[1]).FontSize(11).TextColor(p.muted)
						})
				}
			})
	})
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func fuzzyMatch(s, q string) bool {
	if q == "" {
		return true
	}
	si, qi := 0, 0
	for si < len(s) && qi < len(q) {
		if s[si] == q[qi] {
			qi++
		}
		si++
	}
	return qi == len(q)
}

type command struct {
	title    string
	hint     string
	shortcut string
	run      func()
}

func (a *app) allCommands() []command {
	return []command{
		{T("Library"), T("Go to library"), "Cmd+1", func() { a.router.Push("/library") }},
		{T("Analyze"), T("Go to analyze"), "Cmd+2", func() { a.router.Push("/analyze") }},
		{T("Report"), T("Go to report"), "Cmd+3", func() { a.router.Push("/reports") }},
		{T("History"), T("Go to history"), "Cmd+4", func() { a.router.Push("/history") }},
		{T("Modules"), T("Go to module history"), "Cmd+6", func() { a.router.Push("/modules") }},
		{T("Notifications"), T("Go to notifications"), "Cmd+5", func() { a.router.Push("/notifications") }},
		{T("Settings"), T("Open settings"), "", func() { a.router.Push("/settings") }},
		{T("Show or hide the sidebar"), T("Show or hide the sidebar"), "Cmd+Shift+B", func() {
			a.set.UI.SidebarHidden = !a.set.UI.SidebarHidden
			a.saveSettings()
		}},
		{T("Show or hide the details panel"), T("Show or hide the details panel"), "Cmd+I", func() {
			a.set.UI.Inspector = !a.set.UI.Inspector
			a.saveSettings()
		}},
		{T("Sidebar color: blue"), T("Tint the sidebar blue"), "", func() { a.setSidebarColor("blue") }},
		{T("Sidebar color: green"), T("Tint the sidebar green"), "", func() { a.setSidebarColor("green") }},
		{T("Sidebar color: violet"), T("Tint the sidebar violet"), "", func() { a.setSidebarColor("violet") }},
		{T("Sidebar color: gray"), T("Tint the sidebar gray"), "", func() { a.setSidebarColor("gray") }},
		{T("Quit Prior"), T("Quit the app"), "", func() {
			if a.win != nil {
				a.win.Close()
			}
		}},
		{T("Switch theme"), T("Switch between light and dark"), "Cmd+Shift+T", func() {
			if a.set.Appearance.Theme == "dark" {
				a.set.Appearance.Theme = "light"
				mygo.Theme.SetSource(mygo.ThemeLight)
			} else {
				a.set.Appearance.Theme = "dark"
				mygo.Theme.SetSource(mygo.ThemeDark)
			}
			a.saveSettings()
		}},
		{T("Shortcut help"), T("Show the shortcut list"), "Shift+?", func() { a.helpOpen = true }},
	}
}

// setSidebarColor changes the tint of the window and keeps it.
func (a *app) setSidebarColor(name string) {
	a.set.Appearance.SidebarColor = name
	a.saveSettings()
	a.flash = T("Sidebar color changed to") + name
}

func (a *app) filterCommands(q string) []command {
	if q == "" {
		return a.allCommands()
	}
	var out []command
	for _, cmd := range a.allCommands() {
		if fuzzyMatch(cmd.title, q) || fuzzyMatch(cmd.hint, q) {
			out = append(out, cmd)
		}
	}
	return out
}
