package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"prior/llm"
	"prior/store"
)

func TestRoutes(t *testing.T) {
	a := newTestApp(t)

	routes := []string{
		"/library", "/analyze", "/reports", "/report/1", "/history", "/modules", "/notifications",
		"/settings", "/settings/analyze", "/settings/appearance", "/settings/data",
	}
	sizes := [][2]int{{1024, 680}, {960, 620}}
	for _, path := range routes {
		a.router.Push(path)
		for _, size := range sizes {
			ui.Render(a.view, size[0], size[1], 1.0)
		}
		a.router.View(nil, func(r *ui.Route) {
			if !r.Match(path) {
				t.Fatalf("expected %s, got %s", path, r.Path())
			}
		})
	}
}

func TestFuzzyMatch(t *testing.T) {
	if !fuzzyMatch("历史", "历") {
		t.Fatal("expected fuzzyMatch to find 历 in 历史")
	}
	if fuzzyMatch("设置", "历") {
		t.Fatal("expected fuzzyMatch to not find 历 in 设置")
	}
	if !fuzzyMatch("", "") {
		t.Fatal("expected empty query to match everything")
	}
}

func TestPaletteTints(t *testing.T) {
	blue := paletteFor(&ui.Theme{}, sidebarColors[0])
	green := paletteFor(&ui.Theme{}, findSidebarColor("green"))
	gray := paletteFor(&ui.Theme{}, findSidebarColor("gray"))
	if blue.appBg == green.appBg {
		t.Fatal("expected the sidebar color to tint the window")
	}
	if gray.appBg == blue.appBg {
		t.Fatal("expected the gray sidebar color to wash the tint out")
	}
	if blue.accent != ui.Hex("#2f7df6") || blue.text != ui.RGB(10, 10, 10) {
		t.Fatalf("unexpected light accent or text: %v %v", blue.accent, blue.text)
	}
	if blue.theme(&ui.Theme{}).Radius != 8 {
		t.Fatal("expected the widgets' radius to be 8")
	}
}

func TestDocSorting(t *testing.T) {
	a := newTestApp(t)
	libSortBy, libSortDesc = "", false
	libFilter = "all"
	rows := a.filterDocs()
	if len(rows) != len(a.docs) {
		t.Fatalf("expected all documents, got %d", len(rows))
	}

	libSortBy = "chunks"
	asc := a.filterDocs()
	for i := 1; i < len(asc); i++ {
		if asc[i-1].Chunks > asc[i].Chunks {
			t.Fatalf("expected ascending chunks, got %d before %d", asc[i-1].Chunks, asc[i].Chunks)
		}
	}
	libSortDesc = true
	desc := a.filterDocs()
	for i := 1; i < len(desc); i++ {
		if desc[i-1].Chunks < desc[i].Chunks {
			t.Fatalf("expected descending chunks, got %d before %d", desc[i-1].Chunks, desc[i].Chunks)
		}
	}
	if asc[0].ID == desc[0].ID {
		t.Fatal("expected sorting the other way to move the first row")
	}
	libSortBy, libSortDesc = "", false

	libFilter = "active"
	active := a.filterDocs()
	for _, d := range active {
		if d.Status != "active" {
			t.Fatalf("expected only active documents, got %s", d.Status)
		}
	}
	libFilter = "all"

	libSearchQuery = "优惠券"
	found := a.filterDocs()
	if len(found) != 1 || found[0].Title != "优惠券叠加规则 v0.9" {
		t.Fatalf("expected the coupon document, got %v", found)
	}
	libSearchQuery = ""
}

func TestVisibleColumns(t *testing.T) {
	a := newTestApp(t)
	if len(a.visibleColumns()) != len(libColumns) {
		t.Fatal("expected every column while none are hidden")
	}
	a.set.UI.HiddenColumns = []string{"format", "chunks"}
	cols := a.visibleColumns()
	if len(cols) != len(libColumns)-2 {
		t.Fatalf("expected two columns hidden, got %d", len(cols))
	}
	for _, col := range cols {
		if col.id == "format" || col.id == "chunks" {
			t.Fatalf("expected %s to be hidden", col.id)
		}
	}
}

func TestFilterCommands(t *testing.T) {
	a := newTestApp(t)
	cmds := a.filterCommands("历")
	if len(cmds) == 0 {
		t.Fatal("expected at least one command matching 历")
	}
	found := false
	for _, cmd := range cmds {
		if cmd.title == "历史" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected 历史 command in filtered results")
	}
}

