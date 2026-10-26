package main

import (
	"encoding/json"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"prior/llm"
)

// settingsSectionInfo is one section of the settings: what it holds, and
// whether its page edits a draft that Save applies.
type settingsSectionInfo struct {
	id, title, detail string
	draft             bool
}

var settingsSections = []settingsSectionInfo{
	{"env", "Environment", "Database, Ollama and the language model", true},
	{"analyze", "Analysis", "Protocol, model, temperature and context", true},
	{"appearance", "Appearance", "Theme, language and sidebar color", false},
	{"data", "Data", "Which library columns show", false},
	{"about", "About", "Version and license", false},
}

func findSettingsSection(id string) *settingsSectionInfo {
	for i := range settingsSections {
		if settingsSections[i].id == id {
			return &settingsSections[i]
		}
	}
	return nil
}

// cloneSettings copies settings with their slices, which a plain assignment
// would share: a draft would then edit what is saved.
func cloneSettings(s settings) settings {
	var out settings
	data, _ := json.Marshal(s)
	json.Unmarshal(data, &out)
	return out
}

// settingsView shows a settings section, "" for the first one.
func (a *app) settingsView(c *ui.Context, p palette, section string) {
	if section == "" {
		section = settingsSections[0].id
	}
	info := findSettingsSection(section)
	if info == nil {
		a.router.Replace("/settings")
		return
	}
	// A section's page edits its own draft, filled from the saved settings
	// when the page opens: what the form holds is applied by Save.
	if info.draft && a.draftFor != info.id {
		a.draftFor = info.id
		a.draft = cloneSettings(a.set)
		if info.id == "env" {
			a.syncPGForm(a.draft.Connection.PGDSN)
		}
	}

	a.pageHeader(c, p, T("Settings"), func() {
		ui.Text(c, T(info.detail)).FontSize(12.5).TextColor(p.muted).SingleLine()
		ui.Spacer(c)
		a.toolbar(c, p, []pageAction{
			{"ellipsis", T("More actions"), false, func(_ *ui.Context, m *ui.Menu) {
				dirty := info.draft && a.draftFor == info.id && !sameSettings(a.draft, a.set)
				if m.Item(T("Save")).Disabled(!dirty).Chosen() {
					a.saveDraftSettings()
				}
				if m.Item(T("Discard changes")).Disabled(!dirty).Chosen() {
					a.discardDraftSettings()
				}
				m.Separator()
				if m.Item(T("Restore defaults")).Chosen() {
					a.resetDraftSettings()
				}
			}, nil},
		})
	})
	ui.Divider(c).Background(p.border)

	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		a.settingsSectionList(c, p, info)
		ui.Scroll(c).Grow(1).MinWidth(0).Padding(4, 24, 24, 24).Children(func() {
			ui.Column(c).MaxWidth(760).Children(func() {
				switch info.id {
				case "env":
					a.settingsEnv(c, p, &a.draft)
				case "analyze":
					a.settingsAnalyze(c, p, &a.draft)
				case "appearance":
					a.settingsAppearance(c, p, &a.set)
				case "data":
					a.settingsData(c, p)
				default:
					a.settingsAbout(c, p)
				}
			})
		})
	})

	if !info.draft || a.draftFor != info.id || sameSettings(a.draft, a.set) {
		a.settingsStatusBar(c, p, info)
		return
	}
	// Cmd+S keeps the draft: asked for here, ahead of the form's own
	// controls, so that a focused field cannot take the key first.
	if !a.overlayOpen() && c.Shortcut(ui.Cmd, ui.KeyS) {
		a.saveDraftSettings()
		a.settingsStatusBar(c, p, info)
		return
	}
	ui.Row(c).Padding(12, 24).Gap(8).Justify(ui.End).AlignItems(ui.Center).
		BorderWidth(1, 0, 0, 0).BorderColor(p.border).Background(p.tile).Children(func() {
		ui.Text(c, T("You have unsaved changes")).FontSize(13).TextColor(p.muted).Grow(1)
		if ui.Button(c, T("Discard")).Clicked() {
			a.discardDraftSettings()
		}
		if ui.PrimaryButton(c, T("Save")).Clicked() {
			a.saveDraftSettings()
		}
	})
	a.settingsStatusBar(c, p, info)
}

