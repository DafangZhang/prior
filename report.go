package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"prior/store"
)

var (
	// reportMode is what the open report shows: the document with its
	// problems highlighted, the findings as a list, the ledger, the module's
	// history, or the pre-review's verdict.
	reportMode      = modeDocument
	reportTab       = -1
	reportFilter    string
	reportSearch    string
	reportSearching bool
	reportCurrent   = -1
	reportSort      = "severity"
	reportSortDesc  bool
)

// The report's views. The document is what the report opens on: a review is
// read where its problems sit, not only as a list of titles. The menu beside
// it chooses the view, so that switching what is shown never changes the
// document under the reader's cursor.
const (
	modeDocument = "document"
	modeFindings = "findings"
	modeLedger   = "ledger"
	modeModule   = "module"
	modeVerdict  = "verdict"
)

func init() {
	reportMode = modeDocument
	reportTab = -1
	reportFilter = "all"
	reportSearch = ""
	reportCurrent = -1
}

// pendingFindings counts the open report's findings no one confirmed or
// dismissed yet.
func (a *app) pendingFindings() int {
	n := 0
	for _, f := range a.findings {
		if f.Status == "open" {
			n++
		}
	}
	return n
}

// reportDialog shows the open report over the list it was opened from. The
// report is a dialog rather than a page because a review is read against the
// list of reviews, and returning to that list must not lose the reader's
// place in it.
func (a *app) reportDialog(c *ui.Context, p palette) {
	if !a.reportOpen {
		return
	}
	r, ok := a.openReportRow()
	if !ok {
		a.closeReport()
		return
	}
	width, height := c.Size()
	if width <= 0 || height <= 0 {
		width, height = 1024, 680
	}
	panelW := min(width-64, 1180)
	panelH := min(height-56, 860)
	ui.DialogBase(c, &a.reportOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32)).OnClick(func() { a.closeReport() })
		panel.Width(panelW).Height(panelH).Radius(16).Padding(0).
			Background(modalSurface(p)).Border(1, p.border).Shadow(0, 6, 32, 0, p.shadow).Clip().
			Children(func() { a.reportBody(c, p, r) })
	})
	if !a.reportOpen {
		a.closeReport()
	}
}

// reportBody is the report's frame: its header, the menu of views, and the
// view itself.
func (a *app) reportBody(c *ui.Context, p palette, r run) {
	filtered := a.filterFindings(reportTab, reportFilter, reportSearch)
	if reportCurrent >= len(filtered) {
		reportCurrent = len(filtered) - 1
	}
	a.moveSelection(c)
	// Cmd+F means the findings wherever the reader is: a report opened on
	// the document or the ledger switches to the list with its search open.
	if reportMode != modeFindings && !a.cmdOpen && !a.helpOpen && c.Shortcut(ui.Cmd, ui.KeyF) {
		reportMode, reportTab = modeFindings, -1
		reportSearching = true
	}
	ui.Column(c).Fill().Children(func() {
		a.reportHead(c, p, r)
		ui.Divider(c).Background(p.border)
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			a.reportMenu(c, p)
			a.reportMain(c, p, filtered)
		})
	})
}

// reportHead is the report's title and the run it came from, with the
// actions that belong to the report as a whole.
func (a *app) reportHead(c *ui.Context, p palette, r run) {
	ui.Row(c).Height(72).Padding(16, 20, 10, 20).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, r.Title).FontSize(17).FontWeight(600).SingleLine().Tooltip(r.Title)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				statusPill(c, p, runTone(p, r.Status), runStatusLabel(r.Status))
				if r.Verdict != "" {
					statusPill(c, p, verdictTone(p, r.Verdict), verdictLabelOf(r.Verdict))
				}
				ui.Text(c, T("Run")+" "+itoa64(r.ID)).FontSize(12).TextColor(p.muted).FontFeatures("tnum").SingleLine()
				ui.Text(c, r.StartedAt).FontSize(12).TextColor(p.muted).FontFeatures("tnum").SingleLine()
				ui.Text(c, T("Elapsed")+" "+r.Elapsed).FontSize(12).TextColor(p.muted).FontFeatures("tnum").SingleLine()
				ui.Text(c, T("Findings")+" "+itoa(len(a.findings))).FontSize(12).TextColor(p.muted).FontFeatures("tnum").SingleLine()
				if width, _ := c.Size(); width >= 1100 && r.DocsHit > 0 {
					ui.Text(c, T("Cited history docs")+" "+itoa(r.DocsHit)).FontSize(12).TextColor(p.muted).FontFeatures("tnum").SingleLine()
				}
				if a.pendingFindings() > 0 {
					statusText(c, p, p.tone("amber").bar, T("Pending")+" "+itoa(a.pendingFindings()))
				}
			})
		})
		iconButton(c, p, "copy", T("Copy report")).OnClick(func() {
			c.WriteClipboard(a.reportMarkdown())
			a.flash = T("Report copied")
		})
		iconButton(c, p, "x", T("Close")).OnClick(func() { a.closeReport() })
	})
}