func TestFilterFindings(t *testing.T) {
	a := newTestApp(t)
	cs := a.filterFindings(0, "all", "")
	if len(cs) != 3 {
		t.Fatalf("expected 3 change_signal findings, got %d", len(cs))
	}
	if cs[0].Severity != "S0" {
		t.Fatalf("expected first change_signal to be S0, got %s", cs[0].Severity)
	}
	ol := a.filterFindings(3, "all", "")
	if len(ol) != 2 {
		t.Fatalf("expected 2 one_liner findings, got %d", len(ol))
	}
}

// TestFilteredRuns covers the report list's status filter and search, which
// live on the run titles.
func TestFilteredRuns(t *testing.T) {
	a := newTestApp(t)
	reportListFilter, reportListQuery = "all", ""
	if rows := a.filteredRuns(); len(rows) != len(a.runs) {
		t.Fatalf("all reports = %d, want %d", len(rows), len(a.runs))
	}
	reportListFilter = "failed"
	if rows := a.filteredRuns(); len(rows) != 1 || rows[0].Status != "failed" {
		t.Fatalf("failed reports = %+v", rows)
	}
	reportListFilter, reportListQuery = "all", "消息推送"
	if rows := a.filteredRuns(); len(rows) != 1 || !strings.Contains(rows[0].Title, "消息推送") {
		t.Fatalf("search reports = %+v", rows)
	}
	reportListFilter, reportListQuery = "all", ""
}

func TestSettings(t *testing.T) {
	s := defaultSettings()
	if s.Version != settingsVersion {
		t.Fatalf("expected version %d, got %d", settingsVersion, s.Version)
	}
	if !sameSettings(s, defaultSettings()) {
		t.Fatal("expected sameSettings to return true for identical settings")
	}
	s.UI.Inspector = false
	if sameSettings(s, defaultSettings()) {
		t.Fatal("expected sameSettings to return false for different settings")
	}
}

// TestSidebarToggleRoundTrip covers the collapsed sidebar: the chrome
// button beside the window controls is what shows it again, since a rail of
// width 0 shrinks its own items to nothing.
func TestSidebarToggleRoundTrip(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/library")

	tester := ui.NewTester(a.view, 1024, 680)
	tester.ClickAt(34, 26) // the chrome button beside the window controls
	tester.Frame()
	if !a.set.UI.SidebarHidden {
		t.Fatal("clicking the chrome button did not collapse the sidebar")
	}

	tester.ClickAt(34, 26)
	tester.Frame()
	if a.set.UI.SidebarHidden {
		t.Fatal("collapsed sidebar could not be expanded again")
	}
}

