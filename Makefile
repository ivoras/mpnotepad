BINARY      := mpnotepad
GO_PKG      := ./cmd/mpnotepad
JS_SRC      := web/js-src/editor.mjs
JS_OUT      := web/static/js/editor.js
GO_SOURCES  := $(shell find . -type f -name '*.go' -not -path './node_modules/*')
WEB_SOURCES := $(shell find web -type f -not -path 'web/static/js/editor.js')

.PHONY: all build js deps vet test clean run

all: build

build: $(BINARY)

$(BINARY): $(JS_OUT) $(GO_SOURCES)
	go build -o $(BINARY) $(GO_PKG)

$(JS_OUT): $(JS_SRC) node_modules package.json
	npm run build:js

js: $(JS_OUT)

node_modules: package.json package-lock.json
	npm install
	@touch node_modules

deps: node_modules

vet:
	go vet ./...

test:
	go test ./...

run: build
	./$(BINARY) serve

clean:
	rm -f $(BINARY) $(JS_OUT)