// reportMenu is the report's left menu: one entry a view, with the count of
// what it holds, so a reader sees where the findings are without opening a
// tab to find out.
func (a *app) reportMenu(c *ui.Context, p palette) {
	count := func(typ string) int {
		n := 0
		for _, f := range a.findings {
			if f.Type == typ {
				n++
			}
		}
		return n
	}
	ui.Column(c).Width(196).Shrink(0).Padding(12, 10).Gap(2).
		BorderWidth(0, 1, 0, 0).BorderColor(p.border).Children(func() {
		caption(c, p, T("Review"))
		a.reportNavItem(c, p, "clipboard", T("Pre-review"), 0, reportMode == modeVerdict, func() {
			reportMode = modeVerdict
		})
		a.reportNavItem(c, p, "file-text", T("Document"), 0, reportMode == modeDocument, func() {
			reportMode = modeDocument
		})
		ui.Column(c).Gap(1).Margin(8, 0, 2, 0).Children(func() {
			caption(c, p, T("Findings"))
			a.reportNavItem(c, p, "alert", T("All findings"), len(a.findings),
				reportMode == modeFindings && reportTab == -1, func() {
					reportMode, reportTab = modeFindings, -1
				})
			for i, tab := range findingTabs {
				index := i
				a.reportNavItem(c, p, findingTypeIcon(tab.id), T(tab.label), count(tab.id),
					reportMode == modeFindings && reportTab == index, func() {
						reportMode, reportTab = modeFindings, index
					})
			}
		})
		ui.Column(c).Gap(1).Margin(8, 0, 2, 0).Children(func() {
			caption(c, p, T("Records"))
			a.reportNavItem(c, p, "history", T("Change ledger"), len(a.ledger),
				reportMode == modeLedger, func() { reportMode = modeLedger })
			a.reportNavItem(c, p, "layers", T("Module history"), 0,
				reportMode == modeModule, func() { reportMode = modeModule })
		})
	})
}

// reportNavItem is one line of the report's menu.
func (a *app) reportNavItem(c *ui.Context, p palette, iconName, label string, count int, active bool, onClick func()) {
	b := ui.ButtonBase(c.Key(label)).Height(30).Padding(0, 8).Gap(8).Radius(7).AlignItems(ui.Center).
		TextColor(p.text).Label(label)
	switch {
	case active:
		b.Background(p.active)
	case b.Hovered():
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, icon(iconName)).FontSize(14).TextColor(p.muted)
		ui.Text(c, label).FontSize(12.5).FontWeight(ternaryInt(active, 600, 400)).SingleLine().Grow(1)
		if count > 0 {
			ui.Textf(c, "%d", count).FontSize(11).TextColor(p.muted).FontFeatures("tnum")
		}
	})
	if b.Clicked() {
		onClick()
	}
}

func ternaryInt(cond bool, yes, no int) int {
	if cond {
		return yes
	}
	return no
}

// findingTypeIcon is the icon a finding type reads as in the menu.
func findingTypeIcon(typ string) string {
	switch typ {
	case "change_signal":
		return "refresh"
	case "logic_gap":
		return "git-branch"
	case "ambiguity":
		return "alert"
	case "one_liner":
		return "minus"
	case "pre_review":
		return "clipboard"
	}
	return "alert"
}