// TestLibraryHeaderMenus opens the library header's menus and chooses from
// them, as the header's own buttons do.
func TestLibraryHeaderMenus(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/library")
	tester := ui.NewTester(a.view, 1024, 680)

	filter, ok := tester.Find("6/6")
	if !ok {
		t.Fatal("the count chip is not on the page")
	}
	tester.ClickAt(filter.X+filter.W/2, filter.Y+filter.H/2)
	if items := tester.Menu(); len(items) == 0 {
		t.Fatal("the filter chip opened no menu")
	}
	if err := tester.ChooseMenuItem("活跃"); err != nil {
		t.Fatal(err)
	}
	if libFilter != "active" {
		t.Fatalf("expected the active filter, got %q", libFilter)
	}
	rows := a.filterDocs()
	if len(rows) != 3 {
		t.Fatalf("expected 3 active documents, got %d", len(rows))
	}

	libFilter = "all"
	tester.CloseMenu()
	columns, ok := tester.Find("显示列")
	if !ok {
		t.Fatal("the columns button is not on the page")
	}
	tester.ClickAt(columns.X+columns.W/2, columns.Y+columns.H/2)
	menu := tester.Menu()
	if len(menu) == 0 {
		t.Fatal("the columns button opened no menu")
	}
	if err := tester.ChooseMenuItem("格式"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(a.set.UI.HiddenColumns, "format") {
		t.Fatalf("expected the format column hidden, got %v", a.set.UI.HiddenColumns)
	}
	if slices.Contains(a.visibleColumns(), libColumn{id: "format"}) {
		t.Fatal("the format column is still visible")
	}
}

// TestRowMenus opens a row's context menu and chooses from it.
func TestRowMenus(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/library")
	tester := ui.NewTester(a.view, 1024, 680)

	if err := tester.RightClick("会员积分规则 PRD v2.1"); err != nil {
		t.Fatal(err)
	}
	menu := tester.Menu()
	if len(menu) == 0 {
		t.Fatal("the row opened no context menu")
	}
	// The two actions the library offers beside opening and copying.
	for _, want := range []string{"下载文档原文", "删除文档…"} {
		if !slices.Contains(menu, want) {
			t.Fatalf("the row menu reads %v, without %q", menu, want)
		}
	}
	if err := tester.ChooseMenuItem("复制标题"); err != nil {
		t.Fatal(err)
	}
	if got := tester.Clipboard(); got != "会员积分规则 PRD v2.1" {
		t.Fatalf("expected the row's title on the clipboard, got %q", got)
	}

	a.router.Push("/notifications")
	tester.Frame()
	unread := 0
	for _, n := range a.notes {
		if !n.Read {
			unread++
		}
	}
	if unread == 0 {
		t.Fatal("expected unread notifications to start with")
	}
	if err := tester.RightClick(a.notes[0].Title); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem("标为已读"); err != nil {
		t.Fatal(err)
	}
	if !a.notes[0].Read {
		t.Fatal("choosing 标为已读 did not mark the notification read")
	}
}

// TestFindingStatusWritesThrough covers the finding actions writing to the
// run's findings rather than to the row's copy of one.
func TestFindingStatusWritesThrough(t *testing.T) {
	a := newTestApp(t)
	id := a.findings[0].ID
	if a.findings[0].Status != "open" {
		t.Fatalf("expected an open finding, got %q", a.findings[0].Status)
	}
	a.setFindingStatus(id, "confirmed")
	if a.findings[0].Status != "confirmed" {
		t.Fatalf("expected the finding confirmed, got %q", a.findings[0].Status)
	}
	if a.pendingFindings() != len(a.findings)-1 {
		t.Fatalf("expected %d pending findings, got %d", len(a.findings)-1, a.pendingFindings())
	}
}

// TestSettingsDraftSurvivesFrames covers the draft holding what the form
// edits instead of being reset to the saved settings every frame.
func TestSettingsDraftSurvivesFrames(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings/analyze")
	tester := ui.NewTester(a.view, 1024, 680)

	a.draft.Models.LLM = "deepseek-r1:32b"
	a.draft.Models.Temperature = 0.7
	for range 3 {
		tester.Frame()
	}
	if a.draft.Models.LLM != "deepseek-r1:32b" || a.draft.Models.Temperature != 0.7 {
		t.Fatalf("the draft lost the edits: %+v", a.draft.Models)
	}
	if sameSettings(a.set, a.draft) {
		t.Fatal("expected the saved settings to still differ from the draft")
	}

	a.saveDraftSettings()
	if a.set.Models.LLM != "deepseek-r1:32b" {
		t.Fatalf("saving the draft left the settings at %q", a.set.Models.LLM)
	}
	a.draft.Models.LLM = "qwen2.5:32b"
	a.discardDraftSettings()
	if a.draft.Models.LLM != "deepseek-r1:32b" {
		t.Fatalf("discarding left the draft at %q", a.draft.Models.LLM)
	}
}

// TestFindingSelectionKeys covers J and K moving the finding the page has
// chosen.
func TestFindingSelectionKeys(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/report/1")
	tester := ui.NewTester(a.view, 1024, 680)
	if reportCurrent != -1 {
		t.Fatalf("expected no finding chosen to start with, got %d", reportCurrent)
	}
	tester.Key(0, ui.KeyJ)
	if reportCurrent != 0 {
		t.Fatalf("expected J to choose the first finding, got %d", reportCurrent)
	}
	tester.Key(0, ui.KeyJ)
	tester.Key(0, ui.KeyJ)
	if reportCurrent != 2 {
		t.Fatalf("expected J to move down twice, got %d", reportCurrent)
	}
	tester.Key(0, ui.KeyK)
	if reportCurrent != 1 {
		t.Fatalf("expected K to move up, got %d", reportCurrent)
	}
	tester.Key(0, ui.KeyK)
	tester.Key(0, ui.KeyK)
	if reportCurrent != 0 {
		t.Fatalf("expected K to stop at the first finding, got %d", reportCurrent)
	}
}

// TestAnalyzePageFlows covers the analysis page offering the two flows of
// the business process, each with its own entry.
func TestAnalyzePageFlows(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/analyze")
	tester := ui.NewTester(a.view, 1024, 680)
	for _, want := range []string{"导入目录", "评审新文档", "实时日志", "运行设置"} {
		if !tester.HasText(want) {
			t.Errorf("the analysis page does not show %q", want)
		}
	}
	if !tester.HasText("知识库") {
		t.Error("the analysis page does not say what the knowledge base holds")
	}
}

// TestSettingsSectionsRender covers the section routes showing their own
// form rather than falling back to the document library.
func TestSettingsSectionsRender(t *testing.T) {
	sections := []struct{ path, mark string }{
		{"/settings", "环境自检"},
		{"/settings/env", "环境自检"},
		{"/settings/analyze", "嵌入模型"},
		{"/settings/appearance", "侧边栏配色"},
		{"/settings/data", "文档库列"},
		{"/settings/about", "许可证"},
		{"/settings/nonsense", "环境自检"},
	}
	for _, s := range sections {
		a := newTestApp(t)
		a.router.Push(s.path)
		tester := ui.NewTester(a.view, 1024, 680)
		if !tester.HasText(s.mark) {
			t.Fatalf("%s shows no %q", s.path, s.mark)
		}
		if tester.HasText("会员积分规则 PRD v2.1") {
			t.Fatalf("%s fell back to the document library", s.path)
		}
	}
}

// TestReportRouteParam covers a report route naming its run: the report opens
// in its dialog over the list, and an unknown id opens nothing rather than
// falling back to another document's review.
func TestReportRouteParam(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/report/2")
	tester := ui.NewTester(a.view, 1024, 900)
	tester.Frame()
	if !a.reportOpen || a.reportRun != 2 {
		t.Fatalf("the route opened report %d (open=%v), want run 2", a.reportRun, a.reportOpen)
	}
	if !tester.HasText(a.runs[1].Title) {
		t.Fatalf("/report/2 does not show run 2 (%q)", a.runs[1].Title)
	}
	a.router.Push("/report/99")
	tester.Frame()
	if a.reportOpen {
		t.Fatal("an unknown run opened a report")
	}
	if got := a.router.Path(); got != "/reports" {
		t.Errorf("an unknown report left the route at %q, want /reports", got)
	}
}

// TestPageShortcuts covers the keys the view itself handles, beside the
// application-wide ones.
func TestPageShortcuts(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/library")
	tester := ui.NewTester(a.view, 1024, 680)

	if libSearching {
		t.Fatal("the search field starts hidden")
	}
	tester.Key(ui.Cmd, ui.KeyF)
	if !libSearching {
		t.Fatal("Cmd+F did not show the search field")
	}
	tester.Key(ui.Cmd, ui.KeyF)
	if libSearching {
		t.Fatal("Cmd+F did not hide the search field")
	}

	if a.set.UI.SidebarHidden {
		t.Fatal("the sidebar starts shown")
	}
	tester.Key(ui.Cmd, ui.KeyB)
	if !a.set.UI.SidebarHidden {
		t.Fatal("Cmd+B did not hide the sidebar")
	}
	tester.Key(ui.Cmd, ui.KeyB)
	if a.set.UI.SidebarHidden {
		t.Fatal("Cmd+B did not show the sidebar again")
	}

	// Cmd+S keeps the settings draft. A key matches the frame last built,
	// so the edit takes a frame of its own, as typing one does.
	a.router.Push("/settings/analyze")
	tester.Frame()
	a.draft.Models.NumCtx = 8192
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyS)
	if a.set.Models.NumCtx != 8192 {
		t.Fatalf("Cmd+S did not save the draft, NumCtx is %d", a.set.Models.NumCtx)
	}
}

