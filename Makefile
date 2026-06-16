BINARY  := net-test
PKG     := .
DIST    := dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

GOFLAGS := -trimpath
LDFLAGS := -s -w -X main.version=$(VERSION)

# Pure-Go static builds: no C toolchain needed to cross-compile.
export CGO_ENABLED := 0

.DEFAULT_GOAL := build

# build for one GOOS/GOARCH into $(DIST): $(call gobuild,os,arch,outname)
define gobuild
@mkdir -p $(DIST)
@echo "  → $(DIST)/$(3)"
GOOS=$(1) GOARCH=$(2) go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(DIST)/$(3) $(PKG)
endef

.PHONY: help
help: ## Показать список целей
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n",$$1,$$2}'

.PHONY: build
build: ## Собрать нативный бинарник в ./net-test
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

.PHONY: run
run: ## Запустить (make run ARGS="-target 8.8.8.8")
	go run $(PKG) $(ARGS)

.PHONY: fmt
fmt: ## gofmt всех исходников
	gofmt -w .

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

.PHONY: test
test: ## Юнит-тесты (без сети)
	go test ./...

.PHONY: test-race
test-race: ## Юнит-тесты с детектором гонок
	go test -race ./...

.PHONY: live
live: ## Живой сетевой тест (пинг+трасса)
	NETTEST_LIVE=1 go test -race -run Live -v ./internal/probe

# --- cross-platform binaries ---

.PHONY: darwin-amd64
darwin-amd64: ## macOS Intel
	$(call gobuild,darwin,amd64,$(BINARY)-darwin-amd64)

.PHONY: darwin-arm64
darwin-arm64: ## macOS Apple Silicon
	$(call gobuild,darwin,arm64,$(BINARY)-darwin-arm64)

.PHONY: linux-amd64
linux-amd64: ## Linux x86-64
	$(call gobuild,linux,amd64,$(BINARY)-linux-amd64)

.PHONY: linux-arm64
linux-arm64: ## Linux ARM64
	$(call gobuild,linux,arm64,$(BINARY)-linux-arm64)

.PHONY: windows-amd64
windows-amd64: ## Windows x86-64
	$(call gobuild,windows,amd64,$(BINARY)-windows-amd64.exe)

.PHONY: windows-386
windows-386: ## Windows 32-bit
	$(call gobuild,windows,386,$(BINARY)-windows-386.exe)

.PHONY: dist
dist: darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64 windows-386 ## Собрать под все платформы
	@echo "сборка $(VERSION):"
	@ls -lh $(DIST)

.PHONY: checksums
checksums: ## sha256 для бинарников в dist/
	@cd $(DIST) && shasum -a 256 $(BINARY)-* > SHA256SUMS && cat SHA256SUMS

# --- Android (Fyne APK) ---
# SDK/NDK ищутся автоматически; переопределить — из командной строки:
#   make apk ANDROID_NDK_HOME=/путь/к/ndk
# ABI: по умолчанию android/arm64 (≈все телефоны); все ABI — ANDROID_ABIS=android
ANDROID_HOME     ?= $(HOME)/Library/Android/sdk
ANDROID_NDK_HOME ?= $(firstword $(wildcard $(ANDROID_HOME)/ndk/*))
ANDROID_ABIS     ?= android/arm64
ANDROID_APPID    ?= com.tavvet.nettest
# Каталог go-бинарников: уважаем пользовательский GOBIN, иначе GOPATH/bin.
GOBIN            := $(shell go env GOBIN)
ifeq ($(strip $(GOBIN)),)
GOBIN            := $(shell go env GOPATH)/bin
endif
# Fyne требует CGO (NDK-clang компилит C-glue) — поверх глобального CGO=0.
ANDROID_ENV       = CGO_ENABLED=1 ANDROID_HOME='$(ANDROID_HOME)' ANDROID_NDK_HOME='$(ANDROID_NDK_HOME)'

.PHONY: icon
icon: ## Перегенерировать mobile/app/Icon.png
	cd mobile/app && go run gen.go

.PHONY: apk
apk: ## Android APK (Fyne GUI, mobile/app) → dist/net-test.apk
	@test -x $(GOBIN)/fyne || { echo "нет fyne CLI: go install fyne.io/tools/cmd/fyne@latest"; exit 1; }
	@test -d '$(ANDROID_NDK_HOME)' || { echo "NDK не найден: задайте ANDROID_NDK_HOME (искал $(ANDROID_HOME)/ndk/*)"; exit 1; }
	@mkdir -p $(DIST)
	@echo "  → $(DIST)/net-test.apk  (NDK: $(ANDROID_NDK_HOME); $(ANDROID_ABIS))"
	cd mobile/app && $(ANDROID_ENV) \
		$(GOBIN)/fyne package -os $(ANDROID_ABIS) -appID $(ANDROID_APPID) -name net-test -icon Icon.png
	@mv mobile/app/*.apk $(DIST)/net-test.apk

.PHONY: gui
gui: ## Fyne-GUI десктоп-окном (быстрая отладка UI)
	cd mobile/app && CGO_ENABLED=1 go run .

.PHONY: test-mobile
test-mobile: ## Тесты Fyne-приложения (CGO; отдельный модуль mobile/app)
	cd mobile/app && CGO_ENABLED=1 go test ./...

.PHONY: clean
clean: ## Удалить бинарники и dist/
	rm -rf $(DIST) $(BINARY)
