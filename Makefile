##
# Makefile for nicer.
#
# Targets:
# - clean   delete all generated files.
# - dev     enter a development environment.
# - rebuild rebuild the binary whenever the source changes.
#
# Meta targets:
# - all is the default target; it builds the nicer binary.
#

# Output binary.
BINOUT := nicer

# Golang source files.
SRC_FILES := $(find . -iname '*.go' -not -path './log/*')

# Build dependencies.
BUILD_DEPS := vendor $(SRC_FILES)

# Create production Docker image components by default.
all: $(BINOUT)

# Enter the development environment.
dev:
	docker-compose rm --all -f -v dev && \
	docker-compose run --rm --service-ports dev

# Run the tests.
test:
	go test

# Remove automatically generated files.
clean:
	@rm -rf $(BINOUT) \
		vendor \
		.glide

# Rebuild the binary whenever source files change.
rebuild: realize.yaml vendor
	realize run

# Build an executable optimised for a linux container environment. See
# <https://medium.com/@kelseyhightower/optimizing-docker-images-for-static-binaries-b5696e26eb07#.otbjvqo3i>.
$(BINOUT): BIN_VERSION := $(shell git describe --tags --abbrev=0 --match 'v[0-9]*')
$(BINOUT): BIN_COMMIT := $(shell git rev-parse --short HEAD)
$(BINOUT): $(BUILD_DEPS)
	CGO_ENABLED=0 \
	GOOS=linux \
	go build -a -tags netgo -ldflags '-w -X main.version=$(BIN_VERSION) -X main.commit=$(BIN_COMMIT)' -o $(BINOUT) *.go

vendor: glide.yaml glide.lock 
	glide install && \
	touch -c vendor

glide.lock:
	glide update --all-dependencies --resolve-current && \
	touch -c vendor

glide.yaml:
	glide init --non-interactive

# Targets without filesystem equivalents.
.PHONY: all clean dev rebuild
