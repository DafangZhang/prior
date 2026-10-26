package main

import (
	"strings"

	"prior/ingest"
)

// displayText is a text as the interface shows it: the parser's cleaner runs
// once more on text read from the knowledge base, so a row written before the
// parser stripped the editor's markup still reads as its author wrote it.
func displayText(s string) string {
	return strings.TrimSpace(ingest.CleanText(s))
}
