package main

import (
	"runtime"

	"github.com/egoist/mygo"
)

// appMenu is the menu bar of macOS: the application's own menu with the
// View and Go items that do what the window's controls do, then the
// standard menus. Windows and Linux show none: the window hides its menu
// bar (AutoHideMenuBar).
func appMenu(a *app) *mygo.Menu {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "先例", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			mygo.Separator(),
			{Role: mygo.RoleServices},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Role: mygo.RoleFileMenu},
		{Role: mygo.RoleEditMenu},
		{Label: T("View"), Submenu: []*mygo.MenuItem{
			{Label: T("Show sidebar"), Accelerator: "CmdOrCtrl+Shift+B", Click: a.menuToggleSidebar},
			{Label: T("Show details panel"), Accelerator: "CmdOrCtrl+I", Click: a.menuToggleInspector},
			mygo.Separator(),
			{Label: T("Light appearance"), Type: mygo.MenuItemRadio, Checked: a.set.Appearance.Theme == "light", Click: a.menuTheme("light")},
			{Label: T("Dark appearance"), Type: mygo.MenuItemRadio, Checked: a.set.Appearance.Theme == "dark", Click: a.menuTheme("dark")},
			{Label: T("Follow system"), Type: mygo.MenuItemRadio, Checked: a.set.Appearance.Theme == "system", Click: a.menuTheme("system")},
			mygo.Separator(),
			{Label: T("Command palette"), Accelerator: "CmdOrCtrl+K", Click: func(*mygo.MenuItem, *mygo.Window) {
				a.update(func() {
					a.cmdOpen = true
					a.cmdQuery = ""
				})
			}},
			{Label: T("Shortcut help"), Accelerator: "Shift+?", Click: func(*mygo.MenuItem, *mygo.Window) {
				a.update(func() { a.helpOpen = true })
			}},
		}},
		{Label: T("Go"), Submenu: []*mygo.MenuItem{
			{Label: T("Library"), Accelerator: "CmdOrCtrl+1", Click: a.menuGo("/library")},
			{Label: T("Analyze"), Accelerator: "CmdOrCtrl+2", Click: a.menuGo("/analyze")},
			{Label: T("Report"), Accelerator: "CmdOrCtrl+3", Click: a.menuGo("/reports")},
			{Label: T("History"), Accelerator: "CmdOrCtrl+4", Click: a.menuGo("/history")},
			{Label: T("Notifications"), Accelerator: "CmdOrCtrl+5", Click: a.menuGo("/notifications")},
			mygo.Separator(),
			{Label: T("Settings"), Accelerator: "CmdOrCtrl+,", Click: a.menuGo("/settings")},
		}},
		{Role: mygo.RoleViewMenu},
		{Role: mygo.RoleWindowMenu},
	})
}

func (a *app) menuToggleSidebar(*mygo.MenuItem, *mygo.Window) {
	a.update(func() {
		a.set.UI.SidebarHidden = !a.set.UI.SidebarHidden
		a.saveSettings()
	})
}

func (a *app) menuToggleInspector(*mygo.MenuItem, *mygo.Window) {
	a.update(func() {
		a.set.UI.Inspector = !a.set.UI.Inspector
		a.saveSettings()
	})
}

func (a *app) menuTheme(theme string) func(*mygo.MenuItem, *mygo.Window) {
	return func(*mygo.MenuItem, *mygo.Window) {
		a.update(func() {
			a.set.Appearance.Theme = theme
			a.applyTheme(theme)
			a.saveSettings()
		})
	}
}

func (a *app) menuGo(path string) func(*mygo.MenuItem, *mygo.Window) {
	return func(*mygo.MenuItem, *mygo.Window) {
		a.update(func() { a.router.Push(path) })
	}
}
