package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mygo.App.RequestSingleInstanceLock()

	userData, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	mygo.App.SetPath(mygo.PathUserData, filepath.Join(userData, "Prior"))

	mygo.App.WhenReady(func() {
		userData, err := mygo.App.Path(mygo.PathUserData)
		if err != nil {
			log.Fatal(err)
		}
		app := newApp(loadSettings(filepath.Join(userData, "settings.json")))
		app.settingsPath = filepath.Join(userData, "settings.json")
		app.applyLanguage()
		app.connect()
		app.env = app.checkEnv()
		if menu := appMenu(app); menu != nil {
			mygo.App.SetMenu(menu)
		}
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:                "先例",
			Width:                1024,
			Height:               680,
			MinWidth:             960,
			MinHeight:            620,
			StateKey:             "main-1",
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 20, Y: 19},
			TitleBarHeight:       40,
			AutoHideMenuBar:      true,
			Content:              ui.View(app.view),
		})
		app.win = win

		mygo.App.OnSecondInstance(func(args []string, workingDir string) {
			win.Focus()
		})

		mygo.GlobalShortcut.Register("cmd+k", func() {
			app.cmdOpen = !app.cmdOpen
			app.cmdQuery = ""
		})
		mygo.GlobalShortcut.Register("cmd+1", func() { app.router.Push("/library") })
		mygo.GlobalShortcut.Register("cmd+2", func() { app.router.Push("/analyze") })
		mygo.GlobalShortcut.Register("cmd+3", func() { app.router.Push("/reports") })
		mygo.GlobalShortcut.Register("cmd+4", func() { app.router.Push("/history") })
		mygo.GlobalShortcut.Register("cmd+6", func() { app.router.Push("/modules") })
		mygo.GlobalShortcut.Register("cmd+5", func() { app.router.Push("/notifications") })
		mygo.GlobalShortcut.Register("cmd+shift+b", func() {
			app.set.UI.SidebarHidden = !app.set.UI.SidebarHidden
			app.saveSettings()
		})
		mygo.GlobalShortcut.Register("cmd+i", func() {
			app.set.UI.Inspector = !app.set.UI.Inspector
			app.saveSettings()
		})
		mygo.GlobalShortcut.Register("cmd+shift+t", func() {
			if app.set.Appearance.Theme == "dark" {
				app.set.Appearance.Theme = "light"
				mygo.Theme.SetSource(mygo.ThemeLight)
			} else {
				app.set.Appearance.Theme = "dark"
				mygo.Theme.SetSource(mygo.ThemeDark)
			}
			app.saveSettings()
		})
		mygo.GlobalShortcut.Register("shift+?", func() { app.helpOpen = true })
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
