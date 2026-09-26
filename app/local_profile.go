package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/fishman/clashpulse/core"
	"github.com/fishman/clashpulse/subscriptions"
)

func readLocalProfile(ctx context.Context, path string) ([]byte, error) {
	fail := func(err error) ([]byte, error) {
		return nil, core.WrapActivation(core.ActivationFileInput, err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fail(err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > subscriptions.DefaultMaxProfileBytes {
		return fail(errors.New("unsafe source file"))
	}
	file, err := os.Open(absolute)
	if err != nil {
		return fail(err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return fail(errors.New("source file changed"))
	}
	body, err := io.ReadAll(io.LimitReader(file, subscriptions.DefaultMaxProfileBytes+1))
	if err != nil || len(body) == 0 || int64(len(body)) > subscriptions.DefaultMaxProfileBytes {
		return fail(errors.New("source read failed or exceeded limit"))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return body, nil
}
