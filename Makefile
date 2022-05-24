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
	gofmt -l -w .

.PHONY: lint
lint:
	GOOS=$(OS) GOARCH="$(GOARCH)" golangci-lint run ./...

.PHONY: build
build:
	go install github.com/werf/kubedog/cmd/kubedog

all: fmt lint build