// TestSettingsSaveStaysOffDisk covers saving with no file configured, as in
// these tests: the settings change in memory and write nothing.
func TestSettingsSaveStaysOffDisk(t *testing.T) {
	a := newTestApp(t)
	if a.settingsPath != "" {
		t.Fatalf("a test app should have no settings file, got %q", a.settingsPath)
	}
	a.router.Push("/settings/env")
	a.draft.Connection.OllamaURL = "http://127.0.0.1:9999"
	a.saveDraftSettings()
	if a.set.Connection.OllamaURL != "http://127.0.0.1:9999" {
		t.Fatal("saving did not take the draft into the settings")
	}
}

// TestSettingsConnectionFields covers the Environment page's connection
// fields: the DSN the draft holds is the one they spell, so that the host,
// the port, the user and the password are edited where they show.
func TestSettingsConnectionFields(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings/env")
	tester := ui.NewTester(a.view, 1024, 1200)
	tester.Frame()
	for _, label := range []string{"主机", "端口", "数据库", "用户名", "密码", "连接串"} {
		if !tester.HasText(label) {
			t.Fatalf("the page shows no %q", label)
		}
	}
	if a.pgForm != parsePGDSN("postgres://postgres:postgres@127.0.0.1:5433/prior") {
		t.Fatalf("the page opened on the fields %+v", a.pgForm)
	}
	a.pgForm.port, a.pgForm.password = "5432", "s3cret"
	tester.Frame()
	if got := a.draft.Connection.PGDSN; got != "postgres://postgres:s3cret@127.0.0.1:5432/prior" {
		t.Fatalf("the fields left the DSN at %q", got)
	}
	// Discarding the draft puts the fields back where the saved DSN has
	// them, so that the page cannot show one connection and keep another.
	a.discardDraftSettings()
	if a.pgForm.port != "5433" || a.pgForm.password != "postgres" {
		t.Fatalf("discarding left the fields at %+v", a.pgForm)
	}
}