// settingsSectionList is the list of sections beside the page.
func (a *app) settingsSectionList(c *ui.Context, p palette, current *settingsSectionInfo) {
	ui.Column(c).Width(240).Shrink(0).Padding(8, 12, 16, 12).Gap(2).
		BorderWidth(0, 1, 0, 0).BorderColor(p.border).Children(func() {
		for i := range settingsSections {
			s := &settingsSections[i]
			row := ui.ButtonBase(c.Key(s.id)).Height(46).Padding(0, 12).Gap(10).Radius(10).
				AlignItems(ui.Center).Justify(ui.SpaceBetween).TextColor(p.text)
			switch {
			case s.id == current.id:
				row.Background(p.active)
			case row.Hovered():
				row.Background(p.hover)
			}
			row.Children(func() {
				ui.Column(c).Gap(1).Grow(1).Basis(0).MinWidth(0).Children(func() {
					ui.Text(c, T(s.title)).FontSize(14).FontWeight(500).SingleLine()
					ui.Text(c, T(s.detail)).FontSize(11.5).TextColor(p.muted).SingleLine()
				})
				if s.id == current.id {
					ui.Icon(c, icon("chevron-right")).FontSize(14).TextColor(p.muted)
				}
			})
			if row.Clicked() {
				a.router.Push("/settings/" + s.id)
			}
		}
	})
}

// settingsStatusBar is the band at the foot of the settings page.
func (a *app) settingsStatusBar(c *ui.Context, p palette, info *settingsSectionInfo) {
	state := T("Saved")
	if info.draft && a.draftFor == info.id && !sameSettings(a.draft, a.set) {
		state = T("Unsaved")
	}
	a.statusBar(c, p, []statusItem{
		{T("Section") + " " + T(info.title), ui.Color{}},
		{T("Draft") + " " + state, p.accent},
	}, nil)
}

// sectionTitle heads a group of settings, as the reference's sections do.
func settingsSectionTitle(c *ui.Context, p palette, title string) {
	ui.Text(c, title).FontSize(12).FontWeight(600).LetterSpacing(0.4).TextColor(p.muted).
		Padding(22, 0, 4, 0).SingleLine()
}

// settingRow is a setting: its name over what it does, and its control at
// the right, with a hairline under it.
func settingRow(c *ui.Context, p palette, title, detail string, control func()) {
	ui.Row(c).Key(title).Padding(14, 0).Gap(24).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
		ui.Column(c).Grow(1).Basis(0).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(14).FontWeight(500)
			if detail != "" {
				ui.Text(c, detail).FontSize(12.5).TextColor(p.muted).LineHeight(1.4)
			}
		})
		control()
	})
}

// switchRow is a setting turned on and off.
func switchRow(c *ui.Context, p palette, title, detail string, on *bool) {
	settingRow(c, p, title, detail, func() {
		ui.Switch(c, on).Label(title)
	})
}

