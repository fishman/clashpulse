package ui

import (
	"os"
	"testing"

	"github.com/fishman/clashpulse/localize"
)

func TestMain(m *testing.M) {
	localize.SetLanguage("en")
	os.Exit(m.Run())
}