// TestSettingsProviderFields covers the analysis page's language model: a
// provider protocol shows its endpoint and key, and switching protocol
// fills in that protocol's own defaults.
func TestSettingsProviderFields(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings/analyze")
	tester := ui.NewTester(a.view, 1024, 1000)
	tester.Frame()
	if !tester.HasText("协议") || !tester.HasText("Ollama（本地）") {
		t.Fatal("the page shows no protocol choice")
	}
	if tester.HasText("端点") {
		t.Fatal("an Ollama model needs no endpoint of its own")
	}

	// Switching to a provider fills in its endpoint and a model to start
	// on, and shows the fields that answer for it.
	switchProtocol(&a.draft, llm.ProtocolOpenAI)
	tester.Frame()
	if a.draft.Models.Endpoint != "https://api.openai.com/v1" {
		t.Fatalf("the switch left the endpoint at %q", a.draft.Models.Endpoint)
	}
	if a.draft.Models.LLM != llm.DefaultModel(llm.ProtocolOpenAI) {
		t.Fatalf("the switch left the model at %q", a.draft.Models.LLM)
	}
	for _, label := range []string{"端点", "API Key", "OpenAI 兼容"} {
		if !tester.HasText(label) {
			t.Fatalf("the page shows no %q", label)
		}
	}
	// A key typed for one provider is the user's, not the protocol's: it
	// stays when the protocol changes. An endpoint and model typed by hand
	// are the user's too.
	a.draft.Models.APIKey = "sk-typed"
	a.draft.Models.Endpoint = "https://api.deepseek.com/v1"
	switchProtocol(&a.draft, llm.ProtocolAnthropic)
	if a.draft.Models.APIKey != "sk-typed" {
		t.Fatalf("the switch cleared the key: %+v", a.draft.Models)
	}
	if a.draft.Models.Endpoint != "https://api.deepseek.com/v1" {
		t.Fatalf("the switch dropped an endpoint typed by hand: %q", a.draft.Models.Endpoint)
	}
}

// TestModelClientFollowsTheProtocol covers the client the pipeline runs
// with: an Ollama model calls the Ollama URL, a provider its endpoint.
func TestModelClientFollowsTheProtocol(t *testing.T) {
	set := defaultSettings()
	if got := llmClient(set).Endpoint(); got != set.Connection.OllamaURL {
		t.Fatalf("an Ollama model calls %q", got)
	}
	set.Models.Protocol = llm.ProtocolOpenAI
	set.Models.Endpoint = "https://api.deepseek.com/v1"
	set.Models.APIKey = "sk-test"
	gen := llmClient(set)
	if gen.Endpoint() != "https://api.deepseek.com/v1" || gen.Protocol() != llm.ProtocolOpenAI {
		t.Fatalf("the provider client reads %s %s", gen.Protocol(), gen.Endpoint())
	}
	// The embedding client is Ollama's, whatever the language model is.
	if got := modelClient(set).BaseURL; got != "http://localhost:11434" {
		t.Fatalf("the embeddings go to %q", got)
	}
}

