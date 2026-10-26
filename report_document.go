package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"prior/store"
)

// The document view is the report's own page: the document the run reviewed,
// as it reads, with every place a finding points at marked in the text. A
// reader sees a problem where it is rather than in a list beside the
// document, and hovering a mark says what the problem is.

// docReview is the document a report shows, with its chunks and the findings
// that point into it.
type docReview struct {
	Doc      store.Doc
	Chunks   []store.Chunk
	Findings []finding
	// Text is the document as one string, and Spans where each chunk sits in
	// it: a quote is found against the whole document, since a finding's
	// quote may run past the chunk it starts in.
	Text  string
	Spans []chunkSpan
	// Loaded says the chunks are this document's; a page that reads before
	// the load has finished shows the run's findings without the text.
	Loaded bool
}

// chunkSpan is where one chunk sits in the document's text.
type chunkSpan struct {
	Chunk      store.Chunk
	Start, End int
}

// documentView draws the reviewed document with its problems highlighted.
func (a *app) documentView(c *ui.Context, p palette, findings []finding) {
	review := a.reportDocument(findings)
	if a.store == nil {
		emptyState(c, p, "library", T("Knowledge base not connected"),
			T("Connect PostgreSQL to read the document the run reviewed."))
		return
	}
	if !review.Loaded {
		emptyState(c, p, "library", T("The document is not in the knowledge base"),
			T("It was reviewed but has since been deleted, so only the findings remain."))
		return
	}
	marks := a.reviewMarksOf(review, findings)
	ui.Column(c).MaxWidth(860).Gap(14).Padding(4, 0, 8, 0).Children(func() {
		a.documentHead(c, p, review, marks)
		for _, span := range review.Spans {
			a.chunkParagraph(c, p, review.Text, span, marks)
		}
	})
}

// reviewMarksOf finds the quotes in the document once per document and
// finding set: the page is rebuilt every frame, and searching a long
// document for every quote on every frame is work a reader waits for.
func (a *app) reviewMarksOf(review docReview, findings []finding) []mark {
	key := itoa64(review.Doc.ID) + ":" + itoa(len(review.Text)) + ":" + itoa(len(findings))
	for _, f := range findings {
		key += ":" + f.ID
	}
	if key == a.reviewMarkKey {
		return a.reviewMarks
	}
	a.reviewMarks, a.reviewMarkKey = marksIn(review.Text, findings), key
	return a.reviewMarks
}

// reportDocument is the document a report shows: the one the run reviewed.
// Its chunks are read once per run, so that a page redrawn every frame does
// not ask the database every frame.
func (a *app) reportDocument(findings []finding) docReview {
	out := docReview{Findings: findings}
	r, ok := a.openReportRow()
	if !ok || r.DocID == 0 || a.store == nil {
		return out
	}
	if d := a.findDoc(r.DocID); d != nil {
		out.Doc = store.Doc{ID: d.ID, Title: d.Title, SourceURI: d.SourceURI, Format: d.Format,
			Module: d.Module, Date: d.Date, DateSource: d.DateSource, Chars: d.Chars, Chunks: d.Chunks}
	}
	if a.reviewDocID != r.DocID {
		a.reviewDocID, a.reviewChunks = r.DocID, nil
		a.reviewText, a.reviewSpans = "", nil
	}
	if a.reviewChunks == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		chunks, err := a.store.DocumentChunks(ctx, r.DocID)
		if err != nil {
			return out
		}
		a.reviewChunks = chunks
		a.reviewText, a.reviewSpans = layOut(chunks)
	}
	if a.reviewSpans == nil {
		a.reviewText, a.reviewSpans = layOut(a.reviewChunks)
	}
	out.Chunks, out.Loaded = a.reviewChunks, len(a.reviewChunks) > 0
	out.Text, out.Spans = a.reviewText, a.reviewSpans
	return out
}

// layOut joins a document's chunks into one text, keeping where each sits:
// the chunks are separated by a blank line, which is how they read.
func layOut(chunks []store.Chunk) (string, []chunkSpan) {
	var b strings.Builder
	spans := make([]chunkSpan, 0, len(chunks))
	for i, chunk := range chunks {
		if i > 0 {
			b.WriteString("\n\n")
		}
		start := b.Len()
		// The store cleans rows written before the parser did; this is the
		// document's own words either way.
		b.WriteString(displayText(chunk.Content))
		spans = append(spans, chunkSpan{Chunk: chunk, Start: start, End: b.Len()})
	}
	return b.String(), spans
}

// documentHead is the document's title and what the review found in it: how
// many of its findings sit in the text, and the date it states.
func (a *app) documentHead(c *ui.Context, p palette, review docReview, marks []mark) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, review.Doc.Title).FontSize(20).FontWeight(600).Selectable()
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			if review.Doc.Module != "" {
				ui.Text(c, review.Doc.Module).FontSize(12.5).TextColor(p.muted).SingleLine()
			}
			if !review.Doc.Date.IsZero() {
				ui.Text(c, review.Doc.Date.Format("2006-01-02")).FontSize(12.5).TextColor(p.muted).FontFeatures("tnum")
			}
			ui.Text(c, T("Problems marked in the text")+"："+itoa(len(marks))+"/"+itoa(len(review.Findings))).
				FontSize(12.5).TextColor(p.muted)
		})
	})
	ui.Divider(c).Background(p.border).Margin(4, 0)
}

