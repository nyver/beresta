package main

import (
	"embed"
	"log"
	"os"
	"slices"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/beresta-app/beresta/desktop/platform/traymenu"
)

//go:embed all:frontend/dist
var assets embed.FS

// autostartFlag is the argument applyAutostartReal writes into the
// Windows Run-key command line (desktop/autostart_windows.go); its
// presence starts the window hidden to the tray instead of popping up on
// every sign-in.
const autostartFlag = "--autostart"

func main() {
	app := newApp()

	// The tray icon, context menu, and global hotkey are started here,
	// before wails.Run, rather than from app.startup: whether the OS
	// close button should hide the window (HideWindowOnClose below) or
	// really close it depends on whether a tray now exists to bring the
	// window back, and that has to be decided before the window itself is
	// created.
	settings, err := loadSettings()
	if err != nil {
		settings = defaultSettings()
	}
	mod, vk, err := parseHotkey(settings.QuickNoteHotkey)
	if err != nil {
		// An unparsable persisted value disables the hotkey rather than
		// blocking startup; UpdateSettings validates every future change,
		// so a corrupt settings.json is the only way this is reachable.
		mod, vk = 0, 0
	}
	trayCtrl, trayErr := traymenu.Start(traymenu.Handlers{
		OnQuickNote:  app.handleQuickNoteTrigger,
		OnShowWindow: app.handleShowWindowTrigger,
		OnQuit:       app.handleQuitTrigger,
	}, mod, vk)
	if trayErr != nil {
		log.Printf("tray/hotkey integration unavailable: %v", trayErr)
	}
	if trayCtrl != nil {
		app.shell = trayCtrl
	}

	err = wails.Run(&options.App{
		Title:  "Beresta",
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 248, G: 246, B: 241, A: 1},
		// EnableFileDrop reports dropped files' absolute OS paths through
		// runtime.OnFileDrop (see
		// desktop/frontend/src/shell/AttachmentPanel.tsx), which the
		// attachment drag-and-drop flow (task 5.5) feeds straight into
		// AddAttachmentFromFile; only elements marked with the default
		// --wails-drop-target CSS property receive a drop. DisableWebViewDrop
		// turns off WebView2's own drop handling everywhere else, so dropping
		// a file outside the attachment panel cannot navigate the window to
		// a local file:// URL instead of doing nothing.
		DragAndDrop: &options.DragAndDrop{EnableFileDrop: true, DisableWebViewDrop: true},
		// HideWindowOnClose only takes effect once a tray icon actually
		// exists to bring the window back; otherwise the OS close button
		// must really close the app; otherwise the user would have no way
		// to quit it at all.
		HideWindowOnClose: trayCtrl != nil,
		StartHidden:       slices.Contains(os.Args[1:], autostartFlag),
		OnStartup:         app.startup,
		OnShutdown:        app.shutdown,
		Bind: []any{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