func (a *app) settingsEnv(c *ui.Context, p palette, d *settings) {
	// 自检：三项连接与两张表的状态，缺失时给出可直接复制的命令。
	settingsSectionTitle(c, p, T("Self-check"))
	stateRow(c, p, "PostgreSQL",
		a.env.pgDSN,
		a.env.pgErr == "",
		connectionDetail(a.env.pgErr, a.env.pgVector, a.env.docs, a.env.chunks))
	stateRow(c, p, "Ollama", a.env.ollamaURL, a.env.ollamaErr == "", ollamaDetail(a.env))
	stateRow(c, p, T("Language model"), a.env.llmWhere, a.env.llmErr == "", llmDetail(a.env))
	if len(a.env.missing) > 0 {
		settingRow(c, p, T("Missing models"), T("Install them with the commands below, then check again."), func() {
			var b strings.Builder
			for _, m := range a.env.missing {
				b.WriteString("ollama pull " + m + "\n")
			}
			ui.Text(c, b.String()).FontSize(12.5).TextColor(p.muted).Selectable()
		})
	}
	settingRow(c, p, T("Check again"), T("Probe PostgreSQL and Ollama once more."), func() {
		if ui.Button(c, T("Check again")).Clicked() {
			// A connection the settings no longer spell is let go first:
			// what the page shows must be what the app is on.
			if a.store != nil && a.storeDSN != a.connSettings().Connection.PGDSN {
				a.store.Close()
				a.store, a.storeDSN = nil, ""
			}
			a.env = a.checkEnv()
			if a.store == nil {
				a.connect()
			}
		}
	})

	settingsSectionTitle(c, p, T("Database"))
	f := &a.pgForm
	settingRow(c, p, T("Host"), T("Where the database listens."), func() {
		ui.TextInput(c, &f.host).Label(T("Host")).Placeholder("127.0.0.1").Width(300)
	})
	settingRow(c, p, T("Port"), T("The host port; scripts/docker-compose.yml publishes 5433."), func() {
		ui.TextInput(c, &f.port).Label(T("Port")).Placeholder("5432").Width(120).FontFeatures("tnum")
	})
	settingRow(c, p, T("Database"), T("The knowledge base's database, created when it is missing."), func() {
		ui.TextInput(c, &f.database).Label(T("Database")).Placeholder("prior").Width(300)
	})
	settingRow(c, p, T("Username"), T("The role the app connects as."), func() {
		ui.TextInput(c, &f.user).Label(T("Username")).Placeholder("postgres").Width(300)
	})
	settingRow(c, p, T("Password"), T("Hidden here, and saved with the connection string."), func() {
		ui.TextInput(c, &f.password).Password().Label(T("Password")).Placeholder("postgres").Width(300)
	})
	settingRow(c, p, T("Connection string"), T("What these fields add up to, as pgx and prior-cli take it."), func() {
		ui.Text(c, f.dsn()).FontSize(12.5).TextColor(p.muted).Selectable().SingleLine()
	})
	// The fields are the draft's connection: once one of them is typed
	// into, the DSN the draft holds is the one they spell.
	if *f != a.pgFormBase {
		d.Connection.PGDSN = f.dsn()
	}
	settingsSectionTitle(c, p, T("Model service"))
	settingRow(c, p, "Ollama URL", T("The local embedding and LLM service."), func() {
		ui.TextInput(c, &d.Connection.OllamaURL).Label("Ollama URL").
			Placeholder("http://localhost:11434").Width(320)
	})
}

// stateRow is a self-check line: what was probed, where, and how it went.
func stateRow(c *ui.Context, p palette, name, where string, ok bool, detail string) {
	settingRow(c, p, name, where, func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ok {
				ui.Icon(c, icon("check")).FontSize(16).TextColor(p.success)
				ui.Text(c, detail).FontSize(12.5).TextColor(p.muted).SingleLine()
				return
			}
			ui.Icon(c, icon("x")).FontSize(16).TextColor(p.tone("red").bar)
			ui.Text(c, truncateText(detail, 70)).FontSize(12.5).TextColor(p.muted).SingleLine()
		})
	})
}

// connectionDetail says what the database holds, or why it did not answer.
func connectionDetail(errText string, vector bool, docs, chunks int) string {
	if errText != "" {
		return errText
	}
	detail := itoa(docs) + " " + T("docs") + " · " + itoa(chunks) + " " + T("chunks")
	if vector {
		return detail + " · pgvector"
	}
	return detail + " · " + T("no pgvector: vectors are searched in the app")
}

