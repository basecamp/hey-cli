BIN_DIR := ./bin
BINARY  := $(BIN_DIR)/hey
MODULE  := ./cmd/hey

.PHONY: build test lint clean install

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BINARY) $(MODULE)

test:
	go test ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BIN_DIR)

install: build
	cp $(BINARY) $(GOPATH)/bin/hey 2>/dev/null || cp $(BINARY) $(HOME)/go/bin/hey
