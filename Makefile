BINARY   := vela
EXAMPLES := examples/Counter.vela examples/TodoList.vela examples/Clock.vela
DIST     := dist

.PHONY: all build compile examples clean watch install test

all: build compile

## ── Compiler ──────────────────────────────────────────────────────────────

build:
	@echo "Building Vela compiler..."
	@go build -ldflags="-s -w" -o $(BINARY) .
	@echo "   → ./$(BINARY)"

install: build
	@mv $(BINARY) /usr/local/bin/$(BINARY)
	@echo "   Installed to /usr/local/bin/$(BINARY)"

## ── Compilation ───────────────────────────────────────────────────────────

compile: build
	@mkdir -p $(DIST)
	@echo "Compiling examples..."
	@./$(BINARY) --out $(DIST) $(EXAMPLES)
	@echo ""
	@echo "   Runtime:"
	@cp runtime/runtime.js $(DIST)/runtime.js
	@echo "   ✓  runtime/runtime.js → $(DIST)/runtime.js"

examples: compile

## ── Dev ───────────────────────────────────────────────────────────────────

watch: build
	@mkdir -p $(DIST)
	@cp runtime/runtime.js $(DIST)/runtime.js
	@./$(BINARY) --out $(DIST) --watch $(EXAMPLES)

## ── Test ──────────────────────────────────────────────────────────────────

test:
	@echo "Running Go tests..."
	@go test ./... -v

## ── Clean ─────────────────────────────────────────────────────────────────

clean:
	@rm -rf $(BINARY) $(DIST)
	@echo "   Cleaned."

## ── Size report ───────────────────────────────────────────────────────────

sizes: compile
	@echo ""
	@echo "📦 Output sizes:"
	@wc -c $(DIST)/*.js | awk '{printf "   %6d bytes  %s\n", $$1, $$2}'
	@echo ""
	@echo "   (minified + gzipped would be ~40-60% smaller)"