// TestDownloadCopiesTheOriginal covers downloading a document's own file:
// the bytes written are the file's, and a document with no file of its own
// says so rather than opening a panel that cannot help.
func TestDownloadCopiesTheOriginal(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "原文.docx")
	content := []byte("先例：文档原文的字节")
	if err := os.WriteFile(source, content, 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "副本.docx")
	asked := ""
	defer func() { askSaveDialog = mygo.Dialog.Save }()
	askSaveDialog = func(opts mygo.SaveDialogOptions) (string, error) {
		asked = opts.DefaultPath
		return target, nil
	}

	a := newApp(defaultSettings())
	a.downloadDocument(doc{ID: 7, Title: "原文", SourceURI: source})
	waitFlash(t, a)
	if asked != "原文.docx" {
		t.Errorf("保存面板的默认文件名 = %q，应当接着原文的文件名", asked)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("副本内容 = %q，want %q", got, content)
	}
	if !strings.Contains(a.flash, target) {
		t.Errorf("提示 = %q，应当说出写到哪儿", a.flash)
	}

	// A document with no file of its own: no panel, and the reason why.
	asked, a.flash = "", ""
	a.downloadDocument(doc{ID: 8, Title: "示例文档", SourceURI: ""})
	if asked != "" {
		t.Error("没有原文件时不该打开保存面板")
	}
	if !strings.Contains(a.flash, "没有可下载的原文件") {
		t.Errorf("提示 = %q", a.flash)
	}
}

// TestDownloadReportsAMissingFile covers the file having moved or gone: the
// download says so instead of writing an empty copy.
func TestDownloadReportsAMissingFile(t *testing.T) {
	a := newApp(defaultSettings())
	asked := false
	defer func() { askSaveDialog = mygo.Dialog.Save }()
	askSaveDialog = func(mygo.SaveDialogOptions) (string, error) {
		asked = true
		return "", nil
	}
	a.downloadDocument(doc{ID: 9, Title: "已经搬走的文档", SourceURI: filepath.Join(t.TempDir(), "不在.docx")})
	waitFlash(t, a)
	if asked {
		t.Error("原文件不在时不该打开保存面板")
	}
	if !strings.Contains(a.flash, "原文件已不在") {
		t.Errorf("提示 = %q", a.flash)
	}
}

// TestDeleteWaitsForConfirmation covers the delete asking first: a document
// goes only through the alert, and a delete with no knowledge base behind it
// says so rather than opening an alert that cannot act.
func TestDeleteWaitsForConfirmation(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/library")
	tester := ui.NewTester(a.view, 1024, 680)
	tester.Frame()

	target := a.docs[0]
	a.askDelete(target)
	if a.deleteOpen {
		t.Fatal("没有知识库时不该打开确认框")
	}
	if !strings.Contains(a.flash, "知识库未连接") {
		t.Errorf("提示 = %q", a.flash)
	}

	// With a knowledge base behind it, the alert is what removes anything:
	// until it is answered, the document is still there.
	a.store = &store.Store{}
	a.flash = ""
	a.askDelete(target)
	if !a.deleteOpen || a.deleteDoc.ID != target.ID {
		t.Fatalf("确认框 = %v，目标 = %+v", a.deleteOpen, a.deleteDoc)
	}
	tester.Frame()
	if !tester.HasText("删除这篇文档？") || !tester.HasText(target.Title) {
		t.Fatalf("确认框没有说出要删的是哪一篇：%v", tester.Texts())
	}
	// Escape closes it choosing nothing, and the document stays.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if a.deleteOpen {
		t.Fatal("Escape 之后确认框还开着")
	}
	if !tester.HasText(target.Title) {
		t.Fatalf("确认框没有说出要删的是哪一篇：%v", tester.Texts())
	}
}

// waitFlash waits for the window's own thread to be told something, which a
// download does from a goroutine of its own.
func waitFlash(t *testing.T, a *app) {
	t.Helper()
	for i := 0; i < 300 && a.flash == ""; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if a.flash == "" {
		t.Fatal("下载没有任何提示")
	}
}

