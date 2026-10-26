package main

import (
	"github.com/egoist/mygo/ui"

	"prior/store"
)

// The verdict banner is what a review meeting reads first: the admission
// level, the risks behind it, and the questions to send back to the product
// manager — the three parts of a technical pre-review.

// verdictTone is the colour a verdict reads in: ready is green, clarify
// amber, and a document sent back is red.
func verdictTone(p palette, verdict string) tone {
	switch verdict {
	case "ready":
		return p.tone("green")
	case "clarify":
		return p.tone("amber")
	case "reject":
		return p.tone("red")
	}
	return p.tone("slate")
}

// verdictLabel is a verdict as the page names it.
func verdictLabelOf(verdict string) string {
	switch verdict {
	case "ready":
		return T("Ready for dev")
	case "clarify":
		return T("Needs clarification")
	case "reject":
		return T("Back to the product manager")
	case "unknown":
		return T("Not graded")
	}
	return verdict
}

// verdictBanner draws the run's conclusion: the level, the sentence behind
// it, the biggest risks, and the question list.
func (a *app) verdictBanner(c *ui.Context, p palette) {
	review := a.preReview
	tn := verdictTone(p, review.Verdict)
	ui.Column(c).Grow(1).Padding(16).Radius(14).Gap(10).
		Background(p.tile).BorderWidth(0, 0, 0, 3).BorderColor(tn.bar).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			caption(c, p, T("Pre-review"))
			statusText(c, p, tn.text, verdictLabelOf(review.Verdict))
			ui.Spacer(c)
			if len(review.Asked) > 0 {
				if ui.Button(c, T("Copy the question list")).Clicked() {
					c.WriteClipboard(questionList(review.Asked))
					a.flash = T("Question list copied")
				}
			}
		})
		if review.Summary != "" {
			ui.Text(c, review.Summary).FontSize(13).LineHeight(1.5).Selectable()
		}
		if len(review.Risks) > 0 {
			ui.Column(c).Gap(4).Children(func() {
				caption(c, p, T("Biggest engineering risks"))
				for _, risk := range review.Risks {
					ui.Text(c, "· "+risk).FontSize(12.5).TextColor(p.muted).LineHeight(1.45).Selectable()
				}
			})
		}
		// The question list is the longest part, and a review meeting reads
		// the risks first: it opens on a click, and copies whole.
		if len(review.Asked) > 0 {
			ui.Column(c).Gap(6).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					caption(c, p, T("Questions for the product manager")+" · "+itoa(len(review.Asked)))
					b := ui.ButtonBase(c.Key("questions")).Padding(2, 6).Radius(6).Children(func() {
						ui.Text(c, T(verdictQuestionsLabel())).FontSize(12).TextColor(p.accent)
					})
					if b.Clicked() {
						verdictQuestionsOpen = !verdictQuestionsOpen
					}
				})
				if verdictQuestionsOpen {
					ui.Column(c).Gap(3).Children(func() {
						for i, q := range review.Asked {
							ui.Text(c, itoa(i+1)+". "+q).FontSize(12.5).LineHeight(1.45).Selectable()
						}
					})
				}
			})
		}
	})
}

// verdictQuestionsOpen says the question list is showing.
var verdictQuestionsOpen bool

// verdictQuestionsLabel is the toggle's own words.
func verdictQuestionsLabel() string {
	if verdictQuestionsOpen {
		return "Hide"
	}
	return "Show all"
}

// questionList is the questions as the page copies them: numbered, one a
// line, ready to paste into a chat or a document.
func questionList(asked []string) string {
	out := ""
	for i, q := range asked {
		out += itoa(i+1) + ". " + q + "\n"
	}
	return out
}

// storePreReview is the shape the window keeps, which is the store's own.
var _ store.PreReview