// reportMain is the chosen view, with the report's own toolbar above it.
func (a *app) reportMain(c *ui.Context, p palette, filtered []finding) {
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		a.reportToolbar(c, p, filtered)
		if a.reportErr != "" {
			ui.Column(c).Grow(1).Children(func() {
				emptyState(c, p, "alert", T("This report could not be read"), a.reportErr)
			})
			return
		}
		switch reportMode {
		case modeDocument:
			ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c).Key("report-doc-"+itoa64(a.reportRun)).Grow(1).MinWidth(0).Padding(16, 20, 20, 20).Children(func() {
					a.documentView(c, p, a.findings)
				})
				if f, ok := a.selectedFinding(); ok {
					detailWidth := float32(310)
					if width, _ := c.Size(); width < 1180 {
						detailWidth = 270
					}
					ui.Column(c).Width(detailWidth).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(p.border).
						Children(func() {
							ui.Scroll(c).Key("report-detail-"+f.ID).Grow(1).Padding(14, 16).Children(func() {
								a.reportDetail(c, p, f)
							})
						})
				}
			})
		case modeVerdict:
			ui.Scroll(c).Key("report-verdict-"+itoa64(a.reportRun)).Grow(1).Padding(16, 20, 20, 20).Children(func() {
				ui.Column(c).Grow(1).MaxWidth(900).Children(func() {
					if a.hasPreReview {
						a.verdictBanner(c, p)
					} else {
						emptyState(c, p, "clipboard", T("This run has no pre-review"),
							T("It was run before the pre-review existed, or the verdict could not be drawn."))
					}
				})
			})
		case modeLedger:
			ui.Scroll(c).Key("report-ledger-"+itoa64(a.reportRun)).Grow(1).Padding(8, 20, 20, 20).Children(func() { a.ledgerList(c, p) })
		case modeModule:
			ui.Scroll(c).Key("report-module-"+itoa64(a.reportRun)).Grow(1).Padding(16, 20, 20, 20).Children(func() {
				ui.Column(c).MaxWidth(980).Children(func() { a.reportModuleTimeline(c, p) })
			})
		default:
			ui.Scroll(c).Key("report-findings-"+itoa(reportTab)+reportFilter+reportSearch).Grow(1).Padding(12, 20, 20, 20).Children(func() {
				ui.Column(c).Gap(10).Children(func() {
					if len(filtered) == 0 {
						emptyState(c, p, "alert", T("No matching findings"), T("Try another filter or search word"))
					}
					for i, f := range filtered {
						a.findingCard(c, p, f, i == reportCurrent)
					}
				})
			})
		}
	})
}

// reportToolbar is the row above the chosen view: what the findings can be
// filtered and searched by, or what the document's marks mean.
func (a *app) reportToolbar(c *ui.Context, p palette, filtered []finding) {
	ui.Row(c).Height(48).Padding(8, 20).Gap(6).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
		switch reportMode {
		case modeFindings:
			ui.Textf(c, "%d/%d", len(filtered), len(a.findings)).FontSize(12).FontWeight(600).
				FontFeatures("tnum").Padding(2, 8).Radius(10).Border(1, p.border).SingleLine()
			all := ui.ButtonBase(c.Key("sev-all")).Padding(2, 8).Radius(6).AlignItems(ui.Center)
			if reportFilter == "all" {
				all.Background(p.active)
			}
			all.Children(func() { ui.Text(c, T("All")).FontSize(12).TextColor(p.muted).SingleLine() })
			if all.Clicked() {
				reportFilter = "all"
				reportCurrent = -1
			}
			for _, f := range severityFilters[1:] {
				filter := f
				active := reportFilter == filter.id
				b := ui.ButtonBase(c.Key("sev-"+filter.id)).Padding(2, 8).Radius(6).AlignItems(ui.Center).
					Tooltip(T(filter.label))
				if active {
					b.Background(p.active)
				}
				b.Children(func() {
					statusText(c, p, p.severity(filter.id), T(filter.id))
				})
				if b.Clicked() {
					if active {
						reportFilter = "all"
					} else {
						reportFilter = filter.id
					}
					reportCurrent = -1
				}
			}
			ui.Spacer(c)
			if reportSearching {
				field := ui.SearchField(c, &reportSearch).Label(T("Search findings")).Placeholder(T("Search")).Width(190)
				if !a.cmdOpen && !a.helpOpen {
					field.AutoFocus()
				}
			}
			a.searchToggle(c, p, &reportSearching, &reportSearch, T("Search findings"), !a.cmdOpen && !a.helpOpen)
			a.sortMenu(c, p)
		case modeDocument:
			for _, sev := range severities(a.findings) {
				if sev.count > 0 {
					statusText(c, p, p.severity(sev.sev), sev.sev+" "+itoa(sev.count))
				}
			}
			ui.Spacer(c)
			ui.Text(c, T("Choose a highlighted sentence to read the finding")).FontSize(12).TextColor(p.muted).SingleLine()
		case modeVerdict:
			caption(c, p, T("Pre-review"))
			if a.hasPreReview {
				statusText(c, p, verdictTone(p, a.preReview.Verdict).bar, verdictLabelOf(a.preReview.Verdict))
			}
			ui.Spacer(c)
		case modeLedger:
			caption(c, p, T("Change ledger"))
			ui.Text(c, itoa(len(a.ledger))).FontSize(12).TextColor(p.muted).FontFeatures("tnum").SingleLine()
			ui.Spacer(c)
		case modeModule:
			caption(c, p, T("Module history"))
			ui.Spacer(c)
		default:
			ui.Spacer(c)
		}
	})
}

