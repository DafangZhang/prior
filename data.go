package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"prior/agent"
	"prior/embed"
	"prior/ingest"
	"prior/llm"
	"prior/retrieval"
	"prior/store"
)

// data is what the window shows: the knowledge base when PostgreSQL
// answers, and the built-in sample when it does not, so that the interface
// works before the backend is set up.
type data struct {
	docs     []doc
	runs     []run
	findings []finding
	notes    []note
	ledger   []store.ChangeRecord
}

// env is the state of what the app needs: PostgreSQL, Ollama with the
// embedding model, and the language model where its own protocol says.
type env struct {
	checked   time.Time
	pgErr     string
	pgVector  bool
	pgDSN     string
	ollamaErr string
	ollamaURL string
	models    []string
	missing   []string
	dim       int
	llmWhere  string
	llmErr    string
	llmModels int
	docs      int
	chunks    int
}

// load reads the knowledge base into the window's own shapes. It leaves
// the sample in place when there is no store.
func (a *app) load() {
	if a.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	list, err := a.store.ListDocuments(ctx, 500)
	if err != nil {
		a.try(err)
		return
	}
	docs := make([]doc, 0, len(list))
	for _, d := range list {
		when := d.Date
		if d.DateSource == "missing" {
			// The pipeline fell back to the file's own time: the library
			// shows no date rather than a wrong one, and keeps the hint in
			// the details.
			when = time.Time{}
		}
		docs = append(docs, doc{
			ID: d.ID, Title: d.Title, Format: strings.ToUpper(d.Format), Module: d.Module,
			ProductLine: d.Module, Status: storeStatus(d.Status), Chars: d.Chars, Chunks: d.Chunks,
			Imported: d.ImportedAt.Format("2006-01-02 15:04"),
			Date:     when, DateSource: d.DateSource, DateConfidence: d.DateConf,
			DocType: d.DocType, SourceURI: d.SourceURI,
		})
	}
	a.docs = docs

	runs, err := a.store.Runs(ctx, 200)
	if err == nil {
		counts, _ := a.store.RunCounts(ctx)
		a.runs = make([]run, 0, len(runs))
		a.pendingAll = 0
		for _, r := range runs {
			when := r.Started
			if when.IsZero() {
				when = r.CreatedAt
			}
			row := run{
				ID: r.ID, Title: r.Title, Status: r.Status, Stage: r.Stage,
				StartedAt: when.Format("2006-01-02 15:04"), Elapsed: elapsedOf(r),
				DocID: r.NewDocID, Verdict: r.Verdict,
			}
			if c, ok := counts[r.ID]; ok {
				row.Total, row.Pending, row.DocsHit = c.Total, c.Open, c.DocsHit
				row.Counts = c.Types
				copy(row.Findings[:], c.Severity[:])
				a.pendingAll += c.Open
			}
			a.runs = append(a.runs, row)
		}
	}
	// The report dialog holds its own run's findings; the list page reads
	// its figures from the counts above. A page with no report open must not
	// keep showing the last document's findings.
	if a.store != nil && !a.reportOpen {
		a.findings, a.ledger = nil, nil
		a.hasPreReview = false
	}
	if a.reportOpen {
		if _, ok := a.runByID(a.reportRun); !ok {
			a.closeReport()
		} else {
			a.loadReport(a.reportRun)
		}
	}
	// The notifications are the runs that finished or failed: the analysis
	// tells the product what it found, and a failure is worth seeing.
	notes := make([]note, 0, len(a.runs))
	for i, r := range a.runs {
		if i == 20 {
			break
		}
		title := T("Analysis finished") + "：" + r.Title
		read := true
		switch r.Status {
		case "failed":
			title = T("Analysis failed") + "：" + r.Title
			read = false
		case "running", "degraded":
			read = false
		}
		notes = append(notes, note{
			ID: r.ID, Title: title, Time: r.StartedAt, Read: read, ReportID: itoa64(r.ID),
		})
	}
	a.notes = notes
}

