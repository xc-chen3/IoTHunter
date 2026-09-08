package main

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// embeddedFrontend is replaced with the Vite output by the release Makefile.
// Keeping a tiny checked-in fallback makes `go build` deterministic even when
// Node.js is unavailable; release builds always run frontend-build first.
//
//go:embed frontend-dist/* frontend-dist/assets/*
var embeddedFrontend embed.FS

func main() {
	app := NewApp()
	assets := frontendAssets()
	err := wails.Run(&options.App{
		Title:       "IoTHunter",
		Width:       1380,
		Height:      860,
		AssetServer: &assetserver.Options{Assets: fs.FS(assets)},
		OnStartup:   app.startup,
		OnShutdown:  app.shutdown,
		Bind:        []interface{}{app},
	})
	if err != nil {
		println(err.Error())
	}
}

func frontendAssets() fs.FS {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "desktop", "frontend", "dist"), filepath.Join(cwd, "..", "frontend", "dist"))
	}
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		candidates = append(candidates, filepath.Join(dir, "frontend", "dist"), filepath.Join(dir, "..", "desktop", "frontend", "dist"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return os.DirFS(candidate)
		}
	}
	if assets, err := fs.Sub(embeddedFrontend, "frontend-dist"); err == nil {
		return assets
	}
	return os.DirFS(".")
}