// sortMenu is the findings' order, in the toolbar's right-hand corner.
func (a *app) sortMenu(c *ui.Context, p palette) {
	b := iconButton(c, p, "arrow-up-down", T("Sort"))
	b.Menu(func(m *ui.Menu) {
		for _, s := range reportSorts {
			sort := s
			if m.Item(T(sort.label)).Checked(reportSort == sort.id).Chosen() {
				reportSort = sort.id
			}
		}
		m.Separator()
		if m.Item(T("Descending")).Checked(reportSortDesc).Chosen() {
			reportSortDesc = !reportSortDesc
		}
	})
}

// moveSelection handles J and K over the report's findings, whichever view
// is showing. The selected finding stays where it is in the document, and
// the detail pane follows it.
func (a *app) moveSelection(c *ui.Context) {
	if reportMode != modeDocument && reportMode != modeFindings {
		return
	}
	if a.cmdOpen || a.helpOpen || a.deleteOpen {
		return
	}
	filtered := a.filterFindings(reportTab, reportFilter, reportSearch)
	if len(filtered) == 0 {
		return
	}
	current := -1
	for i, f := range filtered {
		if f.ID == a.reportSelected {
			current = i
			break
		}
	}
	step := 0
	if c.Shortcut(0, ui.KeyJ) || c.Shortcut(0, ui.KeyDown) {
		step = 1
	}
	if c.Shortcut(0, ui.KeyK) || c.Shortcut(0, ui.KeyUp) {
		step = -1
	}
	if step == 0 {
		return
	}
	next := min(max(current+step, 0), len(filtered)-1)
	a.reportSelected = filtered[next].ID
	reportCurrent = next
}

// selectedFinding is the finding the document view's detail pane shows.
func (a *app) selectedFinding() (finding, bool) {
	if a.reportSelected == "" {
		return finding{}, false
	}
	if f := a.findingByID(a.reportSelected); f != nil {
		return *f, true
	}
	return finding{}, false
}

// selectFinding chooses a finding without leaving the document: the reader
// keeps the sentence in view while its conclusion opens beside it.
func (a *app) selectFinding(f finding) {
	a.reportSelected = f.ID
	reportCurrent = a.findingIndex(f.ID)
}

// reportDetail is the selected finding beside the document, in one column:
// what it says, the two places it compares, and what to do with it.
func (a *app) reportDetail(c *ui.Context, p palette, f finding) {
	tn := p.severityTone(f.Severity)
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			statusPill(c, p, tn, f.Severity)
			ui.Text(c, findingTypeLabel(f)).FontSize(12).TextColor(p.muted).SingleLine().Grow(1)
			iconButton(c, p, "x", T("Close")).OnClick(func() { a.reportSelected = "" })
		})
		ui.Text(c, f.Title).FontSize(15).FontWeight(600).LineHeight(1.4).Selectable()
		ui.Text(c, f.Detail).FontSize(12.5).LineHeight(1.55).Selectable()
		if f.NewQuote != "" {
			a.detailQuote(c, p, T("New document"), f.NewQuote, f.NewPath, p.down)
		}
		if f.OldQuote != "" {
			label := T("Earlier document")
			if findingIsInternal(f) {
				label = T("Another place in this document")
			}
			a.detailQuote(c, p, label, f.OldQuote, f.OldPath, p.muted)
		}
		if f.Question != "" {
			ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, icon("alert")).FontSize(13).TextColor(p.muted).Margin(1, 0, 0, 0)
				ui.Text(c, f.Question).FontSize(12.5).LineHeight(1.45).Selectable()
			})
		}
		if f.Impact != "" {
			ui.Text(c, T("Impact")+"："+f.Impact).FontSize(12).TextColor(p.muted).LineHeight(1.45).Selectable()
		}
		if f.Fix != "" {
			ui.Column(c).Padding(8, 10).Radius(8).Background(p.active).Gap(2).Children(func() {
				caption(c, p, T("Suggested fix"))
				ui.Text(c, f.Fix).FontSize(12).LineHeight(1.45).Selectable()
			})
		}
		ui.Row(c).Gap(6).Wrap().Children(func() {
			for _, option := range findingStatusActions {
				action := option
				active := f.Status == action.status
				b := ui.ButtonBase(c.Key("status-"+action.status)).Padding(4, 8).Radius(6).
					Label(T(action.label)).Border(1, p.border)
				if active {
					b.Background(p.active).BorderColor(p.accent)
				}
				b.Children(func() { ui.Text(c, T(action.label)).FontSize(12).SingleLine() })
				if b.Clicked() {
					a.setFindingStatus(f.ID, action.status)
				}
			}
		})
		ui.Button(c, T("Show in the list")).OnClick(func() {
			reportMode = modeFindings
			reportTab = findingTabOf(f.Type)
			reportFilter, reportSearch = "all", ""
			reportCurrent = -1
			for i, candidate := range a.filterFindings(reportTab, reportFilter, reportSearch) {
				if candidate.ID == f.ID {
					reportCurrent = i
					break
				}
			}
		})
	})
}

