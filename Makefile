.PHONY: clean clean-all test lint build

# Cleaning is intentionally reversible: generated site artifacts move to an
# Ariadne archive with a manifest instead of being deleted.
clean:
	cd site && npm run clean

clean-all:
	cd site && npm run clean:all

test:
	go test ./...

lint:
	golangci-lint run

build:
	go build ./...
