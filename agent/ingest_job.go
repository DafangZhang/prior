// Package agent runs the pipeline: the ingest that builds the knowledge
// base from a folder of documents, and the analysis that reviews a new
// document against it. It emits events as it goes and never imports ui.
package agent

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"prior/embed"
	"prior/ingest"
	"prior/store"
)

// Event is one step of a job, as the window and the CLI show it.
type Event struct {
	// Stage is where the job is: scan / parse / chunk / embed / store /
	// ledger / done.
	Stage   string
	File    string
	Index   int
	Total   int
	Message string
}

// Failure is a document the job could not ingest.
type Failure struct {
	Path   string
	Reason string
}

// IngestOptions is what a job runs with.
type IngestOptions struct {
	Root string
	// Manifest is the download manifest beside the documents, which fills
	// in module names and links the documents do not carry.
	Manifest string
	// Ledger asks the model for each document's function points and
	// baseline rules. It is the slow part, so it is asked for the newest
	// LedgerLimit documents only.
	Ledger      bool
	LedgerLimit int
	// Parser version and model names are recorded with the job, so a
	// change in either is visible in the data.
	ParserVersion string
	EmbedModel    string
	LLMModel      string
	// Temperature is what the job's model calls run at; zero takes the
	// pipeline's default.
	Temperature float64
	// Progress receives every event; it may be nil.
	Progress func(Event)
	// Force re-reads a document the knowledge base already holds: the same
	// file saved again replaces its chunks, which is what a change in the
	// parser needs — without it, a fix to how text is read reaches nothing
	// already ingested, since the file's checksum has not changed.
	Force bool
}

// IngestResult is what a job did.
type IngestResult struct {
	JobID    int64
	Total    int
	Done     int
	Skipped  int
	Ledger   int
	Chunks   int
	DocIDs   []int64
	Failures []Failure
}

// Ingest reads a folder of documents into the knowledge base: parse,
// chunk, embed, store, and, where asked, write each document's function
// points and baseline rules into the change ledger.
func Ingest(ctx context.Context, st *store.Store, em *embed.Client, opts IngestOptions) (IngestResult, error) {
	var res IngestResult
	if opts.Progress == nil {
		opts.Progress = func(Event) {}
	}
	paths, err := documents(opts.Root)
	if err != nil {
		return res, err
	}
	res.Total = len(paths)
	opts.Progress(Event{Stage: "scan", Total: len(paths), Message: "发现 " + itoa(len(paths)) + " 份文档"})

	manifest := loadManifest(opts.Manifest)
	jobID, err := st.StartJob(ctx, opts.Root, len(paths), opts.ParserVersion, opts.EmbedModel)
	if err != nil {
		return res, err
	}
	res.JobID = jobID

	// The newest documents are the ones the ledger is worth building for.
	ledgerFrom := -1
	if opts.Ledger {
		ledgerFrom = len(paths) - max(opts.LedgerLimit, 0)
		if opts.LedgerLimit <= 0 {
			ledgerFrom = 0
		}
	}

	for i, path := range paths {
		if err := ctx.Err(); err != nil {
			st.UpdateJob(ctx, jobID, res.Done, failures(res.Failures), "cancelled")
			return res, err
		}
		base := filepath.Base(path)
		opts.Progress(Event{Stage: "parse", File: base, Index: i + 1, Total: len(paths)})

		doc, err := ingest.Parse(path)
		if err != nil {
			res.Failures = append(res.Failures, Failure{Path: base, Reason: err.Error()})
			continue
		}
		if len(doc.Warnings) > 0 {
			res.Skipped++
			res.Failures = append(res.Failures, Failure{Path: base, Reason: strings.Join(doc.Warnings, "；")})
			continue
		}
		applyManifest(&doc.Meta, lookupManifest(manifest, base))
		if err := nameSystem(ctx, em, doc, opts); err != nil {
			// A document whose system could not be named is still archived:
			// the name is what the library groups by, not what it holds.
			res.Failures = append(res.Failures, Failure{Path: base, Reason: "模块判定失败：" + err.Error()})
		}
		if _, ok, err := st.DocumentByChecksum(ctx, doc.Checksum); err == nil && ok && !opts.Force {
			res.Skipped++
			continue
		}

		chunks := ingest.Chunking(doc.Blocks)
		if len(chunks) == 0 {
			res.Failures = append(res.Failures, Failure{Path: base, Reason: "切块为空"})
			continue
		}
		texts := make([]string, 0, len(chunks))
		for _, c := range chunks {
			texts = append(texts, c.EmbeddingText(doc.Title))
		}
		opts.Progress(Event{Stage: "embed", File: base, Index: i + 1, Total: len(paths),
			Message: itoa(len(chunks)) + " 块"})
		vectors, err := em.Embed(ctx, texts)
		if err != nil {
			res.Failures = append(res.Failures, Failure{Path: base, Reason: err.Error()})
			continue
		}
		stored := make([]store.Chunk, 0, len(chunks))
		for _, c := range chunks {
			stored = append(stored, store.Chunk{
				Index: c.Index, Path: c.HeadingPath, Content: c.Content, Tokens: c.Tokens,
			})
		}
		id, err := st.SaveDocument(ctx, storeDoc(doc, opts.EmbedModel), stored, vectors)
		if err != nil {
			res.Failures = append(res.Failures, Failure{Path: base, Reason: err.Error()})
			continue
		}
		res.DocIDs = append(res.DocIDs, id)
		res.Done++
		res.Chunks += len(chunks)

		if opts.Ledger && i >= ledgerFrom {
			n, err := writeBaseline(ctx, st, em, doc, chunks, id, opts)
			if err != nil {
				res.Failures = append(res.Failures, Failure{Path: base, Reason: "台账抽取失败：" + err.Error()})
			}
			res.Ledger += n
		}
		opts.Progress(Event{Stage: "store", File: base, Index: i + 1, Total: len(paths),
			Message: "已入库（累计 " + itoa(res.Done) + "）"})
	}
	status := "succeeded"
	if ctx.Err() != nil {
		status = "cancelled"
	}
	st.UpdateJob(ctx, jobID, res.Done, failures(res.Failures), status)
	opts.Progress(Event{Stage: "done", Total: len(paths), Message: "完成 " + itoa(res.Done) + "，跳过 " + itoa(res.Skipped) +
		"，失败 " + itoa(len(res.Failures))})
	return res, nil
}

