# Makefile for generating isola demo GIFs

DEMO_DIR ?= /tmp/isola-demo
ISOLA_BIN ?= $(shell pwd)/../isola
OUTPUT_DIR ?= $(shell pwd)

.PHONY: all setup tui workflow clean build

all: tui workflow

# Always build a fresh isola so the demo never records a stale binary
build:
	cd .. && go build -o isola .

# Setup demo environment (with .isola.toml already configured)
setup: build
	@echo "Setting up demo environment..."
	@chmod +x setup-demo-env.sh
	@./setup-demo-env.sh "$(DEMO_DIR)" "$(ISOLA_BIN)"

# Generate TUI demo GIF
tui: setup
	@echo "Generating TUI dashboard demo..."
	@cd "$(DEMO_DIR)" && \
		PATH="$(dir $(ISOLA_BIN)):$$PATH" \
		vhs "$(OUTPUT_DIR)/demo-tui.tape" -o "$(OUTPUT_DIR)/demo-tui.gif"
	@echo "✅ Created: $(OUTPUT_DIR)/demo-tui.gif"

# Generate workflow demo GIF
workflow: setup
	@echo "Generating workflow demo..."
	@cd "$(DEMO_DIR)" && \
		PATH="$(dir $(ISOLA_BIN)):$$PATH" \
		vhs "$(OUTPUT_DIR)/demo-workflow.tape" -o "$(OUTPUT_DIR)/demo-workflow.gif"
	@echo "✅ Created: $(OUTPUT_DIR)/demo-workflow.gif"

# Clean up (also removes the demo Postgres container)
clean:
	rm -rf "$(DEMO_DIR)"
	-docker rm -f isola-demo-pg 2>/dev/null
	rm -f *.gif

# Help
help:
	@echo "Usage:"
	@echo "  make all        - Generate all demo GIFs"
	@echo "  make tui        - Generate TUI demo only"
	@echo "  make workflow   - Generate workflow demo only"
	@echo "  make setup      - Setup demo environment only"
	@echo "  make clean      - Remove generated files"