// ollamaDetail says which models the Ollama server holds, and which of the
// embeddings are missing from it.
func ollamaDetail(e env) string {
	if e.ollamaErr != "" {
		return e.ollamaErr
	}
	detail := itoa(len(e.models)) + " " + T("models")
	if e.dim > 0 {
		detail += " · " + T("embedding dim") + " " + itoa(e.dim)
	}
	if len(e.missing) > 0 {
		detail += " · " + T("missing") + " " + strings.Join(e.missing, ", ")
	}
	return detail
}

// llmDetail says where the language model was found, or why it was not.
func llmDetail(e env) string {
	if e.llmErr != "" {
		return e.llmErr
	}
	return itoa(e.llmModels) + " " + T("models")
}

func truncateText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (a *app) settingsAnalyze(c *ui.Context, p palette, d *settings) {
	settingsSectionTitle(c, p, T("Model"))
	settingRow(c, p, T("Embedding model"), T("Sets retrieval quality; rebuilding the index follows a change."), func() {
		ui.Select(c, &d.Models.Embed, []string{"nomic-embed-text", "mxbai-embed-large", "bge-m3"}).Width(220)
	})
	settingRow(c, p, T("Protocol"), T("How the language model answers: Ollama's own API, the OpenAI-compatible one most providers speak, or Anthropic's."), func() {
		labels := make([]string, len(llm.Protocols))
		for i, protocol := range llm.Protocols {
			labels[i] = T(protocolLabel(protocol))
		}
		label := labels[protocolIndexOf(d.Models.Protocol)]
		if ui.Select(c, &label, labels).Label(T("Protocol")).Width(220).Changed() {
			switchProtocol(d, llm.Protocols[slicesIndex(labels, label)])
		}
	})
	settingRow(c, p, T("LLM model"), T("The model that writes findings and explanations."), func() {
		ui.TextInput(c, &d.Models.LLM).Label(T("LLM model")).
			Placeholder(llm.DefaultModel(d.Models.Protocol)).Width(220)
	})
	// An Ollama model answers at the Ollama URL and needs no key: the
	// provider's own address and key are the other protocols'.
	if d.Models.Protocol != "" && d.Models.Protocol != llm.ProtocolOllama {
		settingRow(c, p, T("Endpoint"), T("The provider's base URL, up to but not including /v1: https://api.minimaxi.com/anthropic, https://api.deepseek.com/v1. A missing /v1 is added."), func() {
			ui.TextInput(c, &d.Models.Endpoint).Label(T("Endpoint")).
				Placeholder(llm.DefaultEndpoint(d.Models.Protocol)).Width(300)
		})
		settingRow(c, p, T("API key"), T("Saved in settings.json; sent as a bearer token, or as x-api-key for Anthropic."), func() {
			ui.TextInput(c, &d.Models.APIKey).Password().Label(T("API key")).Placeholder("sk-…").Width(300)
		})
	}

	settingsSectionTitle(c, p, T("Generation"))
	temperature := d.Models.Temperature
	if ui.NumberInput(c, &temperature, 0, 2, 0.1).Label(T("Temperature")).Width(130).Changed() {
		d.Models.Temperature = temperature
	}
	ui.Text(c, T("Temperature")).FontSize(12.5).TextColor(p.muted)
	numCtx := float64(d.Models.NumCtx)
	if ui.NumberInput(c, &numCtx, 512, 32768, 512).Label(T("Context length")).Width(130).Changed() {
		d.Models.NumCtx = int(numCtx)
	}
	timeout := float64(d.Models.TimeoutSec)
	if ui.NumberInput(c, &timeout, 30, 3600, 30).Label(T("Timeout")).Width(130).Changed() {
		d.Models.TimeoutSec = int(timeout)
	}
	// The answer's own room, which a long document's review needs: a stage
	// whose answer is cut is asked again over smaller parts, but a larger
	// answer costs fewer calls.
	maxTokens := float64(d.Models.MaxTokens)
	if ui.NumberInput(c, &maxTokens, 0, 32000, 1024).Label(T("Max answer tokens")).Width(150).Changed() {
		d.Models.MaxTokens = int(maxTokens)
	}
	ui.Text(c, T("0 takes the protocol's default (2400 for Ollama, 8192 for a provider).")).
		FontSize(12).TextColor(p.muted)
}