// elapsedOf is how long a run took, as the history shows it.
func elapsedOf(r store.Run) string {
	if r.Started.IsZero() || r.Finished.IsZero() {
		return "-"
	}
	return r.Finished.Sub(r.Started).Round(time.Second).String()
}

// findingOf maps a stored finding onto what the report draws.
func findingOf(f store.Finding) finding {
	out := finding{
		ID: itoa64(f.ID), Type: f.Type, Subtype: f.Subtype, Severity: f.Severity,
		Confidence: f.Confidence, Title: f.Title, Detail: f.Detail,
		NewQuote: f.Span.Quote, Question: f.Question, Impact: f.Impact,
		Status: f.Status, Note: f.Note, Fix: f.Fix,
	}
	if len(f.Span.Path) > 0 {
		out.NewPath = strings.Join(f.Span.Path, " > ")
	}
	if len(f.Evidence) > 0 {
		first := f.Evidence[0]
		out.OldQuote, out.OldDocTitle = first.Quote, first.DocTitle
		out.OldPath = strings.Join(first.Path, " > ")
	}
	return out
}

// storeStatus maps the knowledge base's status words onto the library's.
func storeStatus(status string) string {
	switch status {
	case "active", "draft", "superseded":
		return status
	case "running":
		return "draft"
	}
	return "draft"
}

// connSettings is the connection in force: the draft while the Environment
// page edits one, else what is saved. The self-check and the connection
// both follow it, so that a fix can be tried before Save.
func (a *app) connSettings() settings {
	if a.draftFor == "env" {
		return a.draft
	}
	return a.set
}

// modelClient is the model services a set of settings spells: Ollama for
// the embeddings the knowledge base searches with, and the language model
// beside it, over the protocol, endpoint and key those settings name.
func modelClient(set settings) *embed.Client {
	em := embed.New(set.Connection.OllamaURL, set.Models.Embed, llmClient(set))
	em.Configure(set.Models.NumCtx, set.Models.TimeoutSec)
	return em
}

// llmClient is the language model a set of settings names: an Ollama model
// answers at the Ollama URL, a provider's at its own endpoint.
func llmClient(set settings) *llm.Client {
	protocol := set.Models.Protocol
	if protocol == "" {
		protocol = llm.ProtocolOllama
	}
	endpoint := set.Models.Endpoint
	if protocol == llm.ProtocolOllama {
		endpoint = set.Connection.OllamaURL
	}
	return llm.New(llm.Config{
		Protocol:  protocol,
		Endpoint:  endpoint,
		APIKey:    set.Models.APIKey,
		Model:     set.Models.LLM,
		MaxTokens: set.Models.MaxTokens,
	})
}

// llmWhere is where the language model answers, as the settings page's
// self-check line shows it.
func llmWhere(gen *llm.Client) string {
	if gen.Protocol() == llm.ProtocolOllama {
		return gen.Protocol() + " · " + gen.Model()
	}
	return gen.Protocol() + " · " + gen.Model() + " · " + gen.Endpoint()
}

