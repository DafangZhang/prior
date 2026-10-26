package ingest

import (
	"strings"
	"unicode"
)

// Chunk is a piece of a document, small enough to embed and to quote: what
// the knowledge base stores and what a finding cites.
type Chunk struct {
	// Index is the chunk's place in its document, from zero.
	Index int
	// HeadingPath is the headings the chunk stands under, outermost first,
	// which is how a reader finds it again.
	HeadingPath []string
	Content     string
	Tokens      int
	// Part says which piece of a long section this is, 1 for the first.
	Part int
}

// Chunk budgets, as the pipeline specifies: a chunk targets 300 to 600
// tokens and never passes 800, so a section longer than that is split at
// its paragraphs.
const (
	chunkTargetTokens = 450
	chunkMaxTokens    = 800
	chunkMinTokens    = 60
)

// Chunking splits normalized blocks into chunks, keeping each block whole
// and each table with its heading: a table split across chunks reads as
// noise in the report's evidence.
func Chunking(blocks []Block) []Chunk {
	var (
		chunks  []Chunk
		path    []string
		content []string
		tokens  int
		part    int
		section []string
	)
	flush := func() {
		if len(content) == 0 || tokens < chunkMinTokens && len(chunks) > 0 {
			// A last scrap joins the chunk before it rather than standing
			// alone below the minimum.
			if len(content) > 0 && len(chunks) > 0 {
				last := &chunks[len(chunks)-1]
				last.Content = strings.TrimSpace(last.Content + "\n" + strings.Join(content, "\n"))
				last.Tokens = estimateTokens(last.Content)
			}
			content, tokens = nil, 0
			return
		}
		part++
		chunks = append(chunks, Chunk{
			Index:       len(chunks),
			HeadingPath: append([]string(nil), section...),
			Content:     strings.TrimSpace(strings.Join(content, "\n")),
			Tokens:      tokens,
			Part:        part,
		})
		content, tokens = nil, 0
	}
	for _, b := range blocks {
		switch b.Kind {
		case KindHeading:
			flush()
			part = 0
			level := max(b.Level, 1)
			if level <= len(path) {
				path = path[:level-1]
			}
			path = append(path, b.Text)
			section = append([]string(nil), path...)
			content = append(content, b.Text)
			tokens = estimateTokens(b.Text)
			continue
		case KindTable:
			text := b.Plain()
			if text == "" {
				continue
			}
			// A table joins the chunk it follows when it fits, so that a
			// field list stays with the section that explains it.
			if t := estimateTokens(text); tokens+t > chunkMaxTokens {
				flush()
			}
			content = append(content, text)
			tokens += estimateTokens(text)
			if tokens >= chunkTargetTokens {
				flush()
			}
			continue
		}
		text := b.Text
		t := estimateTokens(text)
		if tokens+t > chunkMaxTokens {
			flush()
		}
		content = append(content, text)
		tokens += t
		if tokens >= chunkTargetTokens {
			flush()
		}
	}
	flush()
	return chunks
}

// estimateTokens is a rough count for the budgets above: a Chinese
// character is near a token, a Latin word a token and a half. It is used
// only to size chunks, never to bill or to trim a prompt.
func estimateTokens(s string) int {
	var cjk, latin, other int
	inWord := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
			cjk++
			inWord = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !inWord {
				latin++
			}
			inWord = true
		default:
			inWord = false
			other++
		}
	}
	tokens := cjk + latin*3/2 + other/8
	if tokens == 0 && strings.TrimSpace(s) != "" {
		return 1
	}
	return tokens
}

// Tokens is how many tokens a text takes, for the pipeline's budgets.
func Tokens(s string) int { return estimateTokens(s) }

// EmbeddingText is what the pipeline embeds for a chunk: its heading path
// and its content, so that a section's heading counts in a search for it.
func (c Chunk) EmbeddingText(docTitle string) string {
	var b strings.Builder
	if docTitle != "" {
		b.WriteString(docTitle)
		b.WriteString("\n")
	}
	if len(c.HeadingPath) > 0 {
		b.WriteString(strings.Join(c.HeadingPath, " > "))
		b.WriteString("\n")
	}
	b.WriteString(c.Content)
	return b.String()
}
