.PHONY: help
help:
	@echo "Makefile commands:"
	@echo "  test       - Run tests"
	@echo "  test-short - Run tests excluding network integration tests"
	@echo "  build      - Build the CLI binary"
	@echo "  clean      - Remove build artifacts"
	@echo "  tidy       - Run go mod tidy"

.PHONY: test
test:
	go test -v ./...

.PHONY: test-short
test-short:
	go test -v -short ./...

.PHONY: build
build:
	go build -o dist/tfmoduleschema ./cmd/tfmoduleschema

.PHONY: clean
clean:
	rm -rf dist/

.PHONY: tidy
tidy:
	go mod tidy
