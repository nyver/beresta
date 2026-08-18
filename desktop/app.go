package main

import "context"

// App owns desktop process lifecycle and the coarse application services that
// are exposed to the Wails frontend.
type App struct {
	ctx context.Context
}

func newApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}
