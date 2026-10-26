package main

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"prior/store"
)

var (
	libFilter      = "all"
	libSearchQuery string
	libSearching   bool
	libSortBy      string
	libSortDesc    bool
	libSelected    = -1
)

// libColumn is a column of the document table, as the settings hide them.
type libColumn struct {
	id, title string
	width     float32 // 0 takes the room the others leave
	align     ui.Align
}

// The columns add up to what the table can show beside the details panel
// at the window's least width (960): wider headings would cut the last
// column off instead.
var libColumns = []libColumn{
	{"name", "Title", 0, ui.Start},
	{"date", "Date", 96, ui.Start},
	{"module", "Module", 110, ui.Start},
	{"format", "Format", 52, ui.Start},
	{"chunks", "Chunks", 44, ui.End},
	{"status", "Status", 72, ui.Start},
}

const (
	nameMinWidth = 120
	rowPadding   = 12
)

// shortTime is a timestamp as the table's column shows it, without its
// year: the inspector keeps the whole one.
func shortTime(s string) string {
	if len(s) >= 16 {
		return s[5:16]
	}
	return s
}

// rowWidth is the least width a row of the columns takes.
func rowWidth(cols []libColumn) float32 {
	w := float32(2*rowPadding + nameMinWidth)
	for _, col := range cols {
		w += col.width
	}
	return w
}

func (a *app) libraryView(c *ui.Context, p palette) {
	rows := a.sortedAndFiltered()
	if libSelected >= len(rows) {
		libSelected = len(rows) - 1
	}
	if libSelected >= 0 {
		a.currentDoc = rows[libSelected].ID
	}
	a.loadChunks()

	a.pageHeader(c, p, T("Library"), func() {
		filter := ui.ButtonBase(c.Key("filter")).Gap(6).Padding(4, 8).Margin(0, 0, 0, -8).Radius(8).
			AlignItems(ui.Center).Tooltip(T("Filter documents"))
		filter.Children(func() {
			ui.Textf(c, "%d/%d", len(rows), len(a.docs)).FontSize(12.5).FontWeight(600).FontFeatures("tnum").
				Padding(2, 9).Radius(11).Border(1, p.border).SingleLine()
			ui.Icon(c, icon("chevron-down")).FontSize(14).TextColor(p.muted)
		}).Menu(func(m *ui.Menu) {
			for _, f := range docFilters {
				filter := f
				if m.Item(T(filter.label)).Checked(libFilter == filter.id).Chosen() {
					libFilter = filter.id
					libSelected = -1
				}
			}
		})
		ui.Spacer(c)
		if libSearching {
			field := ui.SearchField(c, &libSearchQuery).Label(T("Search documents")).Placeholder(T("Search")).Width(190)
			if !a.overlayOpen() {
				field.AutoFocus()
			}
		}
		a.toolbar(c, p, a.libraryActions())
		a.searchToggle(c, p, &libSearching, &libSearchQuery, T("Search documents"), !a.overlayOpen())
	})

	a.jobStrip(c, p)
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			switch {
			case len(a.docs) == 0:
				ui.Column(c).Fill().Children(func() {
					emptyMosaic(c, p, p.down, p.up)
					title, detail := T("No documents yet"), T("Import a folder of documents to build the knowledge base")
					if a.store == nil {
						title, detail = T("Knowledge base not connected"), T("PostgreSQL did not answer; the environment page has the details.")
					}
					emptyState(c, p, "library", title, detail)
				})
			case len(rows) == 0:
				emptyState(c, p, "", T("No matching documents"), T("Try another filter or search word"))
			default:
				a.docList(c, p, rows)
			}
		})
		if a.set.UI.Inspector {
			a.docInspector(c, p)
		}
	})

	// A row's delete is confirmed here. The alert's last button is the one it
	// focuses, so Cancel is what Enter reaches and no document goes by
	// accident; the alert has to have been open for the first button to count.
	confirming := a.deleteOpen
	if ui.AlertDialog(c, &a.deleteOpen, T("Delete this document?"), a.deleteDoc.Title,
		T("Delete"), T("Cancel")) == 0 && confirming {
		a.deleteDocument(a.deleteDoc)
	}

	dated := 0
	for _, d := range a.docs {
		if !d.Date.IsZero() {
			dated++
		}
	}
	a.statusBar(c, p, []statusItem{
		{T("All") + " " + itoa(len(a.docs)), ui.Color{}},
		{T("dated") + " " + itoa(dated), p.success},
		{T("date unknown") + " " + itoa(len(a.docs)-dated), p.tone("amber").bar},
		{T("chunk total") + " " + itoa(a.chunkCount()), ui.Color{}},
	}, func() { a.inspectorToggle(c, p) })
}