func (a *app) settingsAppearance(c *ui.Context, p palette, s *settings) {
	settingsSectionTitle(c, p, T("Appearance"))
	settingRow(c, p, T("Theme"), T("Light, dark, or the system's."), func() {
		i := indexOfID(themes, s.Appearance.Theme)
		if ui.Segmented(c, &i, T("System"), T("Light"), T("Dark")).Label(T("Theme")).Changed() {
			s.Appearance.Theme = themes[i].id
			a.applyTheme(s.Appearance.Theme)
			a.saveSettings()
		}
	})
	settingRow(c, p, T("Sidebar color"), T("Tints the sidebar and the window."), func() {
		ui.Row(c).Gap(10).Children(func() {
			for _, sc := range sidebarColors {
				color := sc
				b := ui.ButtonBase(c.Key(color.name)).Size(24, 24).Radius(12).
					Label(color.name).Tooltip(color.name).Background(color.swatch())
				if s.Appearance.SidebarColor == color.name {
					b.Border(2, p.text)
				}
				if b.Clicked() {
					s.Appearance.SidebarColor = color.name
					a.saveSettings()
					a.flash = T("Sidebar color changed to") + " " + sidebarColorLabel(color.name)
				}
			}
		})
	})
	settingRow(c, p, T("Language"), T("The interface language; takes full effect after a restart."), func() {
		labels := make([]string, len(languages))
		for i, l := range languages {
			labels[i] = T(l.label)
		}
		label := labels[indexOfID(languages, s.Appearance.Language)]
		if ui.Select(c, &label, labels).Label(T("Language")).Width(160).Changed() {
			s.Appearance.Language = languages[slicesIndex(labels, label)].id
			a.applyLanguage()
			a.saveSettings()
		}
	})
}

