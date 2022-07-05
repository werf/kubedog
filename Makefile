GOARCH = amd64

UNAME = $(shell uname -s)

ifndef OS
	ifeq ($(UNAME), Linux)
	else ifeq ($(UNAME), Darwin)
		OS = darwin
	endif
endif

GOSRC = $(shell find . -type f -name '*.go')
.DEFAULT_GOAL := all

.PHONY: fmt
fmt:
	go mod tidy
	gci write -s Standard -s Default -s 'Prefix(github.com/werf)' pkg/ cmd/
	gofumpt -extra -w cmd/ pkg/
	GOOS=$(OS) GOARCH="$(GOARCH)" golangci-lint run --fix ./...

.PHONY: lint
lint:
	GOOS=$(OS) GOARCH="$(GOARCH)" golangci-lint run ./...

.PHONY: build
build:
	go build github.com/werf/kubedog/cmd/kubedog

.PHONY: install
install:
	go build github.com/werf/kubedog/cmd/kubedog

all: fmt lint install
