package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// analyzeStages are the review's stages, in order, as the page's stepper
// shows them.
var analyzeStages = []struct{ id, label string }{
	{"S0", "Profile the document"},
	{"R", "Search the history"},
	{"S1", "One-line requirements"},
	{"S2", "Clarity checks"},
	{"P", "Pre-review: the verdict"},
	{"S3", "Logic gaps"},
	{"S4", "Change signals and the ledger"},
	{"S5", "Assemble the report"},
}

// ingestStages are the ingest's stages.
var ingestStages = []struct{ id, label string }{
	{"scan", "Scan the folder"},
	{"parse", "Parse each document"},
	{"embed", "Embed the chunks"},
	{"store", "Store the documents"},
}

func (a *app) analyzeView(c *ui.Context, p palette) {
	a.pageHeader(c, p, T("Analyze"), func() {
		ui.Text(c, T("Ingest a folder, or review one document against the knowledge base")).
			FontSize(12.5).TextColor(p.muted).SingleLine()
		if a.ingest.running || a.analyzing {
			ui.Spinner(c)
		}
		ui.Spacer(c)
		a.toolbar(c, p, a.analyzeActions())
	})
	ui.Divider(c).Background(p.border)
	a.jobStrip(c, p)

	ui.Row(c).Grow(1).MinHeight(0).Padding(16, 24, 24, 24).Gap(20).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Grow(1).MinWidth(0).Children(func() {
			ui.Column(c).Gap(16).Children(func() {
				a.flowTiles(c, p)
				if a.analyzing {
					a.stageTile(c, p, T("Review stages"), analyzeStages, a.analyzeStage)
				}
				if a.ingest.running {
					a.stageTile(c, p, T("Ingest stages"), ingestStages, a.ingest.message)
				}
				a.analyzeSettingsTile(c, p)
			})
		})
		ui.Column(c).Width(300).Shrink(0).Children(func() {
			tile(c, p).Grow(1).Children(func() {
				caption(c, p, T("Live log"))
				ui.Spacer(c).MinHeight(6)
				lines := a.analyzeLines
				if a.ingest.running {
					lines = a.ingest.lines
				}
				if len(lines) == 0 {
					emptyState(c, p, "scan", T("No log yet"), T("The live log shows here once a run starts"))
					return
				}
				ui.Scroll(c).Grow(1).Children(func() {
					ui.Column(c).Gap(2).Children(func() {
						for _, line := range lines {
							ui.Text(c, line).FontSize(12.5).FontFeatures("tnum").Padding(3, 0).Selectable()
						}
					})
				})
			})
		})
	})
	a.statusBar(c, p, a.analyzeStatus(), nil)
}

// analyzeActions are the page's two entries: they are the two flows of the
// business process, and nothing else.
func (a *app) analyzeActions() []pageAction {
	actions := []pageAction{
		{"plus", T("Import a folder"), false, nil, func() {
			if a.store == nil {
				a.flash = T("Knowledge base not connected")
				return
			}
			a.pickFolder(a.startIngest)
		}},
		{"scan", T("Review a new document"), false, nil, func() {
			if a.store == nil {
				a.flash = T("Knowledge base not connected")
				return
			}
			a.pickFile(a.startAnalyze)
		}},
	}
	if a.ingest.running && a.ingest.cancel != nil {
		actions = append(actions, pageAction{"x", T("Cancel"), false, nil, func() { a.ingest.cancel() }})
	}
	if a.analyzing && a.analyzeCancel != nil {
		actions = append(actions, pageAction{"x", T("Cancel"), false, nil, func() { a.analyzeCancel() }})
	}
	return actions
}

// flowTiles are the page's two entries as tiles, with what each does.
func (a *app) flowTiles(c *ui.Context, p palette) {
	ui.Row(c).Gap(16).AlignItems(ui.Stretch).Children(func() {
		a.flowTile(c, p, T("Import a folder"), T("Build the knowledge base from the documents you already have"),
			T("Every document is parsed, dated, chunked and embedded. The folder's manifest fills in the module names."),
			T("Choose a folder"), func() {
				if a.store == nil {
					a.flash = T("Knowledge base not connected")
					return
				}
				a.pickFolder(a.startIngest)
			})
		a.flowTile(c, p, T("Review a new document"), T("Judge a new requirement against the history"),
			T("The document is ingested, then compared with the history: change signals with their impact, conflicts, unclear requirements."),
			T("Choose a document"), func() {
				if a.store == nil {
					a.flash = T("Knowledge base not connected")
					return
				}
				a.pickFile(a.startAnalyze)
			})
	})
}