// newTestApp is the app the tests drive: page changes show at once, since
// pages take no clicks while they slide in.
func newTestApp(t *testing.T) *app {
	t.Helper()
	a := newApp(defaultSettings())
	a.router.Transition = ui.TransitionNone
	return a
}

// TestAppMenu covers the menu bar the app installs on macOS: the standard
// menus, and the View and Go items that mirror the window's own controls.
func TestAppMenu(t *testing.T) {
	a := newTestApp(t)
	menu := appMenu(a)
	if runtime.GOOS != "darwin" {
		if menu != nil {
			t.Fatal("expected no application menu off macOS")
		}
		return
	}
	if menu == nil {
		t.Fatal("expected an application menu on macOS")
	}
	labels := []string{}
	for _, item := range menu.Items() {
		labels = append(labels, item.Label)
	}
	for _, want := range []string{"先例", "视图", "前往"} {
		found := false
		for _, item := range menu.Items() {
			if item.Label == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("the menu has no %q among %v", want, labels)
		}
		if want == "先例" {
			continue
		}
	}
	// The View submenu toggles what the window's chrome buttons do.
	view := menuItemByLabel(t, menu.Items(), "视图")
	if view.Submenu == nil {
		t.Fatal("视图 has no submenu")
	}
	toggle := menuItemByLabel(t, view.Submenu, "显示侧边栏")
	if toggle.Accelerator != "CmdOrCtrl+Shift+B" {
		t.Fatalf("unexpected accelerator %q", toggle.Accelerator)
	}
	if a.set.UI.SidebarHidden {
		t.Fatal("the sidebar starts shown")
	}
	toggle.Click(toggle, nil)
	if !a.set.UI.SidebarHidden {
		t.Fatal("the View menu item did not hide the sidebar")
	}
}

func menuItemByLabel(t *testing.T, items []*mygo.MenuItem, label string) *mygo.MenuItem {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("no menu item %q", label)
	return nil
}

// TestLanguageSwitch covers the interface following the language: the same
// pages read in Simplified Chinese and in English, with the data they show
// keeping its own language.
func TestLanguageSwitch(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/library")
	setLanguage("zh", "")
	tester := ui.NewTester(a.view, 1024, 680)
	if !tester.HasText("文档库") {
		t.Fatalf("the Chinese library is missing its title: %q", tester.Texts())
	}
	if !tester.HasText("活跃") {
		t.Fatal("the Chinese status label is missing")
	}
	tester.Frame()

	setLanguage("en", "")
	tester.Frame()
	if !tester.HasText("Library") {
		t.Fatalf("the English library is missing its title: %q", tester.Texts())
	}
	if !tester.HasText("Active") {
		t.Fatal("the English status label is missing")
	}
	if tester.HasText("文档库") {
		t.Fatal("the Chinese title still shows in English")
	}
	// The data keeps its own language, as documents do.
	if !tester.HasText("会员积分规则 PRD v2.1") {
		t.Fatal("the document titles should keep their own language")
	}

	// The locale picks the language when the setting follows the system.
	setLanguage("system", "zh-Hans")
	if T("Library") != "文档库" {
		t.Fatalf("a Chinese locale should read Chinese, got %q", T("Library"))
	}
	setLanguage("system", "en-US")
	if T("Library") != "Library" {
		t.Fatalf("an English locale should read English, got %q", T("Library"))
	}
	setLanguage("zh", "")
}

// TestCatalogueCoversInterfaceCopy covers every key the interface asks for
// having a Chinese text, so nothing falls back to English mid-interface.
func TestCatalogueCoversInterfaceCopy(t *testing.T) {
	setLanguage("zh", "")
	missing := []string{}
	for en, zh := range zhCN {
		if zh == "" {
			t.Fatalf("the catalogue has an empty text for %q", en)
		}
	}
	// Keys the interface uses that are only ever built at run time: they
	// must be in the catalogue, since no Chinese text is written in the
	// view code.
	for _, key := range []string{
		"Library", "Analyze", "Report", "History", "Notifications", "Settings",
		"Active", "Superseded", "Draft", "Completed", "Degraded", "Failed", "Canceled", "Running",
		"Change signal", "Logic gap", "Ambiguity", "One-line request",
		"Parsing documents", "Building the index", "Retrieving changes",
		"Detecting S0", "Detecting S1", "Detecting S2", "Summarizing",
		"Trading", "Payment", "Marketing", "Membership", "Platform", "Risk",
		"System", "Light", "Dark", "Simplified Chinese", "English",
		"Blue", "Violet", "Pink", "Orange", "Gold", "Green", "Cyan", "Gray",
		"Environment", "Analysis", "Appearance", "Data", "About",
	} {
		if _, ok := zhCN[key]; !ok {
			missing = append(missing, key)
		}
		// A few labels read the same in both languages, as "English" does.
		if T(key) == key && key != "English" {
			missing = append(missing, key+" (untranslated)")
		}
	}
	if len(missing) > 0 {
		t.Fatalf("the Chinese catalogue is missing %v", missing)
	}
}