func slicesIndex(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// protocolLabel is a protocol as the settings page names it.
func protocolLabel(protocol string) string {
	switch protocol {
	case llm.ProtocolOpenAI:
		return "OpenAI-compatible"
	case llm.ProtocolAnthropic:
		return "Anthropic (Claude)"
	}
	return "Ollama (local)"
}

// protocolIndexOf is where a protocol sits in the page's list, an empty one
// being Ollama's, which is first.
func protocolIndexOf(protocol string) int {
	if i := slicesIndex(llm.Protocols, protocol); i >= 0 {
		return i
	}
	return 0
}

// switchProtocol moves the language model to another protocol, filling in
// what that protocol answers at and starts on where the settings still hold
// the one before it.
func switchProtocol(d *settings, protocol string) {
	if d.Models.Endpoint == "" || d.Models.Endpoint == llm.DefaultEndpoint(d.Models.Protocol) {
		d.Models.Endpoint = llm.DefaultEndpoint(protocol)
	}
	if d.Models.LLM == "" || d.Models.LLM == llm.DefaultModel(d.Models.Protocol) {
		d.Models.LLM = llm.DefaultModel(protocol)
	}
	d.Models.Protocol = protocol
}

func labelOfSidebarColor(name string) string {
	switch name {
	case "blue":
		return T("Blue")
	case "violet":
		return T("Violet")
	case "pink":
		return T("Pink")
	case "orange":
		return T("Orange")
	case "gold":
		return T("Gold")
	case "green":
		return T("Green")
	case "cyan":
		return T("Cyan")
	default:
		return T("Gray")
	}
}

func (a *app) settingsData(c *ui.Context, p palette) {
	settingsSectionTitle(c, p, T("Library columns"))
	for _, col := range libColumns[1:] {
		column := col
		settingRow(c, p, T(column.title), "", func() {
			hidden := slicesContains(a.set.UI.HiddenColumns, column.id)
			shown := !hidden
			ui.Switch(c, &shown).Label(T(column.title))
			if shown != !hidden {
				if shown {
					a.set.UI.HiddenColumns = slicesDelete(a.set.UI.HiddenColumns, column.id)
				} else {
					a.set.UI.HiddenColumns = append(a.set.UI.HiddenColumns, column.id)
				}
				a.saveSettings()
			}
		})
	}
	settingsSectionTitle(c, p, T("Details panel"))
	settingRow(c, p, T("Show details panel"), T("The details panel beside the library."), func() {
		ui.Switch(c, &a.set.UI.Inspector).Label(T("Show details panel"))
	})
	settingRow(c, p, T("Show sidebar"), T("The navigation sidebar."), func() {
		shown := !a.set.UI.SidebarHidden
		ui.Switch(c, &shown).Label(T("Show sidebar"))
		if shown == a.set.UI.SidebarHidden {
			a.set.UI.SidebarHidden = !shown
			a.saveSettings()
		}
	})
}

func slicesContains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func slicesDelete(list []string, s string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func (a *app) settingsAbout(c *ui.Context, p palette) {
	settingsSectionTitle(c, p, T("About"))
	settingRow(c, p, T("Version"), "", func() { ui.Text(c, "0.1.0").FontSize(12.5).TextColor(p.muted) })
	settingRow(c, p, T("Build"), "", func() { ui.Text(c, "dev").FontSize(12.5).TextColor(p.muted) })
	settingRow(c, p, T("License"), "", func() { ui.Text(c, "MIT").FontSize(12.5).TextColor(p.muted) })
	settingRow(c, p, T("Reference project"), T("The mygo app the interface follows."), func() {
		ui.Text(c, "missuo/motrix-mygo").FontSize(12.5).TextColor(p.muted).SingleLine()
	})
}

// syncPGForm fills the Environment page's connection fields from a DSN,
// and keeps what that DSN spelled: an edit against those is what the page
// writes back, so that opening the page changes nothing.
func (a *app) syncPGForm(dsn string) {
	a.pgFormBase = parsePGDSN(dsn)
	a.pgForm = a.pgFormBase
}

// saveDraftSettings keeps what the form edits and applies it.
func (a *app) saveDraftSettings() {
	theme := a.draft.Appearance.Theme
	a.set = cloneSettings(a.draft)
	a.draftFor = ""
	// The open connection is the one the settings spell, or none: what a
	// page shows of the knowledge base must not come from another database.
	if a.store != nil && a.storeDSN != a.set.Connection.PGDSN {
		a.store.Close()
		a.store, a.storeDSN = nil, ""
	}
	if a.settingsPath != "" {
		a.set.save(a.settingsPath)
	}
	a.applyTheme(theme)
	a.flash = T("Settings saved")
}

// discardDraftSettings drops what the form edits, back to what is saved.
func (a *app) discardDraftSettings() {
	a.draft = cloneSettings(a.set)
	a.draftFor = ""
	a.syncPGForm(a.draft.Connection.PGDSN)
	a.flash = T("Changes discarded")
}

// resetDraftSettings fills the draft with the defaults, keeping the
// appearance the window follows until Save.
func (a *app) resetDraftSettings() {
	draft := defaultSettings()
	draft.Appearance = a.set.Appearance
	a.draft = draft
	a.draftFor = a.currentSectionID()
	a.syncPGForm(a.draft.Connection.PGDSN)
	a.flash = T("Defaults restored; save to apply")
}

func (a *app) currentSectionID() string {
	section := strings.TrimPrefix(a.router.Path(), "/settings/")
	if findSettingsSection(section) == nil {
		return ""
	}
	return section
}

// applyTheme has the window follow the appearance. Tests run without one,
// where the theme source is the desktop's and cannot be set.
func (a *app) applyTheme(theme string) {
	if a.win == nil {
		return
	}
	switch theme {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}

func settingsPath() string {
	p, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return ""
	}
	return p + "/settings.json"
}
