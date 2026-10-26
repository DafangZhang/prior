package store

import "prior/ingest"

// Text read back from the knowledge base goes through the parser's cleaner.
// Rows written before the parser stripped the editor's <span> runs are still
// in the database; a reader, a model prompt or a copied quote must see the
// document's words rather than the markup around them. Cleaning is
// idempotent, so a row the parser already cleaned is unchanged.
func cleanText(s string) string { return ingest.CleanText(s) }

func cleanPath(path []string) []string {
	for i := range path {
		path[i] = cleanText(path[i])
	}
	return path
}
