PREFIX ?= /usr/local
VERSION := $(shell sed -n 's/^const version = "\(.*\)"/\1/p' main.go)
# Статическая сборка без cgo: один файл работает на любом дистрибутиве (glibc и musl)
GOBUILD := CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"

.PHONY: build test install uninstall release version dmt clean

build:
	$(GOBUILD) -o crtctl .

test:
	go vet ./...
	go test ./...

install: build
	install -Dm755 crtctl $(DESTDIR)$(PREFIX)/bin/crtctl

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/crtctl

# Готовые бинарники для релиза: dist/crtctl-linux-{amd64,arm64}. Имена без версии,
# чтобы работала ссылка releases/latest/download/crtctl-linux-amd64 из README
release: test
	rm -rf dist && mkdir -p dist
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o dist/crtctl-linux-amd64 .
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o dist/crtctl-linux-arm64 .
	cd dist && sha256sum crtctl-linux-* > SHA256SUMS

# Версия для проверки тега в CI
version:
	@echo $(VERSION)

# Перегенерировать таблицу VESA DMT из edid-decode
dmt:
	./tools/gen-dmt.sh

clean:
	rm -rf crtctl dist
