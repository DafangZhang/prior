package main

// The labels below turn the data's own values into what the interface
// shows, in its language. The values themselves, as stored and compared,
// stay as they are: only their labels pass through T.

// docStatusLabel is a document's status as it reads.
func docStatusLabel(status string) string {
	switch status {
	case "active":
		return T("Active")
	case "superseded":
		return T("Superseded")
	case "draft":
		return T("Draft")
	}
	return status
}

// runStatusLabel is a run's status as it reads.
func runStatusLabel(status string) string {
	switch status {
	case "succeeded", "completed":
		return T("Completed")
	case "degraded":
		return T("Degraded")
	case "failed":
		return T("Failed")
	case "canceled":
		return T("Canceled")
	case "running":
		return T("Running")
	}
	return status
}

// findingTypeLabel is a finding's type as it reads.
func findingTypeLabel(f finding) string {
	switch f.Type {
	case "change_signal":
		return T("Change signal")
	case "logic_gap":
		return T("Logic gap")
	case "ambiguity":
		return T("Ambiguity")
	case "one_liner":
		return T("One-line request")
	case "pre_review":
		return T("Engineering gap")
	}
	return f.Type
}

// findingStatusLabel is a finding's own status as it reads.
func findingStatusLabel(status string) string {
	switch status {
	case "confirmed":
		return T("Confirm")
	case "dismissed":
		return T("False positive")
	case "pending":
		return T("Needs more")
	case "parked":
		return T("Parked")
	}
	return status
}

// findingReviewLabel is a finding's stored review state as the card shows it;
// an open finding shows nothing, because it has not been decided yet.
func findingReviewLabel(status string) string {
	switch status {
	case "confirmed":
		return T("Confirmed")
	case "dismissed":
		return T("False positive")
	case "pending":
		return T("Needs more")
	case "parked":
		return T("Parked")
	}
	return ""
}

// findingTabs are the types of finding, in the order of the report's menu,
// with the ids the data carries.
var findingTabs = []struct{ id, label string }{
	{"change_signal", "Change signals"},
	{"logic_gap", "Logic gaps"},
	{"ambiguity", "Ambiguities"},
	{"one_liner", "One-line requests"},
	{"pre_review", "Engineering gaps"},
}

// severityFilters are the severities the report filters by.
var severityFilters = []struct{ id, label string }{
	{"all", "All severities"},
	{"S0", "S0 · Blocker"},
	{"S1", "S1 · High"},
	{"S2", "S2 · Medium"},
	{"S3", "S3 · Low"},
}

// reportSorts are the orders the findings read in.
var reportSorts = []struct{ id, label string }{
	{"severity", "By severity"},
	{"confidence", "By confidence"},
	{"type", "By type"},
}

// severities is the severity distribution of the run's findings, worst
// first, which the bar and its legend draw.
func severities(findings []finding) []struct {
	sev   string
	count int
} {
	out := []struct {
		sev   string
		count int
	}{{"S0", 0}, {"S1", 0}, {"S2", 0}, {"S3", 0}}
	for _, f := range findings {
		for i := range out {
			if out[i].sev == f.Severity {
				out[i].count++
			}
		}
	}
	return out
}

// docFilters are the document statuses the library filters by.
var docFilters = []struct{ id, label string }{
	{"all", "All"},
	{"active", "Active"},
	{"superseded", "Superseded"},
	{"draft", "Draft"},
}

// analyzeSteps are the pipeline's stages, in order.
var analyzeSteps = []string{
	"Parsing documents", "Building the index", "Retrieving changes",
	"Detecting S0", "Detecting S1", "Detecting S2", "Summarizing",
}

// productLines are the lines a run may be scoped to: the id a run stores,
// and the label it shows.
var productLines = []struct{ id, label string }{
	{"trading", "Trading"},
	{"payment", "Payment"},
	{"marketing", "Marketing"},
	{"membership", "Membership"},
	{"platform", "Platform"},
	{"risk", "Risk"},
}

// productLineOf is a stored product line's label, or the label itself
// while it is one.
func productLineOf(id string) string {
	for _, pl := range productLines {
		if pl.id == id {
			return T(pl.label)
		}
	}
	return T(id)
}

// themes are the appearance choices, by id and label.
var themes = []struct{ id, label string }{
	{"system", "System"},
	{"light", "Light"},
	{"dark", "Dark"},
}

// languages are the interface languages, by id and label.
var languages = []struct{ id, label string }{
	{"system", "System"},
	{"zh", "Simplified Chinese"},
	{"en", "English"},
}

// sidebarColorLabel is a sidebar color's label.
func sidebarColorLabel(name string) string {
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
	}
	return T("Gray")
}

// labelOf finds a label by id in a table of ids and labels, or the id
// itself while it is unknown.
func labelOf(list []struct{ id, label string }, id string) string {
	for _, it := range list {
		if it.id == id {
			return T(it.label)
		}
	}
	return T(id)
}

// indexOfID finds the index of an id in a table of ids and labels, or 0.
func indexOfID(list []struct{ id, label string }, id string) int {
	for i, it := range list {
		if it.id == id {
			return i
		}
	}
	return 0
}