func (a *app) flowTile(c *ui.Context, p palette, title, subtitle, detail, button string, run func()) {
	tile(c, p).Grow(1).Basis(0).Children(func() {
		caption(c, p, strings.ToUpper(title))
		ui.Text(c, subtitle).FontSize(15).FontWeight(600).LineHeight(1.35).Margin(6, 0, 0, 0)
		ui.Text(c, detail).FontSize(12.5).TextColor(p.muted).LineHeight(1.5)
		ui.Spacer(c).MinHeight(10)
		ui.Row(c).Gap(8).Children(func() {
			ui.PrimaryButton(c, button).OnClick(run)
		})
	})
}

// stageTile is the stepper: what the run has done, what it is doing, and
// what is left.
func (a *app) stageTile(c *ui.Context, p palette, title string, stages []struct{ id, label string }, current string) {
	tile(c, p).Gap(0).Children(func() {
		caption(c, p, title)
		ui.Spacer(c).MinHeight(6)
		ui.Column(c).Children(func() {
			reached := false
			for i, stage := range stages {
				active := !reached && current == stage.id
				if active {
					reached = true
				}
				done := current != "" && !active && !reached
				if current != "" && indexOf(stages, current) > i {
					done, active = true, false
				}
				ui.Row(c).Padding(7, 0).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, stage.id).FontSize(11.5).TextColor(p.muted).Width(28).FontFeatures("tnum")
					ui.Text(c, T(stage.label)).FontSize(12.5).Grow(1).Basis(0).MinWidth(0)
					if done {
						ui.Icon(c, icon("check")).FontSize(14).TextColor(p.success)
					}
					if active {
						ui.Text(c, T("Running...")).FontSize(12.5).TextColor(p.accent)
					}
				})
				if i < len(stages)-1 {
					sepLine(c, p)
				}
			}
		})
	})
}

// indexOf is where a stage stands in the list, or -1.
func indexOf(stages []struct{ id, label string }, id string) int {
	for i, s := range stages {
		if s.id == id {
			return i
		}
	}
	return -1
}

// analyzeSettingsTile reads the settings the analysis runs with, which the
// settings page owns.
func (a *app) analyzeSettingsTile(c *ui.Context, p palette) {
	tile(c, p).Gap(12).Children(func() {
		caption(c, p, T("Run settings"))
		ui.Row(c).Gap(24).Children(func() {
			readOnly(c, p, T("Embedding model"), a.set.Models.Embed)
			readOnly(c, p, T("Protocol"), T(protocolLabel(a.set.Models.Protocol)))
			readOnly(c, p, T("LLM model"), a.set.Models.LLM)
			readOnly(c, p, T("Temperature"), fmtFloat(a.set.Models.Temperature))
			readOnly(c, p, T("Knowledge base"), knowledgeState(a))
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, T("Settings")).OnClick(func() { a.router.Push("/settings/analyze") })
			ui.Button(c, T("Check the environment")).OnClick(func() { a.router.Push("/settings/env") })
		})
	})
}

func readOnly(c *ui.Context, p palette, label, value string) {
	ui.Column(c).Gap(3).Grow(1).Basis(0).MinWidth(0).Children(func() {
		ui.Text(c, label).FontSize(11.5).TextColor(p.muted)
		ui.Text(c, value).FontSize(12.5).SingleLine().Tooltip(value)
	})
}

// knowledgeState says what the knowledge base holds, in one line.
func knowledgeState(a *app) string {
	if a.store == nil {
		return T("not connected")
	}
	return itoa(len(a.docs)) + " " + T("docs")
}

func fmtFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	return s
}

// p_success and p_accent are the palette's colours for the status figures.
func p_success(a *app) ui.Color {
	return paletteFor(nil, findSidebarColor(a.set.Appearance.SidebarColor)).success
}

func p_accent(a *app) ui.Color {
	return paletteFor(nil, findSidebarColor(a.set.Appearance.SidebarColor)).accent
}

// analyzeStatus is the page's status bar: the connection, the models and
// what the knowledge base holds.
func (a *app) analyzeStatus() []statusItem {
	items := []statusItem{{T("Knowledge base") + " " + knowledgeState(a), ui.Color{}}}
	if a.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, _, findings, records, err := a.store.Stats(ctx); err == nil {
			items = append(items,
				statusItem{T("Findings") + " " + itoa(findings), p_accent(a)},
				statusItem{T("Change records") + " " + itoa(records), p_success(a)})
		}
	}
	if a.env.pgErr != "" {
		items = append(items, statusItem{T("PostgreSQL") + " " + T("not connected"), ui.Color{}})
	}
	return items
}
