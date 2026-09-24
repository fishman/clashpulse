build:
	go build -o clashpulse ./cmd/clashpulse

test:
	go test -tags ci ./...

vet:
	go vet -tags ci ./...

run: build
	./clashpulse

tui: build
	./clashpulse tui

.PHONY: build test vet run tui
