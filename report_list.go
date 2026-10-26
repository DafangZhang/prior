package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"prior/store"
)

var (
	// reportListQuery is the report list's search, reportListFilter its
	// status filter, and reportListSearching whether the field shows.
	reportListQuery     string
	reportListFilter    = "all"
	reportListSearching bool
)

// reportListFilters are the run statuses the report list filters by.
var reportListFilters = []struct{ id, label string }{
	{"all", "All"},
	{"succeeded", "Completed"},
	{"degraded", "Degraded"},
	{"failed", "Failed"},
	{"running", "Running"},
}

// reportListView is the report page: the reviews that have been run, newest
// first. A review is read in its own dialog rather than in the page, so the
// page stays a place to find one.
func (a *app) reportListView(c *ui.Context, p palette) {
	rows := a.filteredRuns()
	a.pageHeader(c, p, T("Report"), func() {
		ui.Textf(c, "%d/%d", len(rows), len(a.runs)).FontSize(12.5).FontWeight(600).FontFeatures("tnum").
			Padding(2, 9).Radius(11).Border(1, p.border).SingleLine()
		ui.Spacer(c)
		if reportListSearching {
			field := ui.SearchField(c, &reportListQuery).Label(T("Search reports")).Placeholder(T("Search")).Width(210)
			if !a.overlayOpen() {
				field.AutoFocus()
			}
		}
		a.toolbar(c, p, []pageAction{
			{"filter", T("Filter reports"), false, func(_ *ui.Context, m *ui.Menu) {
				for _, f := range reportListFilters {
					filter := f
					if m.Item(T(filter.label)).Checked(reportListFilter == filter.id).Chosen() {
						reportListFilter = filter.id
					}
				}
			}, nil},
			{"refresh", T("Reload reports"), false, nil, func() {
				if a.store == nil {
					a.flash = T("Knowledge base not connected")
					return
				}
				a.load()
				a.flash = T("Reports reloaded")
			}},
		})
		a.searchToggle(c, p, &reportListSearching, &reportListQuery, T("Search reports"), !a.overlayOpen())
	})
	ui.Divider(c).Background(p.border)

	switch {
	case len(a.runs) == 0:
		emptyState(c, p, "alert", T("No reports yet"),
			T("Review a document from the analysis page to start the first one."))
	case len(rows) == 0:
		emptyState(c, p, "alert", T("No matching reports"), T("Try another filter or search word"))
	default:
		a.reportRunList.Key = func(i int) any { return rows[i].ID }
		ui.List(c, &a.reportRunList, len(rows), func(i int) {
			a.reportRunRow(c, p, rows[i])
		}).Grow(1)
	}

	a.statusBar(c, p, []statusItem{
		{T("Reports") + " " + itoa(len(a.runs)), ui.Color{}},
		{T("Pending") + " " + itoa(a.pendingAll), p.tone("amber").bar},
		{T("Failed") + " " + itoa(a.countRuns("failed")), p.tone("red").bar},
	}, nil)
}

// filteredRuns is the list the page shows: the status filter and the search
// word, on the run titles.
func (a *app) filteredRuns() []run {
	var out []run
	query := strings.ToLower(strings.TrimSpace(reportListQuery))
	for _, r := range a.runs {
		if reportListFilter != "all" {
			if reportListFilter == "succeeded" {
				if r.Status != "succeeded" && r.Status != "completed" {
					continue
				}
			} else if r.Status != reportListFilter {
				continue
			}
		}
		if query != "" && !strings.Contains(strings.ToLower(r.Title), query) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (a *app) countRuns(status string) int {
	n := 0
	for _, r := range a.runs {
		if r.Status == status {
			n++
		}
	}
	return n
}

// reportRunRow is one review on the list: its status, what it found, and the
// verdict a meeting would have taken from it.
func (a *app) reportRunRow(c *ui.Context, p palette, r run) {
	tn := runTone(p, r.Status)
	row := ui.Row(c.Key(r.ID)).Padding(14, 24).Gap(14).AlignItems(ui.Start).
		BorderWidth(0, 0, 1, 0).BorderColor(p.border)
	row.Tooltip(typeSummary(r))
	if row.Hovered() {
		row.Background(p.hover)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item(T("Open report")).Chosen() {
			a.openReport(r.ID)
		}
		if m.Item(T("Analyze again")).Chosen() {
			a.router.Push("/analyze")
		}
	})
	row.Children(func() {
		statusPill(c, p, tn, runStatusLabel(r.Status))
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, r.Title).FontSize(14.5).FontWeight(600).SingleLine().Tooltip(r.Title)
			ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
				ui.Text(c, r.StartedAt).FontSize(12.5).TextColor(p.muted).FontFeatures("tnum").SingleLine().Shrink(0)
				ui.Text(c, r.Elapsed).FontSize(12.5).TextColor(p.muted).FontFeatures("tnum").SingleLine().Shrink(0)
				ui.Text(c, T("Findings")+" "+itoa(r.Total)).FontSize(12.5).TextColor(p.muted).FontFeatures("tnum").SingleLine().Shrink(0)
				if r.Pending > 0 {
					statusText(c, p, p.tone("amber").bar, T("Pending")+" "+itoa(r.Pending))
				}
			})
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if r.Verdict != "" {
				statusPill(c, p, verdictTone(p, r.Verdict), verdictLabelOf(r.Verdict))
			}
			ui.Icon(c, icon("chevron-right")).FontSize(16).TextColor(p.muted)
		})
	})
	if row.Clicked() {
		a.openReport(r.ID)
	}
}