// chunkParagraph draws one chunk: its heading path, then its text with the
// findings' quotes marked. A mark that starts in an earlier chunk is drawn
// from this chunk's start, so a quote that runs past a chunk boundary still
// shows where it lands.
func (a *app) chunkParagraph(c *ui.Context, p palette, text string, span chunkSpan, marks []mark) {
	local := clip(marks, span)
	chunkText := text[span.Start:span.End]
	// The chunk's body begins with the section heading it was stored under:
	// drawing that heading as the path too would show the same title twice.
	// The headings above it are still context, so only the repeated one goes.
	path := span.Chunk.Path
	if len(path) > 0 {
		last := strings.TrimSpace(path[len(path)-1])
		if last != "" && strings.HasPrefix(strings.TrimSpace(chunkText), last) {
			path = path[:len(path)-1]
		}
	}
	ui.Column(c).Gap(4).Key(itoa(span.Chunk.Index)).Children(func() {
		if len(path) > 0 {
			ui.Text(c, strings.Join(path, " › ")).FontSize(12).FontWeight(600).
				TextColor(p.muted).Padding(10, 0, 0, 0).SingleLine()
		}
		paragraph := ui.RichText(c).FontSize(13.5).LineHeight(1.7).Selectable()
		paragraph.Children(func() {
			at := span.Start
			for _, m := range local {
				if m.start > at {
					ui.Text(c, text[at:m.start])
				}
				end := min(m.end, len(text))
				a.markSpan(c, p, text[m.start:end], m.finding)
				at = end
			}
			if at < span.End {
				ui.Text(c, text[at:span.End])
			}
		})
	})
}

// mark is one finding's quote as it sits in the document: where it starts and
// ends, and the finding it is.
type mark struct {
	start, end int
	finding    finding
}

// clip keeps the part of each mark that shows in a chunk. A quote that runs
// past the chunk it starts in is drawn in each chunk's own words, so the
// reader sees one highlighted sentence across the boundary rather than the
// same tail rendered twice inside the first paragraph.
func clip(marks []mark, span chunkSpan) []mark {
	var out []mark
	for _, m := range marks {
		if m.end <= span.Start || m.start >= span.End {
			continue
		}
		out = append(out, mark{
			start:   max(m.start, span.Start),
			end:     min(m.end, span.End),
			finding: m.finding,
		})
	}
	return out
}

// marksIn finds the quotes of the document's findings in its text, in order
// and without overlaps: two findings on the same words mark them once, and
// one that would cross an earlier mark yields to it.
func marksIn(text string, findings []finding) []mark {
	var marks []mark
	for _, f := range findings {
		quote := strings.TrimSpace(f.NewQuote)
		if quote == "" {
			continue
		}
		start, end, ok := findQuote(text, quote)
		if !ok {
			continue
		}
		marks = append(marks, mark{start: start, end: end, finding: f})
	}
	for i := 1; i < len(marks); i++ {
		for j := i; j > 0 && marks[j].start < marks[j-1].start; j-- {
			marks[j], marks[j-1] = marks[j-1], marks[j]
		}
	}
	out := marks[:0]
	last := 0
	for _, m := range marks {
		if m.start < last {
			continue
		}
		out = append(out, m)
		last = m.end
	}
	return out
}

// findQuote finds a quote in the text: exactly where it is there, and by its
// words where the whitespace differs — the model quotes the document's words,
// and a newline or a double space between them is not a different quote.
func findQuote(text, quote string) (int, int, bool) {
	if at := strings.Index(text, quote); at >= 0 {
		return at, at + len(quote), true
	}
	fields := strings.Fields(quote)
	if len(fields) == 0 {
		return 0, 0, false
	}
	for at := 0; at < len(text); {
		i := strings.Index(text[at:], fields[0])
		if i < 0 {
			return 0, 0, false
		}
		start := at + i
		if end, ok := matchFields(text, start, fields); ok {
			return start, end, true
		}
		at = start + len(fields[0])
	}
	return 0, 0, false
}

// matchFields says whether text at start holds the fields in order, separated
// by any run of spaces, and where the last one ends.
func matchFields(text string, start int, fields []string) (int, bool) {
	at := start
	for i, field := range fields {
		if i > 0 {
			for at < len(text) && (text[at] == ' ' || text[at] == '\n' || text[at] == '\t' || text[at] == '\r') {
				at++
			}
		}
		if !strings.HasPrefix(text[at:], field) {
			return 0, false
		}
		at += len(field)
	}
	return at, true
}

// markSpan is a finding's quote in the text: marked in its severity's colour,
// with what the finding is on hover, and choosing it opens the finding.
func (a *app) markSpan(c *ui.Context, p palette, text string, f finding) {
	tn := p.severityTone(f.Severity)
	mark := ui.Text(c, text).Background(tn.bg).TextColor(tn.text).
		DecorationColor(tn.bar).DecorationThickness(2).
		Tooltip(f.Severity + " · " + findingTypeLabel(f) + "\n" + f.Title + "\n\n" + truncateText(f.Detail, 220))
	if mark.Clicked() {
		// The sentence stays in the document; its conclusion opens beside
		// it. Switching views on a click was what made the report feel like
		// it moved under the reader.
		a.selectFinding(f)
	}
}

// findingIndex is where a finding sits among the report's own list.
func (a *app) findingIndex(id string) int {
	for i, f := range a.filteredFindings() {
		if f.ID == id {
			return i
		}
	}
	return -1
}

// findingTabOf is the tab a finding's type belongs to.
func findingTabOf(typ string) int {
	for i, tab := range findingTabs {
		if tab.id == typ {
			return i
		}
	}
	return 0
}

// filteredFindings is what the findings view shows, unfiltered by the current
// tab: the marks in the document follow the finding, not the view.
func (a *app) filteredFindings() []finding {
	return a.filterFindings(reportTab, reportFilter, reportSearch)
}
