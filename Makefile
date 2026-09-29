# make          build ./scopolamine and copy it to $(BINDIR)
# make test     run the tests with the race detector
# make licenses write THIRD_PARTY_LICENSES.md again

BINDIR ?= $(HOME)/.local/bin
GOFLAGS_BUILD := -trimpath -ldflags="-s -w"

.PHONY: build test licenses

build:
	go build $(GOFLAGS_BUILD) -o scopolamine ./cmd/scopolamine
	install -D -m 0755 scopolamine $(BINDIR)/scopolamine

test:
	go vet ./...
	go test -race -count=1 ./...

licenses:
	./scripts/third-party-licenses.sh