// typeSummary is a run's findings by type, for the row's tooltip: the list
// itself stays two lines at the window's least width, and the distribution is
// one hover away.
func typeSummary(r run) string {
	parts := make([]string, 0, len(findingTabs))
	for _, tab := range findingTabs {
		if n := r.Counts[tab.id]; n > 0 {
			parts = append(parts, T(tab.label)+" "+itoa(n))
		}
	}
	if len(parts) == 0 {
		return r.Title
	}
	return r.Title + "\n" + strings.Join(parts, " · ")
}

// runByID is the run a report names, and whether the list holds it. An
// unknown id is said rather than falling back to the newest run, which would
// show another document's review under the one asked for.
func (a *app) runByID(id int64) (run, bool) {
	for _, r := range a.runs {
		if r.ID == id {
			return r, true
		}
	}
	return run{}, false
}

// openReportRow is the run the open report shows.
func (a *app) openReportRow() (run, bool) {
	return a.runByID(a.reportRun)
}

// openReport shows a run's report in its dialog. Opening the report that is
// already open keeps the reader's place; opening another starts at the
// document and clears the filters of the one before.
func (a *app) openReport(id int64) {
	if _, ok := a.runByID(id); !ok {
		a.closeReport()
		a.flash = T("That report is not in the list")
		return
	}
	if a.reportOpen && a.reportRun == id && a.reportLoaded {
		return
	}
	a.reportOpen = true
	a.reportRun = id
	reportMode = modeDocument
	reportTab = -1
	reportFilter = "all"
	reportSort, reportSortDesc = "severity", false
	reportSearch, reportSearching = "", false
	reportCurrent = -1
	a.reportSelected = ""
	a.reviewDocID, a.reviewChunks, a.reviewMarks, a.reviewMarkKey = 0, nil, nil, ""
	a.reviewText, a.reviewSpans = "", nil
	a.reportLoaded, a.reportErr = false, ""
	a.loadReport(id)
}

// closeReport puts the report away. The list behind it stays where it was; a
// route that named the report is replaced by the list, or the next frame would
// open the same report again.
func (a *app) closeReport() {
	a.reportOpen = false
	a.reportSelected = ""
	reportCurrent = -1
	if a.router != nil && strings.HasPrefix(a.router.Path(), "/report/") {
		a.router.Replace("/reports")
	}
}

// loadReport reads the open run's findings, its ledger and its verdict. The
// list's counts are read with the runs; the detail is read when a report is
// opened, so the list does not load every finding to show a total.
func (a *app) loadReport(id int64) {
	a.reportLoaded, a.reportErr = true, ""
	a.reviewDocID, a.reviewChunks, a.reviewMarks, a.reviewMarkKey = 0, nil, nil, ""
	a.reviewText, a.reviewSpans = "", nil
	if a.store == nil {
		a.ledger, a.hasPreReview = nil, false
		if id == 1 {
			// The sample the interface starts on: its findings are the
			// fixture's, and only the first run has them.
			a.findings = fixtureFindings()
			a.preReview = store.PreReview{
				Verdict: "clarify",
				Summary: "文档的版本信息与积分规则可以读出，但缺少验收标准与异常分支：补齐后可进入排期。",
				Risks:   []string{"S1 · 积分过期规则被删除，历史文档仍引用该条款", "S2 · 缺少退款订单的叠加规则"},
				Asked:   []string{"积分过期规则删除后，存量积分如何处理？", "已退款订单是否允许叠加优惠券？"},
			}
			a.hasPreReview = true
		} else {
			a.findings = nil
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	findings, err := a.store.Findings(ctx, id)
	if err != nil {
		a.reportErr = err.Error()
		a.findings, a.ledger = nil, nil
		return
	}
	a.findings = make([]finding, 0, len(findings))
	for _, f := range findings {
		a.findings = append(a.findings, findingOf(f))
	}
	if records, err := a.store.RunLedger(ctx, id, 500); err == nil {
		a.ledger = records
	}
	if pre, ok, err := a.store.PreReview(ctx, id); err == nil && ok {
		a.preReview, a.hasPreReview = pre, true
	} else {
		a.preReview, a.hasPreReview = store.PreReview{}, false
	}
	// A finding the reader had chosen may not be in this run's list.
	if a.reportSelected != "" && a.findingByID(a.reportSelected) == nil {
		a.reportSelected = ""
	}
}

// findingByID is a finding of the open report.
func (a *app) findingByID(id string) *finding {
	for i := range a.findings {
		if a.findings[i].ID == id {
			return &a.findings[i]
		}
	}
	return nil
}
