package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := newApp()
	err := wails.Run(&options.App{
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
		OnStartup:   app.startup,
		OnShutdown:  app.shutdown,
		Bind: []any{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