// checkEnv probes PostgreSQL, Ollama and the language model, which the
// settings page and the status pill show.
func (a *app) checkEnv() env {
	set := a.connSettings()
	e := env{checked: time.Now(), pgDSN: set.Connection.PGDSN, ollamaURL: set.Connection.OllamaURL}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	// Ollama holds the embeddings, and the language model answers where its
	// own protocol says, so each is probed where it lives.
	em := modelClient(set)
	if models, err := em.Tags(ctx); err != nil {
		e.ollamaErr = err.Error()
	} else {
		e.models = models
		if !llm.HasModel(models, set.Models.Embed, llm.ProtocolOllama) {
			e.missing = append(e.missing, set.Models.Embed)
		}
		if dim, err := em.Dim(ctx); err == nil {
			e.dim = dim
		}
	}
	e.llmWhere = llmWhere(em.LLM)
	if models, err := em.LLM.Models(ctx); err != nil {
		e.llmErr = err.Error()
	} else if !llm.HasModel(models, set.Models.LLM, em.LLM.Protocol()) {
		e.llmErr = T("The endpoint does not list") + " " + set.Models.LLM
		if em.LLM.Protocol() == llm.ProtocolOllama {
			e.missing = append(e.missing, set.Models.LLM)
		}
	} else {
		e.llmModels = len(models)
	}
	// The open store answers for the DSN it was opened with, and nothing
	// else: a connection the settings now spell another way is probed here
	// on its own, since the page must show what is configured, not what is
	// still open.
	if a.store != nil && a.storeDSN == set.Connection.PGDSN {
		if docs, chunks, _, _, err := a.store.Stats(ctx); err == nil {
			e.docs, e.chunks = docs, chunks
		}
		e.pgVector = a.store.HasVector()
	} else {
		st, err := store.Open(ctx, set.Connection.PGDSN)
		if err != nil {
			e.pgErr = err.Error()
		} else {
			defer st.Close()
			e.pgVector = st.HasVector()
			if docs, chunks, _, _, err := st.Stats(ctx); err == nil {
				e.docs, e.chunks = docs, chunks
			}
		}
	}
	return e
}

// connect opens the knowledge base the settings spell, if it answers.
func (a *app) connect() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dsn := a.connSettings().Connection.PGDSN
	st, err := store.Open(ctx, dsn)
	if err != nil {
		a.env.pgErr = err.Error()
		return
	}
	a.store = st
	a.storeDSN = dsn
	a.env.pgErr = ""
	a.load()
}

// ingestJob is a running ingest: its own goroutine, which the window
// follows through a.update.
type ingestJob struct {
	root     string
	running  bool
	done     int
	total    int
	message  string
	lastLine string
	lines    []string
	failed   []string
	started  time.Time
	cancel   context.CancelFunc
}