// detailQuote is one side of a finding in the narrow detail pane: a label,
// the quote and where it sits.
func (a *app) detailQuote(c *ui.Context, p palette, label, quote, path string, bar ui.Color) {
	ui.Column(c).Gap(3).Padding(0, 0, 0, 8).BorderWidth(0, 0, 0, 2).BorderColor(bar).Children(func() {
		ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(p.muted)
		ui.Text(c, quote).FontSize(12).LineHeight(1.45).Selectable()
		if path != "" {
			ui.Text(c, path).FontSize(11).TextColor(p.muted).LineHeight(1.35)
		}
	})
}

// findingStatusActions are the reviewer's decisions, in the order they read.
var findingStatusActions = []struct{ status, label string }{
	{"confirmed", "Confirm"},
	{"dismissed", "False positive"},
	{"pending", "Needs more"},
	{"parked", "Parked"},
}

// reportMarkdown is the report as one text: what the copy button puts on the
// clipboard, ready to paste into a review note.
func (a *app) reportMarkdown() string {
	r, _ := a.openReportRow()
	var b strings.Builder
	b.WriteString("# " + r.Title + "\n\n")
	fmt.Fprintf(&b, "- 运行：%d\n- 状态：%s\n- 开始：%s\n- 耗时：%s\n\n", r.ID, runStatusLabel(r.Status), r.StartedAt, r.Elapsed)
	if a.hasPreReview {
		b.WriteString("## 预评审\n\n")
		b.WriteString(verdictLabelOf(a.preReview.Verdict) + "\n\n")
		b.WriteString(a.preReview.Summary + "\n\n")
		for _, risk := range a.preReview.Risks {
			fmt.Fprintf(&b, "- %s\n", risk)
		}
		if len(a.preReview.Risks) > 0 {
			b.WriteString("\n")
		}
		for i, q := range a.preReview.Asked {
			fmt.Fprintf(&b, "%d. %s\n", i+1, q)
		}
		b.WriteString("\n")
	}
	b.WriteString("## 发现\n\n")
	for _, f := range a.findings {
		fmt.Fprintf(&b, "### [%s] %s · %s\n\n%s\n\n", f.Severity, findingTypeLabel(f), f.Title, f.Detail)
		if f.NewQuote != "" {
			fmt.Fprintf(&b, "> 新文档：%s\n\n", f.NewQuote)
		}
		if f.OldQuote != "" {
			label := "对照"
			if findingIsInternal(f) {
				label = "同一文档的另一处"
			}
			fmt.Fprintf(&b, "> %s：%s\n\n", label, f.OldQuote)
		}
		if f.Question != "" {
			fmt.Fprintf(&b, "问题：%s\n\n", f.Question)
		}
	}
	if len(a.ledger) > 0 {
		b.WriteString("## 变更账本\n\n")
		for _, rec := range a.ledger {
			fmt.Fprintf(&b, "- %s · %s（%s）\n", rec.Label, ledgerKindLabel(rec.Kind), recordDateLabel(rec))
			if rec.Before != "" {
				fmt.Fprintf(&b, "  - 上一次：%s\n", rec.Before)
			}
			if rec.After != "" {
				fmt.Fprintf(&b, "  - 这一次：%s\n", rec.After)
			}
		}
	}
	return b.String()
}

