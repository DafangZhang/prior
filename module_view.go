package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"prior/store"
)

// The module page answers one question: what has this module's requirements
// been over time? It takes a module's name, reads every change record filed
// under it, and shows them oldest first — the order a reader follows a
// requirement from the version that stated it to the version that changed it.

var (
	// moduleQuery is the module the page is showing, and moduleInput what
	// the field holds: the page reads when the field is submitted.
	moduleQuery string
	moduleInput string
	// moduleRecords are the module's records as the page shows them,
	// moduleDocs the documents that stated them, and moduleNames what the
	// ledger holds for the field to offer.
	moduleRecords []store.ChangeRecord
	moduleDocs    []store.Doc
	moduleNames   []store.ModuleCount
	// moduleLoaded says the records are the query's; a query that failed
	// says so rather than showing the last module's history.
	moduleLoaded bool
	moduleErr    string
)

// moduleView is the page: a field for the module's name, the documents that
// stated it, and the rules in the order they were written.
func (a *app) moduleView(c *ui.Context, p palette) {
	a.pageHeader(c, p, T("Module history"), func() {
		ui.Text(c, T("Every requirement of one module, oldest first")).FontSize(12.5).TextColor(p.muted).SingleLine()
		ui.Spacer(c)
		a.moduleField(c, p)
	})

	ui.Scroll(c).Grow(1).Padding(16, 24, 24, 24).Children(func() {
		ui.Column(c).MaxWidth(980).Gap(16).Children(func() {
			switch {
			case moduleErr != "":
				emptyState(c, p, "alert", T("That module's history could not be read"), moduleErr)
			case moduleQuery == "":
				a.modulePicker(c, p)
			case moduleLoaded && len(moduleRecords) == 0:
				emptyState(c, p, "history", T("No requirements filed under")+" "+moduleQuery,
					T("Ingest the documents that state them, or review one against the module."))
			case moduleLoaded:
				a.moduleSummary(c, p)
				a.moduleTimeline(c, p)
			}
		})
	})
}

// moduleField is the module's name as the page takes it, offering the
// modules the ledger holds.
func (a *app) moduleField(c *ui.Context, p palette) {
	field := ui.TextInput(c, &moduleInput).Label(T("Module")).Placeholder(T("Module name")).Width(220)
	if field.Submitted() {
		a.showModule(strings.TrimSpace(moduleInput))
	}
	if len(moduleNames) > 0 {
		field.Tooltip(strings.Join(moduleNameList(moduleNames), "\n"))
	}
	ui.Button(c, T("Show")).OnClick(func() { a.showModule(strings.TrimSpace(moduleInput)) })
}

// modulePicker is the page before a module is named: what the ledger holds,
// with each one a click away.
func (a *app) modulePicker(c *ui.Context, p palette) {
	if len(moduleNames) == 0 {
		emptyState(c, p, "history", T("No module has requirements yet"),
			T("The ledger fills as documents are ingested or reviewed."))
		return
	}
	ui.Column(c).Gap(10).Children(func() {
		caption(c, p, T("Modules the ledger holds"))
		ui.Row(c).Gap(8).Wrap().Children(func() {
			for _, m := range moduleNames {
				module := m
				b := ui.Button(c, m.Name+" · "+itoa(m.Count)).OnClick(func() {
					moduleInput = module.Name
					a.showModule(module.Name)
				})
				_ = b
			}
		})
	})
}

// showModule reads a module's history: its documents and every record filed
// under it, oldest first.
func (a *app) showModule(module string) {
	if module == "" {
		moduleQuery, moduleLoaded, moduleErr = "", false, ""
		moduleRecords, moduleDocs = nil, nil
		return
	}
	moduleQuery, moduleInput = module, module
	if a.store == nil {
		moduleErr = T("Knowledge base not connected")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	records, err := a.store.ModuleTimeline(ctx, module, 400)
	if err != nil {
		moduleErr, moduleLoaded = err.Error(), false
		return
	}
	docs, err := a.store.ModuleDocuments(ctx, module, 200)
	if err != nil {
		moduleErr, moduleLoaded = err.Error(), false
		return
	}
	moduleRecords, moduleDocs, moduleErr, moduleLoaded = records, docs, "", true
}

// loadModuleNames reads the modules the ledger holds, for the field to offer
// and the page to list before one is named.
func (a *app) loadModuleNames() {
	if a.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if names, err := a.store.ModuleNames(ctx); err == nil {
		moduleNames = names
	}
}

// moduleSummary is what the module holds: how many documents, how many
// function points, and the span of dates its history covers.
func (a *app) moduleSummary(c *ui.Context, p palette) {
	points := map[int64]bool{}
	for _, rec := range moduleRecords {
		points[rec.PointID] = true
	}
	first, last := "", ""
	for _, rec := range moduleRecords {
		if rec.DocDate.IsZero() {
			continue
		}
		day := rec.DocDate.Format("2006-01-02")
		if first == "" {
			first = day
		}
		last = day
	}
	ui.Column(c).Padding(16).Radius(14).Background(p.tile).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, moduleQuery).FontSize(18).FontWeight(600)
			ui.Text(c, T("requirements in time order")).FontSize(12.5).TextColor(p.muted)
		})
		ui.Row(c).Gap(20).Children(func() {
			for _, item := range []struct{ label, value string }{
				{T("Documents"), itoa(len(moduleDocs))},
				{T("Function points"), itoa(len(points))},
				{T("Change records"), itoa(len(moduleRecords))},
			} {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, item.value).FontSize(15).FontWeight(600).FontFeatures("tnum")
					ui.Text(c, item.label).FontSize(12.5).TextColor(p.muted)
				})
			}
			if first != "" {
				ui.Text(c, first+" → "+last).FontSize(12.5).TextColor(p.muted).FontFeatures("tnum")
			}
		})
	})
}

