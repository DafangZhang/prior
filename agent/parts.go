package agent

import (
	"fmt"
	"strings"

	"prior/ingest"
	"prior/store"
)

// PartChars is how much of a document one model call reads. The bound is in
// characters because these documents are Chinese, where a character is about
// a token; a document longer than this is read in several parts, so that
// neither the prompt nor the answer has to fit in one call. One call over a
// whole long document had its answer cut in the middle of its JSON, which
// lost the stage's findings entirely.
const PartChars = 8000

// Part is what a prompt shows of a document: the sections it has, and the
// body to read — for a long document, one part of it.
type Part struct {
	// Headings are the document's own section titles, which say where this
	// part sits in the whole.
	Headings []string
	// Chunks are the part's own chunks, in order: what a retry splits when an
	// answer came back cut.
	Chunks []store.Chunk
	// Text is this part's body, each chunk under its own heading.
	Text string
	// Index counts from one, and Total is how many parts the document has:
	// a stage message says which part it is on.
	Index, Total int
}

// parts splits a document's chunks into parts of at most PartChars each. A
// chunk is never split — every quote a finding makes is inside one part — and
// a document that fits stays in one.
func parts(chunks []store.Chunk, headings []string) []Part {
	groups := groupChunks(chunks, PartChars)
	out := make([]Part, 0, len(groups))
	for i, group := range groups {
		out = append(out, Part{
			Headings: headings, Chunks: group, Text: bodyOf(group),
			Index: i + 1, Total: len(groups),
		})
	}
	return out
}

// partsOf is parts for the ingest's own chunks, which are the parser's rather
// than the store's.
func partsOf(chunks []ingest.Chunk, headings []string) []Part {
	converted := make([]store.Chunk, 0, len(chunks))
	for _, c := range chunks {
		converted = append(converted, store.Chunk{Index: c.Index, Path: c.HeadingPath, Content: c.Content, Tokens: c.Tokens})
	}
	return parts(converted, headings)
}

// groupChunks cuts a document's chunks into groups of at most maxChars. The
// cut falls between chunks, never inside one.
func groupChunks(chunks []store.Chunk, maxChars int) [][]store.Chunk {
	var (
		groups  [][]store.Chunk
		current []store.Chunk
		size    int
	)
	for _, chunk := range chunks {
		n := len([]rune(chunk.Content)) + 8*len(chunk.Path)
		if len(current) > 0 && size+n > maxChars {
			groups = append(groups, current)
			current, size = nil, 0
		}
		current = append(current, chunk)
		size += n
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

// halve cuts a part in two at a chunk boundary, for an answer that was cut by
// its own length: half the text asks for half the findings.
func halve(part Part) (Part, Part) {
	mid := len(part.Chunks) / 2
	left, right := part, part
	left.Chunks, right.Chunks = part.Chunks[:mid], part.Chunks[mid:]
	left.Text, right.Text = bodyOf(left.Chunks), bodyOf(right.Chunks)
	return left, right
}

// bodyOf is a group of chunks as a prompt shows them: each under its own
// heading, so that a quote can be read where it sits.
func bodyOf(chunks []store.Chunk) string {
	var b strings.Builder
	for _, chunk := range chunks {
		if len(chunk.Path) > 0 {
			b.WriteString("### ")
			b.WriteString(strings.Join(chunk.Path, " > "))
			b.WriteString("\n")
		}
		b.WriteString(strings.TrimSpace(chunk.Content))
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

// askPart runs one stage over one part, and, when the answer came back cut by
// its own length, asks again over each half of the part: half the text asks
// for half the findings, which is what a cut answer needed. The cut answer's
// own entries are dropped — the halves cover the whole part, so keeping both
// would count the same finding twice.
func askPart[T any](opts AnalyzeOptions, stage string, part Part,
	prompt func(AnalyzeOptions, Part) string, ask func(text string) ([]T, bool, error)) ([]T, error) {
	items, truncated, err := ask(prompt(opts, part))
	// An answer cut before its first complete element comes back as an
	// error, and it is a cut answer all the same: the part goes again over
	// its halves rather than leaving the stage short a section.
	cut := truncated || (err != nil && len(items) == 0)
	if !cut {
		return items, err
	}
	if len(part.Chunks) < 2 {
		// A single chunk cannot be split: the stage keeps what arrived and
		// says the rest was lost.
		return items, err
	}
	opts.progress(Event{Stage: stage, Message: fmt.Sprintf("第 %d/%d 段的回答达到长度上限，拆成两半重问", part.Index, part.Total)})
	left, right := halve(part)
	var out []T
	for _, half := range []Part{left, right} {
		got, err := askPart(opts, stage, half, prompt, ask)
		out = append(out, got...)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// notePart says which part a stage is on, which only a long document has more
// than one of.
func notePart(opts AnalyzeOptions, stage string, part Part) {
	if part.Total > 1 {
		opts.progress(Event{Stage: stage, Message: fmt.Sprintf("第 %d/%d 段", part.Index, part.Total)})
	}
}

// progress reports a stage line, and does nothing when the caller kept none:
// the pipeline's stages are called from tests as well as the window.
func (o AnalyzeOptions) progress(ev Event) {
	if o.Progress != nil {
		o.Progress(ev)
	}
}