// ledgerList draws the change ledger: one line a record, newest first,
// with what it changed and where it stands.
func (a *app) ledgerList(c *ui.Context, p palette) {
	if len(a.ledger) == 0 {
		emptyState(c, p, "history", T("No change records for this run"),
			T("They are written when a document is reviewed or a folder is ingested"))
		return
	}
	ui.Column(c).Children(func() {
		for _, rec := range a.ledger {
			row := ui.Row(c).Key(itoa64(rec.ID)).Padding(12, 0).Gap(14).AlignItems(ui.Start).
				BorderWidth(0, 0, 1, 0).BorderColor(p.border)
			if row.Hovered() {
				row.Background(p.hover)
			}
			row.ContextMenu(func(m *ui.Menu) {
				if m.Item(T("Confirm")).Chosen() {
					a.setChangeRecordStatus(rec, "confirmed")
				}
				if m.Item(T("Reject")).Chosen() {
					a.setChangeRecordStatus(rec, "rejected")
				}
			})
			row.Children(func() {
				ui.Column(c).Width(96).Shrink(0).Gap(2).Children(func() {
					ui.Text(c, ledgerKindLabel(rec.Kind)).FontSize(11.5).TextColor(ledgerKindColor(p, rec.Kind)).SingleLine()
					ui.Text(c, recordDateLabel(rec)).FontSize(11.5).TextColor(p.muted).FontFeatures("tnum").SingleLine()
				})
				ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, rec.Label).FontSize(14).FontWeight(500).SingleLine().Tooltip(rec.Label)
					if rec.Before != "" {
						ui.Text(c, T("before")+"："+rec.Before).FontSize(12.5).TextColor(p.muted).LineHeight(1.4).MaxLines(2)
					}
					if rec.After != "" {
						ui.Text(c, T("after")+"："+rec.After).FontSize(12.5).LineHeight(1.4).MaxLines(2)
					}
					if rec.Impact != "" {
						ui.Text(c, T("Impact")+"："+rec.Impact).FontSize(12.5).TextColor(p.muted).LineHeight(1.4).MaxLines(2)
					}
					// How this entry was attached to a function point is
					// part of reading it: a merged entry continues another
					// point's history, a candidate asks to be looked at.
					if rec.MatchLabel != "" && (rec.MatchKind == "candidate" || rec.MatchKind == "normalized") {
						label := T("Possibly the same as")
						color := p.s1
						if rec.MatchKind == "normalized" {
							label, color = T("Written onto"), p.muted
						}
						hint := label + "：" + rec.MatchLabel +
							"（" + strconv.FormatFloat(float64(rec.MatchScore), 'f', 2, 64) + "）"
						ui.Text(c, hint).FontSize(12).TextColor(color).LineHeight(1.4).MaxLines(2)
					}
				})
				ui.Column(c).Width(120).Shrink(0).Gap(2).AlignItems(ui.End).Children(func() {
					statusText(c, p, ledgerStatusColor(p, rec.Status), ledgerStatusLabel(rec.Status))
					if rec.DocTitle != "" {
						ui.Text(c, truncateText(rec.DocTitle, 18)).FontSize(11.5).TextColor(p.muted).SingleLine().Tooltip(rec.DocTitle)
					}
				})
			})
		}
	})
}

// setChangeRecordStatus records what a reviewer decided about a ledger entry.
func (a *app) setChangeRecordStatus(rec store.ChangeRecord, status string) {
	if a.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.store.SetChangeStatus(ctx, rec.ID, status); err != nil {
		a.flash = err.Error()
		return
	}
	a.flash = T("Ledger updated")
	a.load()
}

// recordDateLabel is the day a ledger entry belongs to: the day of the
// document it came from. A document that states no date of its own is not put
// on the timeline — the row says the date is unknown rather than showing the
// day it happened to be read, which would read as the newest rule.
func recordDateLabel(rec store.ChangeRecord) string {
	if rec.DocDate.IsZero() || rec.DocDateSource == "" || rec.DocDateSource == "missing" {
		return T("date unknown")
	}
	return rec.DocDate.Format("2006-01-02")
}

// ledgerKindLabel is what a record's kind reads as.
func ledgerKindLabel(kind string) string {
	switch kind {
	case "baseline":
		return T("baseline")
	case "added":
		return T("added")
	case "removed":
		return T("removed")
	case "clarified":
		return T("clarified")
	case "conflicted":
		return T("conflicted")
	}
	return T("modified")
}

