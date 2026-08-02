// Command desktop is the Quiver desktop application shell (Wails v2). It embeds the
// built React frontend and starts the native webview. The shell stays thin: all
// execution logic lives in internal/* and is reached through the bound App.
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
	app := NewApp()

	if err := wails.Run(&options.App{
		Title:  "Quiver",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.startup,
		Bind:      []any{app},
	}); err != nil {
		log.Fatal(err)
	}
}