// startIngest begins indexing a folder: parse, chunk, embed, store.
func (a *app) startIngest(root string) {
	if a.store == nil || a.ingest.running {
		a.flash = T("Knowledge base not connected")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	em := modelClient(a.set)
	opts := agent.IngestOptions{
		Root: root, Ledger: true, LedgerLimit: 8,
		ParserVersion: ingest.ParserVersion, EmbedModel: a.set.Models.Embed, LLMModel: a.set.Models.LLM,
		Temperature: a.set.Models.Temperature,
		Progress: func(ev agent.Event) {
			a.update(func() {
				a.ingest.done = ev.Index
				a.ingest.total = ev.Total
				a.ingest.message = ev.Stage + " " + ev.Message
				if ev.File != "" {
					line := ev.Stage + " " + ev.File
					if line != a.ingest.lastLine {
						a.ingest.lastLine = line
						a.ingest.lines = appendLine(a.ingest.lines, line)
					}
				}
			})
		},
	}
	a.ingest = ingestJob{root: root, running: true, started: time.Now(), cancel: cancel}
	a.flash = "开始入库：" + root
	go func() {
		res, err := agent.Ingest(ctx, a.store, em, opts)
		a.update(func() {
			a.ingest.running = false
			if err != nil {
				a.flash = err.Error()
			} else {
				a.flash = T("Ingested") + " " + itoa(res.Done) + " / " + itoa(res.Total)
			}
			for _, f := range res.Failures {
				a.ingest.failed = append(a.ingest.failed, f.Path+" — "+f.Reason)
			}
			a.load()
		})
	}()
}

// startAnalyze begins a review of one document against the knowledge base.
func (a *app) startAnalyze(path string) {
	if a.store == nil || a.analyzing {
		a.flash = T("Knowledge base not connected")
		return
	}
	doc, chunks, err := agent.ParseChunksForAnalysis(path)
	if err != nil {
		a.flash = err.Error()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	em := modelClient(a.set)
	a.analyzing = true
	a.analyzeDoc = doc.Meta.Title
	a.analyzeLines = nil
	a.analyzeCancel = cancel
	a.flash = T("Analyzing") + " " + doc.Meta.Title

	go func() {
		vectors, err := em.Embed(ctx, textsOf(doc, chunks))
		if err != nil {
			a.update(func() { a.analyzing = false; a.flash = err.Error() })
			return
		}
		stored := make([]store.Chunk, 0, len(chunks))
		for _, c := range chunks {
			stored = append(stored, store.Chunk{Index: c.Index, Path: c.HeadingPath, Content: c.Content, Tokens: c.Tokens})
		}
		docID, err := a.store.SaveDocument(ctx, store.Doc{
			// The run's models and temperature are the settings', so what
			// the page says a run does is what it does.
			Title: doc.Meta.Title, SourceURI: doc.Path, Format: doc.Format, DocType: doc.Meta.DocType,
			Module: doc.Meta.Module, Author: doc.Meta.Author, Version: doc.Meta.Version, Status: "draft",
			Checksum: doc.Checksum, Chars: doc.Chars, EmbedModel: a.set.Models.Embed,
			Date: doc.Meta.Date, DateSource: doc.Meta.DateSource, DateConf: doc.Meta.Confidence,
			VersionRank: ingest.VersionRank(doc.Meta.Version),
			Warnings:    doc.Warnings,
		}, stored, vectors)
		if err != nil {
			a.update(func() { a.analyzing = false; a.flash = err.Error() })
			return
		}
		profile := agent.ProfileDocument(doc, chunks)
		search := retrieval.Defaults()
		res, err := agent.Analyze(ctx, a.store, em, agent.AnalyzeOptions{
			Doc: store.Doc{ID: docID, Title: doc.Meta.Title, Module: doc.Meta.Module,
				Date: doc.Meta.Date, DateSource: doc.Meta.DateSource, Status: "draft"},
			Chunks: stored, Profile: profile, Search: search,
			EmbedModel: a.set.Models.Embed, LLMModel: a.set.Models.LLM,
			Temperature: a.set.Models.Temperature,
			Progress: func(ev agent.Event) {
				a.update(func() {
					a.analyzeStage = ev.Stage
					if ev.Message != "" {
						a.analyzeLines = appendLine(a.analyzeLines, ev.Stage+" "+ev.Message)
					}
				})
			},
		})
		a.update(func() {
			a.analyzing = false
			if err != nil {
				a.flash = err.Error()
				a.analyzeLines = appendLine(a.analyzeLines, T("Review failed")+"："+err.Error())
			} else {
				// What the run produced, and what it failed to produce: a
				// degraded run says so here rather than looking complete.
				summary := fmt.Sprintf("%s %s · %s %s · %s %s",
					T("findings"), itoa(len(res.Findings)),
					T("ledger entries"), itoa(res.Records),
					T("function points"), itoa(res.Points))
				if len(res.Degraded) > 0 {
					summary += " · " + T("degraded") + "：" + strings.Join(res.Degraded, "；")
				}
				a.flash = T("Review finished") + "：" + itoa(len(res.Findings))
				a.analyzeLines = appendLine(a.analyzeLines, summary)
			}
			a.load()
		})
	}()
}

// textsOf is what the embedder sees for a document's chunks.
func textsOf(doc *ingest.Doc, chunks []ingest.Chunk) []string {
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c.EmbeddingText(doc.Title))
	}
	return out
}

// appendLine adds a line to a log the window shows, keeping the last few.
func appendLine(lines []string, line string) []string {
	lines = append(lines, line)
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	return lines
}

// pickFolder asks for a folder and gives it to then, which runs on the
// window's own thread.
func (a *app) pickFolder(then func(string)) {
	a.pick(mygo.OpenDialogOptions{
		Parent: a.win, Title: T("Choose a folder"), Directory: true,
	}, then)
}

// pickFile asks for one document to review and gives it to then, which runs
// on the window's own thread.
func (a *app) pickFile(then func(string)) {
	a.pick(mygo.OpenDialogOptions{
		Parent: a.win, Title: T("Choose a document"),
		Filters: []mygo.FileFilter{
			{Name: "文档", Extensions: []string{"docx", "md", "markdown", "xlsx", "txt"}},
		},
	}, then)
}