// fixtureTexts is every text the fixtures hold, which the pages show in
// the documents' own language whatever the interface's is.
func fixtureTexts(a *app) map[string]bool {
	out := map[string]bool{}
	add := func(texts ...string) {
		for _, s := range texts {
			if s != "" {
				out[s] = true
			}
		}
	}
	for _, d := range a.docs {
		add(d.Title, d.Format, d.ProductLine, d.Status, d.Imported)
	}
	for _, r := range a.runs {
		add(r.Title, r.Status, r.Stage, r.StartedAt, r.Elapsed)
	}
	for _, f := range a.findings {
		add(f.ID, f.Type, f.Subtype, f.Severity, f.Title, f.Detail, f.NewQuote, f.NewPath,
			f.OldQuote, f.OldPath, f.OldDocTitle, f.Question, f.Impact, f.Status)
	}
	for _, n := range a.notes {
		add(n.Title, n.Time, n.ReportID)
	}
	return out
}

// TestNoUntranslatedCopyOnAnyPage covers every page drawing its copy in the
// language chosen: in English no Chinese text of the catalogue shows, and
// in Chinese no key does. It is what catches a table or a field filled
// once, before the language is known.
func TestNoUntranslatedCopyOnAnyPage(t *testing.T) {
	zhOf := map[string]string{} // key -> Chinese text, where they differ
	zhTexts := map[string]string{}
	for en, zh := range zhCN {
		if en != zh && !strings.Contains(en, "%") {
			zhOf[en] = zh
			zhTexts[zh] = en
		}
	}
	// What the app shows of its own documents is in their language, so a
	// text equal to one of theirs is data, not copy.
	blank := newTestApp(t)
	data := fixtureTexts(blank)
	routes := []string{
		"/library", "/analyze", "/reports", "/report/1", "/history", "/modules", "/notifications",
		"/settings", "/settings/env", "/settings/analyze", "/settings/appearance",
		"/settings/data", "/settings/about",
	}
	for _, route := range routes {
		for _, lang := range []string{"en", "zh"} {
			setLanguage(lang, "")
			a := newTestApp(t)
			a.currentDoc = 1
			a.router.Push(route)
			// Search fields show their placeholder, which the framework
			// would otherwise write in the locale's own language.
			libSearching, reportSearching = true, true
			tester := ui.NewTester(a.view, 1024, 680)
			libSearching, reportSearching = false, false
			for _, text := range tester.Texts() {
				if data[text] {
					continue
				}
				switch lang {
				case "en":
					if en, ok := zhTexts[text]; ok {
						t.Errorf("%s in English shows %q, the Chinese text of %q", route, text, en)
					}
				case "zh":
					if _, ok := zhOf[text]; ok {
						t.Errorf("%s in Chinese shows the key %q", route, text)
					}
				}
			}
		}
	}
	setLanguage("zh", "")
}

// TestPluralLabel carries numbers through a template key, which the
// coverage tests skip: it must still read in the language chosen.
func TestPluralLabel(t *testing.T) {
	setLanguage("zh", "")
	if got := T("%d docs / %d chunks"); got != "%d 篇 / %d 块" {
		t.Fatalf("the pill's template reads %q in Chinese", got)
	}
	setLanguage("en", "")
	if got := T("%d docs / %d chunks"); got != "%d docs / %d chunks" {
		t.Fatalf("the pill's template reads %q in English", got)
	}
	setLanguage("zh", "")
	if _, isText := zhCN["docs"]; isText {
		t.Fatal("a unit key collides with the chunks column id")
	}
}