func (a *app) countDocs(status string) int {
	n := 0
	for _, d := range a.docs {
		if d.Status == status {
			n++
		}
	}
	return n
}

func (a *app) libraryActions() []pageAction {
	return []pageAction{
		{"plus", T("Import a folder"), false, nil, func() {
			a.pickFolder(a.startIngest)
		}},
		{"alert", T("Review a new document"), false, nil, func() {
			a.pickFile(a.startAnalyze)
		}},
		{"columns-3", T("Show columns"), false, func(_ *ui.Context, m *ui.Menu) {
			for _, col := range libColumns[1:] {
				hidden := slices.Contains(a.set.UI.HiddenColumns, col.id)
				if m.Item(T(col.title)).Checked(!hidden).Chosen() {
					if hidden {
						a.set.UI.HiddenColumns = slices.DeleteFunc(a.set.UI.HiddenColumns, func(id string) bool { return id == col.id })
					} else {
						a.set.UI.HiddenColumns = append(a.set.UI.HiddenColumns, col.id)
					}
					a.saveSettings()
				}
			}
		}, nil},
		{"ellipsis", T("More actions"), false, func(c *ui.Context, m *ui.Menu) {
			if m.Item(T("Import document")).Chosen() {
				a.flash = T("Importing is not available yet")
			}
			if m.Item(T("Show details panel")).Checked(a.set.UI.Inspector).Chosen() {
				a.set.UI.Inspector = !a.set.UI.Inspector
				a.saveSettings()
			}
			m.Separator()
			if m.Item(T("Copy the list of documents")).Chosen() {
				var b strings.Builder
				for _, d := range a.docs {
					b.WriteString(d.Title + "\t" + d.ProductLine + "\t" + docStatusLabel(d.Status) + "\n")
				}
				c.WriteClipboard(b.String())
				a.flash = T("Copied") + " " + itoa(len(a.docs)) + " " + T("documents")
			}
			m.Separator()
			// The import queue lands with the backend; the entries show
			// where they will sit, as Motrix's disabled bulk actions do.
			m.Item(T("Retry failed imports (backend pending)")).Disabled(true)
			m.Item(T("Clear finished imports (backend pending)")).Disabled(true)
		}, nil},
	}
}

// libFilters are the document statuses the table filters by.

// docList is the table of documents: a header of sortable columns over rows
// that zebra-stripe and take the pointer's wash.
func (a *app) docList(c *ui.Context, p palette, rows []doc) {
	cols := a.visibleColumns()

	head := ui.Row(c).Height(30).Padding(0, rowPadding).Clip().BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
		for _, col := range cols {
			cell := ui.ButtonBase(c.Key(col.id)).Height(30).PaddingX(8).Gap(4).AlignItems(ui.Center).
				FontSize(11.5).FontWeight(500).TextColor(p.muted)
			sizeCell(cell, col)
			cell.Children(func() {
				ui.Text(c, T(col.title)).SingleLine()
				if libSortBy == col.id {
					arrow := "arrow-up"
					if libSortDesc {
						arrow = "arrow-down"
					}
					ui.Icon(c, icon(arrow)).FontSize(11)
				}
			})
			if cell.Clicked() {
				switch {
				case libSortBy != col.id:
					libSortBy, libSortDesc = col.id, false
				case !libSortDesc:
					libSortDesc = true
				default:
					libSortBy, libSortDesc = "", false
				}
			}
		}
	})

	a.listState.Key = func(i int) any { return rows[i].ID }
	a.listState.Selected = &libSelected
	a.listState.Label = func(i int) string { return rows[i].Title }
	list := ui.List(c, &a.listState, len(rows)).Grow(1).RowWidth(rowWidth(cols)).Label(T("Library")).
		Rows(func(row ui.ListRow) { a.docRow(row, p, rows, cols) })
	head.ScrollWith(list)
	if list.Submitted() && libSelected >= 0 && libSelected < len(rows) {
		// A double click or Enter opens what the row holds: its details,
		// as the inspector shows them.
		a.currentDoc = rows[libSelected].ID
		a.set.UI.Inspector = true
		a.saveSettings()
		a.flash = T("Opened") + " " + rows[libSelected].Title
	}
	if !a.overlayOpen() && list.Shortcut(0, ui.KeyDelete) && libSelected >= 0 && libSelected < len(rows) {
		a.askDelete(rows[libSelected])
	}
}