func ledgerKindColor(p palette, kind string) ui.Color {
	switch kind {
	case "baseline":
		return p.muted
	case "added":
		return p.tone("green").bar
	case "removed":
		return p.tone("red").bar
	case "clarified":
		return p.tone("blue").bar
	case "conflicted":
		return p.tone("amber").bar
	}
	return p.text
}

func ledgerStatusLabel(status string) string {
	switch status {
	case "confirmed":
		return T("confirmed")
	case "rejected":
		return T("rejected")
	case "unquoted":
		// The rule was recorded without a quote from the document: the
		// reader has to check it rather than take the ledger's word.
		return T("no quote")
	case "unresolved":
		// The rule changed and the document does not say what happens to
		// what the old one governed: the question is a finding of its own.
		return T("unresolved")
	}
	return T("extracted")
}

func ledgerStatusColor(p palette, status string) ui.Color {
	switch status {
	case "confirmed":
		return p.success
	case "rejected":
		return p.tone("red").bar
	case "unquoted":
		return p.s1
	case "unresolved":
		return p.tone("red").bar
	}
	return p.muted
}

// findingMenu fills the menu of a finding, from its card's context menu.
func (a *app) findingMenu(c *ui.Context, m *ui.Menu, f finding) {
	if m.Item(T("Copy title")).Chosen() {
		c.WriteClipboard(f.Title)
		a.flash = T("Title copied")
	}
	if m.Item(T("Copy evidence")).Chosen() {
		label := T("Earlier document")
		if findingIsInternal(f) {
			label = T("Another place in this document")
		}
		var b strings.Builder
		b.WriteString(f.Title)
		if f.NewQuote != "" {
			b.WriteString("\n\n" + T("New document") + ": " + f.NewQuote + "\n" + f.NewPath)
		}
		if f.OldQuote != "" {
			b.WriteString("\n\n" + label + ": " + f.OldQuote + "\n" + f.OldDocTitle + " → " + f.OldPath)
		}
		c.WriteClipboard(b.String())
		a.flash = T("Evidence copied")
	}
	m.Separator()
	if m.Item(T("Confirm")).Chosen() {
		a.setFindingStatus(f.ID, "confirmed")
	}
	if m.Item(T("False positive")).Chosen() {
		a.setFindingStatus(f.ID, "dismissed")
	}
	if m.Item(T("Needs more")).Chosen() {
		a.setFindingStatus(f.ID, "pending")
	}
	if m.Item(T("Parked")).Chosen() {
		a.setFindingStatus(f.ID, "parked")
	}
}

// setFindingStatus writes a finding's status back to the run's findings and
// to the knowledge base. The row's own buttons hold a copy, so the status
// has to be written through both or it lasts only until the next load.
func (a *app) setFindingStatus(id, status string) {
	found := false
	note := ""
	for i := range a.findings {
		if a.findings[i].ID == id {
			a.findings[i].Status = status
			note = a.findings[i].Note
			found = true
		}
	}
	if !found {
		return
	}
	if a.store != nil {
		number, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			a.flash = T("This finding has no stored id")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.store.SetFindingReview(ctx, number, status, note); err != nil {
			a.flash = err.Error()
			return
		}
	}
	a.flash = T("Marked as") + findingStatusLabel(status)
	a.recountRun()
}

// recountRun keeps the list's figures true after a review: the open report's
// counts come from the findings in memory, and the sidebar's pending badge
// is the sum over every run.
func (a *app) recountRun() {
	pending := 0
	for i := range a.runs {
		if a.runs[i].ID == a.reportRun {
			severity, types := summarizeFindings(a.findings)
			a.runs[i].Findings = severity
			a.runs[i].Counts = types
			a.runs[i].Total = len(a.findings)
			a.runs[i].Pending = a.pendingFindings()
		}
		pending += a.runs[i].Pending
	}
	a.pendingAll = pending
}

// findingReviewColor is the tone a finding's stored review state reads in.
func findingReviewColor(p palette, status string) ui.Color {
	switch status {
	case "confirmed":
		return p.success
	case "dismissed":
		return p.tone("red").bar
	case "pending":
		return p.tone("amber").bar
	}
	return p.muted
}

// findingIsInternal says a finding compares two places in the same
// document, rather than the new document with history.
func findingIsInternal(f finding) bool {
	return f.Subtype == "internal_conflict"
}

