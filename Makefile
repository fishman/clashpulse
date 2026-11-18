PREFIX ?= $(HOME)/.local

build:
	go build -o clashpulse ./cmd/clashpulse

install: build
	install -Dm755 clashpulse $(PREFIX)/bin/clashpulse
	install -Dm644 ui/clashpulse.svg $(PREFIX)/share/icons/hicolor/scalable/apps/clashpulse.svg
	install -Dm644 clashpulse.desktop $(PREFIX)/share/applications/clashpulse.desktop

test:
	go test -tags ci ./...

vet:
	go vet -tags ci ./...

format:
	go fmt ./...

run: build
	./clashpulse

tui: build
	./clashpulse tui

screenshots:
	go test -tags 'ci screenshots' -count=1 -run TestDocsScreenshots ./ui ./tui

.PHONY: build install test vet format run tui screenshots