// docRow is one row of the document table.
func (a *app) docRow(row ui.ListRow, p palette, rows []doc, cols []libColumn) {
	c := row.Context
	d := rows[row.Index]
	r := ui.Row(c).Height(36).Padding(0, rowPadding).FontSize(12.5).FontFeatures("tnum")
	text, muted := p.text, p.muted
	switch {
	case row.Selected():
		text, muted = p.accentText, p.accentText
	case r.Hovered():
		r.Background(p.active)
	case row.Index%2 == 1:
		r.Background(p.hover)
	}
	r.TextColor(text)
	r.ContextMenu(func(m *ui.Menu) {
		if m.Item(T("Open details")).Chosen() {
			a.set.UI.Inspector = true
			libSelected = row.Index
			a.saveSettings()
		}
		if m.Item(T("Analyze this document")).Chosen() {
			a.router.Push("/analyze")
		}
		if m.Item(T("Copy title")).Chosen() {
			c.WriteClipboard(d.Title)
			a.flash = T("Title copied")
		}
		m.Separator()
		if m.Item(T("Download the original document")).Chosen() {
			a.downloadDocument(d)
		}
		if m.Item(T("Delete this document…")).Chosen() {
			a.askDelete(d)
		}
	})
	r.Children(func() {
		for _, col := range cols {
			cell := ui.Row(c.Key(col.id)).PaddingX(8).AlignItems(ui.Center)
			sizeCell(cell, col)
			cell.Children(func() {
				switch col.id {
				case "name":
					ui.Text(c, d.Title).FontWeight(500).SingleLine().Grow(1).MinWidth(0).Tooltip(d.Title)
				case "date":
					dateCell(c, p, d)
				case "module":
					ui.Text(c, d.Module).SingleLine()
				case "format":
					ui.Text(c, d.Format).SingleLine()
				case "chunks":
					ui.Text(c, itoa(d.Chunks)).SingleLine()
				case "status":
					tn := docTone(p, d.Status)
					if row.Selected() {
						tn.bg, tn.text = p.hover, text
					}
					statusPill(c, p, tn, docStatusLabel(d.Status))
				case "imported":
					ui.Text(c, shortTime(d.Imported)).TextColor(muted).SingleLine()
				}
			})
		}
	})
}

// dateCell is a document's date, with how it was found: a reader has to be
// able to tell a date of unknown origin from one the document states.
func dateCell(c *ui.Context, p palette, d doc) {
	if d.Date.IsZero() {
		ui.Text(c, T("date unknown")).FontSize(12.5).TextColor(p.muted).SingleLine().
			Tooltip(T("The document states no date; its file time is shown in the details."))
		return
	}
	cell := ui.Text(c, d.Date.Format("2006-01-02")).FontSize(12.5).TextColor(p.text).
		FontFeatures("tnum").SingleLine().Tooltip(d.Date.Format("2006-01-02") + " · " + dateSourceLabel(d.DateSource))
	if d.DateConfidence < 0.6 {
		cell.TextColor(p.muted)
	}
}

// docTone is the tone a document's status reads in.
func docTone(p palette, status string) tone {
	switch status {
	case "active":
		return p.tone("green")
	case "superseded":
		return p.tone("slate")
	default:
		return p.tone("amber")
	}
}

func (a *app) visibleColumns() []libColumn {
	out := make([]libColumn, 0, len(libColumns))
	for _, col := range libColumns {
		if col.id == "name" || !slices.Contains(a.set.UI.HiddenColumns, col.id) {
			out = append(out, col)
		}
	}
	return out
}

// sizeCell gives a cell of a column its width and its alignment.
func sizeCell(cell ui.Element, col libColumn) {
	if col.width == 0 {
		cell.Grow(1).Basis(0).MinWidth(nameMinWidth)
	} else {
		cell.Width(col.width).Shrink(0)
	}
	cell.Justify(col.align)
}

// sortedAndFiltered is what the table shows: the documents ordered by their
// own date, with the search and the status filter applied.
func (a *app) sortedAndFiltered() []doc {
	all := a.sortedDocs()
	if libSortBy == "" || libSortBy == "date" {
		if libSortDesc {
			for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
				all[i], all[j] = all[j], all[i]
			}
		}
		return a.filterDocsList(all, libSearchQuery)
	}
	return a.filterDocsList(all, libSearchQuery)
}