// moduleTimeline draws the module's rules in the order they were written, a
// document at a time: each document's heading says when it stated them, and
// what changed is said against what it changed from.
func (a *app) moduleTimeline(c *ui.Context, p palette) {
	byDoc := map[int64][]store.ChangeRecord{}
	var order []int64
	for _, rec := range moduleRecords {
		if _, seen := byDoc[rec.DocID]; !seen {
			order = append(order, rec.DocID)
		}
		byDoc[rec.DocID] = append(byDoc[rec.DocID], rec)
	}
	for _, docID := range order {
		records := byDoc[docID]
		ui.Column(c).Gap(8).Children(func() {
			a.timelineHeading(c, p, records[0])
			for _, rec := range records {
				a.timelineRow(c, p, rec)
			}
		})
	}
}

// timelineHeading heads a document's stretch of the timeline: the day it was
// written, its title, and how many rules it stated.
func (a *app) timelineHeading(c *ui.Context, p palette, rec store.ChangeRecord) {
	day := recordDateLabel(rec)
	if rec.DocDateSource == "missing" {
		day = T("date unknown")
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(6, 0).Children(func() {
		dot(c, p.accent)
		ui.Text(c, day).FontSize(12.5).FontWeight(600).FontFeatures("tnum").TextColor(p.muted)
		ui.Text(c, rec.DocTitle).FontSize(14).FontWeight(600).SingleLine().Grow(1).MinWidth(0).Tooltip(rec.DocTitle)
	})
}

// timelineRow is one rule as the module held it: what the point is, the rule
// the document stated, and — for a change — the rule it replaced.
func (a *app) timelineRow(c *ui.Context, p palette, rec store.ChangeRecord) {
	row := ui.Column(c).Key(itoa64(rec.ID)).Padding(10, 0, 10, 26).Gap(4).
		BorderWidth(0, 0, 1, 0).BorderColor(p.border)
	if row.Hovered() {
		row.Background(p.hover)
	}
	row.Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			statusText(c, p, ledgerKindColor(p, rec.Kind), ledgerKindLabel(rec.Kind))
			ui.Text(c, rec.Label).FontSize(13.5).FontWeight(500).SingleLine().Grow(1).MinWidth(0).Tooltip(rec.Label)
			if rec.DocID != 0 {
				if ui.ButtonBase(c.Key("doc")).Padding(2, 6).Radius(6).Label(T("Open the document")).Tooltip(rec.DocTitle).Children(func() {
					ui.Text(c, "→").FontSize(12.5).TextColor(p.muted)
				}).Clicked() {
					a.currentDoc = rec.DocID
					a.router.Push("/library")
				}
			}
		})
		if rec.Before != "" && rec.Before != rec.After {
			ui.Text(c, T("was")+"："+rec.Before).FontSize(12.5).TextColor(p.muted).LineHeight(1.45).Selectable()
		}
		if rec.After != "" {
			ui.Text(c, T("now")+"："+rec.After).FontSize(12.5).LineHeight(1.45).Selectable()
		}
		if rec.Impact != "" {
			ui.Text(c, T("Impact")+"："+rec.Impact).FontSize(12).TextColor(p.muted).LineHeight(1.4)
		}
	})
}

// moduleNameList is the modules as one line each, for a tooltip.
func moduleNameList(names []store.ModuleCount) []string {
	out := make([]string, 0, len(names))
	for _, m := range names {
		out = append(out, m.Name+" · "+itoa(m.Count))
	}
	return out
}

// runModule is the module the report's run reviewed: the document's own
// module, which is what its history is read under.
func (a *app) runModule() string {
	r, ok := a.openReportRow()
	if !ok {
		return ""
	}
	if d := a.findDoc(r.DocID); d != nil {
		return d.Module
	}
	return ""
}

// reportModuleTimeline draws the module's history inside the report, which
// is where a reader compares what the new document says with what the module
// already held.
func (a *app) reportModuleTimeline(c *ui.Context, p palette) {
	module := a.runModule()
	if module == "" {
		emptyState(c, p, "history", T("This run's document names no module"),
			T("Its module is read from the document, and a document without one has no history to follow."))
		return
	}
	// The tab is where a reader lands, not only what a click opens: the
	// module's history is read when the page shows it, not only when the tab
	// is chosen, so a report opened on this view is not empty.
	if moduleQuery != module || !moduleLoaded {
		a.showModule(module)
	}
	if moduleErr != "" {
		emptyState(c, p, "alert", T("That module's history could not be read"), moduleErr)
		return
	}
	if !moduleLoaded {
		emptyState(c, p, "history", T("No requirements filed under")+" "+module,
			T("Ingest the documents that state them, or review one against the module."))
		return
	}
	ui.Column(c).MaxWidth(980).Gap(14).Children(func() {
		a.moduleSummary(c, p)
		a.moduleTimeline(c, p)
	})
}
