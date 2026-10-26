package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSmokeRender(t *testing.T) {
	a := newApp(defaultSettings())
	img := ui.Render(a.view, 1024, 680, 1.0)
	if img == nil {
		t.Fatal("Render returned nil")
	}
	t.Logf("rendered %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
}

func TestDarkPalette(t *testing.T) {
	p := paletteFor(&ui.Theme{Dark: true}, sidebarColors[0])
	if p.text != darkPalette.text {
		t.Fatalf("dark palette mismatch: %v", p.text)
	}
	if p.appBg != darkPalette.appBg {
		t.Fatal("dark palette window color mismatch")
	}
	if !p.dark {
		t.Fatal("expected the dark appearance")
	}
	if p.appBg == lightPalette.appBg {
		t.Fatal("expected light and dark window colors to differ")
	}
}