// askDialog is how the system is asked for files or folders. It is a variable
// so that a test can answer without a window: what the picking has to get right
// is where it is called from, not what the panel does.
var askDialog = mygo.Dialog.Open

// pick opens a file dialog and calls then with what was chosen, if anything.
// The dialog answers only when the user has answered it, and an event listener
// has to return quickly — asked from the click itself, the click never returns
// and no dialog appears — so it is asked from a goroutine of its own, and the
// answer comes back on the window's thread. A dialog that cannot open says so
// instead of doing nothing.
func (a *app) pick(opts mygo.OpenDialogOptions, then func(string)) {
	go func() {
		paths, err := askDialog(opts)
		switch {
		case err != nil:
			a.update(func() { a.flash = T("The file dialog did not open") + "：" + err.Error() })
		case len(paths) == 0:
			// The user closed the dialog: nothing to do, and nothing to say.
		default:
			a.update(func() { then(paths[0]) })
		}
	}()
}

// sortedDocs is the library's order: by date, newest first, with the
// documents of unknown date after them and the recently answered first.
func (a *app) sortedDocs() []doc {
	out := make([]doc, len(a.docs))
	copy(out, a.docs)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date.IsZero() != out[j].Date.IsZero() {
			return !out[i].Date.IsZero()
		}
		if !out[i].Date.Equal(out[j].Date) {
			return out[i].Date.After(out[j].Date)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// itoa64 renders an id.
func itoa64(n int64) string { return itoa(int(n)) }

// askSaveDialog is how the system is asked where to write a file. It is a
// variable so that a test can answer without a window, as askDialog is.
var askSaveDialog = mygo.Dialog.Save

// downloadDocument copies a document's own file where the user asks for it:
// the knowledge base holds the text it read, and the file it read is the
// document itself, which is what a reader wants back.
func (a *app) downloadDocument(d doc) {
	source := d.SourceURI
	if source == "" {
		a.flash = T("This document has no file to download")
		return
	}
	// A save panel answers only when the user has answered it, so it is
	// asked from a goroutine of its own, as the open panel is.
	go func() {
		if _, err := os.Stat(source); err != nil {
			a.update(func() { a.flash = T("The original file is gone") + "：" + source })
			return
		}
		target, err := askSaveDialog(mygo.SaveDialogOptions{
			Parent: a.win, Title: T("Save the original document"),
			DefaultPath: filepath.Base(source),
		})
		switch {
		case err != nil:
			a.update(func() { a.flash = T("The file dialog did not open") + "：" + err.Error() })
			return
		case target == "":
			// The user closed the panel: nothing to write, nothing to say.
			return
		}
		if err := copyFile(source, target); err != nil {
			a.update(func() { a.flash = T("Could not write the file") + "：" + err.Error() })
			return
		}
		a.update(func() { a.flash = T("Saved to") + " " + target })
	}()
}

// copyFile writes a copy of a file, byte for byte: what is downloaded is the
// document that was reviewed, not a re-export of what was read from it.
func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	// A save panel has already asked about replacing an existing path.
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// askDelete opens the confirmation a row's delete has to pass: a document
// takes its chunks and its place in the library with it, and neither comes
// back from a menu item chosen by accident.
func (a *app) askDelete(d doc) {
	if a.store == nil {
		a.flash = T("Knowledge base not connected")
		return
	}
	a.deleteDoc, a.deleteOpen = d, true
}

// deleteDocument removes a document and its chunks from the knowledge base,
// and reloads what the window shows.
func (a *app) deleteDocument(d doc) {
	if a.store == nil {
		a.flash = T("Knowledge base not connected")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	gone, err := a.store.Delete(ctx, d.ID)
	if err != nil {
		a.try(err)
		return
	}
	// The inspector holds the chunks of the document it shows, and they are
	// not there any more.
	if a.currentDoc == d.ID {
		a.currentDoc, a.currentChunks = 0, nil
	}
	a.load()
	if !gone {
		a.flash = T("That document was already gone")
		return
	}
	a.flash = T("Deleted") + " " + d.Title
}
