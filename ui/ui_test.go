package ui

import (
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2"
)

func TestRunCanceledBeforeStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, "unused"); err != nil {
		t.Fatalf("Run returned error for canceled context: %v", err)
	}
}

func TestRunEnablesFyneDoMigration(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := Run(ctx, "unused"); err != nil {
		t.Fatal(err)
	}
	if !fyne.CurrentApp().Metadata().Migrations["fyneDo"] {
		t.Fatal("desktop app does not declare the fyne.Do migration")
	}
}