func (a *app) filterDocsList(list []doc, query string) []doc {
	query = strings.ToLower(strings.TrimSpace(query))
	var out []doc
	for _, d := range list {
		if libFilter != "all" && d.Status != libFilter {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(d.Title), query) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func (a *app) filterDocs() []doc {
	query := strings.ToLower(strings.TrimSpace(libSearchQuery))
	var out []doc
	for _, d := range a.docs {
		if libFilter != "all" && d.Status != libFilter {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(d.Title), strings.ToLower(query)) {
			continue
		}
		out = append(out, d)
	}
	slices.SortStableFunc(out, func(x, y doc) int {
		var n int
		switch libSortBy {
		case "name":
			n = strings.Compare(strings.ToLower(x.Title), strings.ToLower(y.Title))
		case "format":
			n = strings.Compare(x.Format, y.Format)
		case "product":
			n = strings.Compare(x.ProductLine, y.ProductLine)
		case "date":
			switch {
			case x.Date.Equal(y.Date):
				n = 0
			case x.Date.Before(y.Date):
				n = -1
			default:
				n = 1
			}
		case "chunks":
			n = cmpInt(x.Chunks, y.Chunks)
		case "status":
			n = strings.Compare(x.Status, y.Status)
		case "imported":
			n = strings.Compare(x.Imported, y.Imported)
		}
		if libSortDesc {
			n = -n
		}
		return n
	})
	return out
}

func cmpInt(x, y int) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// loadChunks reads the chosen document's chunks, which the inspector lists
// and the source preview shows.
func (a *app) loadChunks() {
	if a.store == nil || a.currentDoc == 0 {
		a.currentChunks = nil
		return
	}
	if len(a.currentChunks) > 0 && a.currentChunks[0].DocID == a.currentDoc {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	chunks, err := a.store.DocumentChunks(ctx, a.currentDoc)
	if err != nil {
		return
	}
	a.currentChunks = chunks
}

// inspectorWidth is the room the details panel takes: narrow enough that
// the table keeps its columns at the window's least width.
const inspectorWidth = 260

// docInspector shows the details of the document chosen, beside the list.
func (a *app) docInspector(c *ui.Context, p palette) {
	ui.Scroll(c).Width(inspectorWidth).Shrink(0).Padding(4, 20, 20, 20).Gap(14).BorderWidth(0, 0, 0, 1).BorderColor(p.border).Children(func() {
		d := a.findDoc(a.currentDoc)
		if d == nil {
			ui.Text(c, T("Select a document to see its details")).FontSize(12.5).TextColor(p.muted).Padding(24, 0)
			return
		}
		ui.Text(c, d.Title).FontSize(15).FontWeight(600).MaxLines(3).Selectable()
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			statusPill(c, p, docTone(p, d.Status), docStatusLabel(d.Status))
			ui.Text(c, d.Format).FontSize(12.5).TextColor(p.muted)
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, T("Analyze this")).OnClick(func() { a.router.Push("/analyze") })
			ui.Button(c, T("Copy title")).OnClick(func() {
				c.WriteClipboard(d.Title)
				a.flash = T("Title copied")
			})
		})
		ui.Divider(c).Background(p.border)
		detail(c, p, T("Product line"), d.ProductLine)
		detail(c, p, T("Module"), d.Module)
		if d.Date.IsZero() {
			detail(c, p, T("Date"), T("date unknown")+" · "+dateSourceLabel(d.DateSource))
		} else {
			detail(c, p, T("Date"), d.Date.Format("2006-01-02")+" · "+dateSourceLabel(d.DateSource))
		}
		detail(c, p, T("Document type"), d.DocType)
		detail(c, p, T("Characters"), itoa(d.Chars))
		detail(c, p, T("Chunks"), itoa(d.Chunks))
		detail(c, p, T("Imported"), d.Imported)
		detail(c, p, T("Status"), docStatusLabel(d.Status))
		if d.SourceURI != "" {
			detail(c, p, T("File"), d.SourceURI)
		}
		ui.Divider(c).Background(p.border)
		sectionTitle(c, p, T("Chunks"))
		if len(a.currentChunks) == 0 {
			ui.Text(c, T("No chunks")).FontSize(12.5).TextColor(p.muted)
		} else {
			for _, chunk := range a.currentChunks {
				chunkRow(c, p, chunkLabel(chunk), chunk.Tokens)
			}
		}
		ui.Divider(c).Background(p.border)
		sectionTitle(c, p, T("Source preview"))
		preview := T("No source")
		if len(a.currentChunks) > 0 {
			preview = a.currentChunks[0].Content
		}
		ui.Text(c, preview).FontSize(12.5).LineHeight(1.5).Selectable().MaxLines(14)
	})
}

// chunkRow is one of a document's chunks: where it stands and how big it is.
func chunkRow(c *ui.Context, p palette, label string, tokens int) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).FontSize(12.5).Children(func() {
		ui.Text(c, label).SingleLine().Grow(1).Basis(0).MinWidth(0).Tooltip(label)
		ui.Text(c, itoa(tokens)+" tok").TextColor(p.muted).FontFeatures("tnum").SingleLine()
	})
}

// chunkLabel is where a chunk sits in its document.
func chunkLabel(chunk store.Chunk) string {
	if len(chunk.Path) == 0 {
		return itoa(chunk.Index+1) + ". " + T("body")
	}
	return itoa(chunk.Index+1) + ". " + strings.Join(chunk.Path, " > ")
}

func (a *app) findDoc(id int64) *doc {
	for i := range a.docs {
		if a.docs[i].ID == id {
			return &a.docs[i]
		}
	}
	return nil
}
