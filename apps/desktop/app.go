package main

import (
	"context"

	"github.com/gsoares85/quiver/internal/buildinfo"
)

// App is the Wails-bound application context; its exported methods are callable from
// the frontend. It stays thin — bound methods delegate to internal/*, so no request
// or storage logic lives in the desktop shell.
type App struct {
	ctx context.Context
}

// NewApp creates the bound application.
func NewApp() *App {
	return &App{}
}

// startup captures the Wails runtime context when the app launches; later runtime
// calls (events, dialogs) use it.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// Version returns the binary's version and build metadata, delegating to the shared
// buildinfo package so the desktop app and CLI report identical values.
func (a *App) Version() string {
	return buildinfo.Get().String()
}
