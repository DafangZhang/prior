package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestMarkShowsItsFindingOnHover covers the mark's tooltip: hovering the
// highlighted words says the finding behind them — its severity and type on
// the first line, its title, and its detail — so a reader does not have to
// leave the document to know what is wrong.
func TestMarkShowsItsFindingOnHover(t *testing.T) {
	a := newTestApp(t)
	view := func(c *ui.Context) {
		ui.Column(c).Padding(20).Children(func() {
			a.markSpan(c, paletteFor(c.Theme(), findSidebarColor("blue")),
				"被标出的原文", finding{ID: "1", Type: "ambiguity", Severity: "S2", Title: "没有量化", Detail: "这句话没有数字"})
		})
	}
	tester := ui.NewTester(view, 400, 120)
	tester.Frame()
	if !tester.HasText("被标出的原文") {
		t.Fatalf("the mark's words are not drawn: %v", tester.Texts())
	}
	// The tooltip is the element's own description, which the tester reads
	// as the pointer settles over it.
	r, ok := tester.Find("被标出的原文")
	if !ok {
		t.Fatal("the mark has no area")
	}
	tester.Move(r.X+r.W/2, r.Y+r.H/2)
	// A tooltip shows once the pointer has rested: its delay is longer than
	// one frame.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tester.Frame()
		time.Sleep(120 * time.Millisecond)
		if tester.HasText("没有量化") {
			break
		}
	}
	texts := tester.Texts()
	found := false
	for _, text := range texts {
		if strings.Contains(text, "没有量化") || strings.Contains(text, "这句话没有数字") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the hover says nothing about the finding: %v", texts)
	}
}
