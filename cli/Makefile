VERSION ?= 0.1.0-dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/version.Version=$(VERSION) \
           -X github.com/FairMind-Gen-AI-Studio/fairmind-cli/internal/version.Commit=$(COMMIT)
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64

.PHONY: build mock devbridge test vet dist sync-templates clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/fairmind ./cmd/fairmind

mock:
	go build -o bin/fairmind-mock ./cmd/fairmind-mock

devbridge:
	go build -o bin/fairmind-devbridge ./cmd/fairmind-devbridge

test:
	go test -race -count=1 ./...

vet:
	gofmt -l . | (! grep .) && go vet ./...

dist:
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; out=dist/fairmind_$(VERSION)_$${os}_$${arch}; \
	  mkdir -p $$out && CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $$out/fairmind ./cmd/fairmind && \
	  cp README.md $$out/ && tar -C dist -czf $$out.tar.gz $$(basename $$out) && rm -rf $$out; \
	done; cd dist && shasum -a 256 *.tar.gz > SHA256SUMS

# Refresh the embedded Copilot templates from the canonical github/ folder.
sync-templates:
	rm -rf internal/assets/templates
	mkdir -p internal/assets/templates
	cp -R github/skills github/agents github/copilot-instructions.fairmind.md internal/assets/templates/
	@echo "internal/assets/templates refreshed from github/"

clean:
	rm -rf bin dist