// findingCard is one finding: its title and confidence, its severity and
// type, its detail, the two documents it compares and what to do with it.
func (a *app) findingCard(c *ui.Context, p palette, f finding, selected bool) {
	tn := p.severityTone(f.Severity)
	card := ui.Column(c).Padding(16).Radius(14).Background(p.tile).Gap(12).
		BorderWidth(0, 0, 0, 3).BorderColor(tn.bar)
	if selected {
		card.BorderColor(p.accent)
	}
	card.ContextMenu(func(m *ui.Menu) { a.findingMenu(c, m, f) })
	if card.Hovered() && !selected {
		card.Background(p.hover.Mix(p.tile, 0.5))
	}
	if card.Clicked() {
		a.selectFinding(f)
	}
	card.Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			statusPill(c, p, tn, f.Severity)
			ui.Text(c, findingTypeLabel(f)).FontSize(12.5).TextColor(p.muted).SingleLine()
			if label := findingReviewLabel(f.Status); label != "" {
				ui.Text(c, label).FontSize(11.5).TextColor(findingReviewColor(p, f.Status)).SingleLine()
			}
			ui.Spacer(c)
			ui.Text(c, fmt.Sprintf("%.0f%%", f.Confidence*100)).FontSize(12.5).TextColor(p.muted).FontFeatures("tnum")
		})

		ui.Text(c, f.Title).FontSize(15).FontWeight(600).LineHeight(1.35)

		ui.Text(c, f.Detail).FontSize(12.5).TextColor(p.muted).LineHeight(1.55)

		// The comparison is shown only when there is something to compare:
		// an ambiguity with no history used to draw an empty second column.
		if f.NewQuote != "" || f.OldQuote != "" {
			ui.Row(c).Gap(16).Children(func() {
				if f.NewQuote != "" {
					evidenceCol(c, p, T("New document"), f.NewQuote, f.NewPath, p.down)
				}
				if f.OldQuote != "" {
					label := T("Earlier document")
					if findingIsInternal(f) {
						label = T("Another place in this document")
					}
					path := f.OldPath
					if !findingIsInternal(f) && f.OldDocTitle != "" {
						path = f.OldDocTitle + " → " + path
					}
					evidenceCol(c, p, label, f.OldQuote, path, p.muted)
				}
			})
		}

		if f.Question != "" {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icon("alert")).FontSize(14).TextColor(p.muted)
				ui.Text(c, f.Question).FontSize(12.5)
			})
		}

		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Button(c, T("Confirm")).OnClick(func() { a.setFindingStatus(f.ID, "confirmed") })
			ui.Button(c, T("False positive")).OnClick(func() { a.setFindingStatus(f.ID, "dismissed") })
			ui.Button(c, T("Needs more")).OnClick(func() { a.setFindingStatus(f.ID, "pending") })
			ui.Button(c, T("Parked")).OnClick(func() { a.setFindingStatus(f.ID, "parked") })
			ui.Spacer(c)
			iconButton(c, p, "copy", T("Copy")).OnClick(func() {
				c.WriteClipboard(f.Title + "\n" + f.Detail)
				a.flash = T("Copied to the clipboard")
			})
		})
	})
}

// evidenceCol is one side of a finding's comparison: a labeled quote with
// a colored bar before it.
func evidenceCol(c *ui.Context, p palette, label, quote, path string, bar ui.Color) {
	ui.Column(c).Grow(1).Gap(6).Padding(0, 0, 0, 10).BorderWidth(0, 0, 0, 3).BorderColor(bar).Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(p.muted)
		if quote != "" {
			ui.Text(c, quote).FontSize(12.5).LineHeight(1.55).Selectable()
		}
		if path != "" {
			ui.Text(c, path).FontSize(11.5).TextColor(p.muted)
		}
	})
}

func (a *app) filterFindings(tab int, filter, query string) []finding {
	var out []finding
	query = strings.ToLower(strings.TrimSpace(query))
	for _, f := range a.findings {
		if tab >= 0 && tab < len(findingTabs) && f.Type != findingTabs[tab].id {
			continue
		}
		if filter != "all" && f.Severity != filter {
			continue
		}
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{
				f.Title, f.Detail, f.NewQuote, f.OldQuote, f.Question, f.NewPath, f.OldPath,
			}, "\n"))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, f)
	}
	slices.SortStableFunc(out, func(x, y finding) int {
		var n int
		switch reportSort {
		case "confidence":
			switch {
			case x.Confidence < y.Confidence:
				n = -1
			case x.Confidence > y.Confidence:
				n = 1
			}
		case "type":
			n = strings.Compare(x.Type, y.Type)
		default:
			n = strings.Compare(x.Severity, y.Severity)
		}
		if reportSortDesc {
			n = -n
		}
		return n
	})
	return out
}