// storeDoc turns a parsed document into its row.
func storeDoc(doc *ingest.Doc, embedModel string) store.Doc {
	return store.Doc{
		Title: doc.Meta.Title, SourceURI: doc.Path, Format: doc.Format,
		DocType: doc.Meta.DocType, Module: doc.Meta.Module, Author: doc.Meta.Author,
		Version: doc.Meta.Version, Status: "active", Checksum: doc.Checksum,
		Chars: doc.Chars, EmbedModel: embedModel,
		Date: doc.Meta.Date, DateSource: doc.Meta.DateSource, DateConf: doc.Meta.Confidence,
		VersionRank: ingest.VersionRank(doc.Meta.Version),
		Warnings:    doc.Warnings,
	}
}

// documents lists the documents of a folder, in name order, which is the
// order the team's own names put them in: oldest first.
func documents(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if ingest.Supported(path) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out, nil
}

// manifestEntry is one row of the download manifest.
type manifestEntry struct {
	module, title, link, status string
}

// loadManifest reads the download manifest, keyed by the file name it
// names, and by the names it nearly names: the manifest records some files
// by a shortened title, so a lookup falls back to a prefix match. A
// manifest that is not there is not an error: it only fills gaps.
func loadManifest(path string) map[string]manifestEntry {
	out := map[string]manifestEntry{}
	if path == "" {
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil || len(rows) < 2 {
		return out
	}
	header := rows[0]
	col := func(names ...string) int {
		for i, h := range header {
			h = strings.TrimPrefix(strings.TrimSpace(h), "\ufeff")
			for _, name := range names {
				if h == name {
					return i
				}
			}
		}
		return -1
	}
	iModule, iTitle := col("需求名称"), col("文档标题")
	iLink, iStatus, iFile := col("链接"), col("状态"), col("本地文件名")
	for _, row := range rows[1:] {
		get := func(i int) string {
			if i < 0 || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		name := get(iFile)
		if name == "" {
			continue
		}
		entry := manifestEntry{module: firstModule(get(iModule)), title: get(iTitle), link: get(iLink), status: get(iStatus)}
		out[name] = entry
		out[normalizeName(name)] = entry
	}
	return out
}

// firstModule is the product module a manifest row names: the requirement
// name's first part, before the slashes the team writes.
func firstModule(name string) string {
	for _, sep := range []string{"/", "／", "、"} {
		if i := strings.Index(name, sep); i > 0 {
			name = name[:i]
		}
	}
	return strings.TrimSpace(name)
}

// normalizeName strips what a download adds: the separator, the download
// suffix and the extension, leaving the title the manifest lists.
func normalizeName(name string) string {
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if i := strings.LastIndex(name, "_"); i > 0 && len(name)-i <= 12 && !strings.ContainsAny(name[i:], "，。 ") {
		name = name[:i]
	}
	return strings.TrimSpace(name)
}

// lookupManifest finds the manifest row for a file: its exact name first,
// then the title without the download's decoration, then the longest
// manifest name the file starts with.
func lookupManifest(manifest map[string]manifestEntry, base string) manifestEntry {
	if e, ok := manifest[base]; ok {
		return e
	}
	short := normalizeName(base)
	if e, ok := manifest[short]; ok {
		return e
	}
	best, bestLen := manifestEntry{}, 0
	for key, entry := range manifest {
		if len(key) < 12 || len(key) <= bestLen {
			continue
		}
		if strings.HasPrefix(short, key) || strings.HasPrefix(key, short) {
			best, bestLen = entry, len(key)
		}
	}
	return best
}

// applyManifest fills a document's metadata from the manifest row that
// names it, which knows more than the file does.
// temperature is what this job's model calls use.
func (o IngestOptions) temperature() float64 {
	if o.Temperature <= 0 {
		return DefaultTemperature
	}
	return o.Temperature
}

// nameSystem decides which system a document belongs to, which is what the
// library groups by and what the function point ledger keys its modules on.
// The document's own words come first: its title, then its headings, then
// the bracket the team's file names use. A document that names no system is
// put to the model with the systems already in the ledger as its
// vocabulary, and only when the job was allowed to spend model calls.
func nameSystem(ctx context.Context, em *embed.Client, doc *ingest.Doc, opts IngestOptions) error {
	if system, ok := ingest.SystemIn(doc.Meta.Title); ok {
		doc.Meta.Module = system
		return nil
	}
	headings := make([]string, 0, 32)
	for _, block := range doc.Blocks {
		if block.Kind == ingest.KindHeading && strings.TrimSpace(block.Text) != "" {
			headings = append(headings, strings.TrimSpace(block.Text))
		}
	}
	if system, ok := ingest.SystemIn(headings...); ok {
		doc.Meta.Module = system
		return nil
	}
	if doc.Meta.Module != "" && ingest.SystemKnown(doc.Meta.Module) {
		return nil
	}
	if !opts.Ledger {
		return nil
	}
	vocabulary := ingest.Systems()
	system, err := InferModule(ctx, em, DocumentBrief{
		Title: doc.Meta.Title, Date: doc.Meta.Date, Module: doc.Meta.Module, DocType: doc.Meta.DocType,
	}, headings, doc.Meta.Title+"\n"+sampleOf(doc.Blocks), vocabulary, opts.LLMModel, opts.temperature())
	if err != nil {
		return err
	}
	doc.Meta.Module = system
	return nil
}

// sampleOf is a document's opening text, for the model calls that need a
// look at the document rather than its structure.
func sampleOf(blocks []ingest.Block) string {
	var b strings.Builder
	for _, block := range blocks {
		if b.Len() > 2000 {
			break
		}
		b.WriteString(block.Plain())
		b.WriteString("\n")
	}
	return b.String()
}

func applyManifest(meta *ingest.Meta, entry manifestEntry) {
	if meta.Module == "" {
		if system, ok := ingest.SystemIn(meta.Title); ok {
			meta.Module = system
		}
	}
	if meta.Module == "" && entry.title != "" {
		if system, ok := ingest.SystemIn(entry.title); ok {
			meta.Module = system
		}
	}
	if entry.title != "" {
		meta.ManifestTitle = entry.title
	}
	meta.ManifestLink = entry.link
	if entry.status != "" {
		meta.Status = entry.status
	}
	if meta.Title == "" {
		meta.Title = entry.title
	}
}

func failures(list []Failure) []map[string]string {
	out := make([]map[string]string, 0, len(list))
	for _, f := range list {
		out = append(out, map[string]string{"path": f.Path, "reason": f.Reason})
	}
	return out
}

// writeBaseline asks the model for a document's function points and their
// rules — a part at a time, so that a long document's rules are all read — and
// writes them as the ledger's first entries.
func writeBaseline(ctx context.Context, st *store.Store, em *embed.Client, doc *ingest.Doc, chunks []ingest.Chunk, docID int64, opts IngestOptions) (int, error) {
	brief := DocumentBrief{
		Title: doc.Meta.Title, Date: doc.Meta.Date, Module: doc.Meta.Module, DocType: doc.Meta.DocType,
	}
	var (
		points   []FunctionPoint
		firstErr error
	)
	for _, part := range partsOf(chunks, ProfileDocument(doc, chunks).Headings) {
		got, _, err := ExtractFunctionPoints(ctx, em, brief, part, opts.LLMModel, opts.temperature())
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		points = append(points, got...)
	}
	if len(points) == 0 && firstErr != nil {
		return 0, firstErr
	}
	written := 0
	for _, p := range points {
		pointID, err := st.UpsertFunctionPoint(ctx, store.FunctionPoint{
			Module:   firstNonEmpty(p.Module, doc.Meta.Module, "未归类"),
			Function: firstNonEmpty(p.Function, "未命名功能"),
			Topic:    p.Topic,
			Label:    labelOfPoint(p, doc.Meta.Module),
		}, docID)
		if err != nil {
			return written, err
		}
		evidence := []store.Evidence{}
		if p.Quote != "" {
			evidence = append(evidence, store.Evidence{
				DocID: docID, DocTitle: doc.Meta.Title, Quote: p.Quote,
				Path: p.HeadingPath, Date: doc.Meta.Date.Format("2006-01-02"),
			})
		}
		if _, err := st.SaveChangeRecord(ctx, store.ChangeRecord{
			PointID: pointID, DocID: docID, Kind: "baseline",
			After: p.Rule, Impact: p.Impact, Evidence: evidence,
			Status: "extracted", Recorded: doc.Meta.Date,
		}); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func labelOfPoint(p FunctionPoint, module string) string {
	parts := []string{firstNonEmpty(p.Module, module, "未归类"), firstNonEmpty(p.Function, "未命名功能")}
	if p.Topic != "" {
		parts = append(parts, p.Topic)
	}
	return strings.Join(parts, " · ")
}

// itoa is a small helper, kept local so the package needs no fmt for it.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

var _ = sync.Mutex{}
