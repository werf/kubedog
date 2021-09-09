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
.PHONY: fmt lint

fmt:
	gofumpt -l -w .

lint:
	GOOS=$(OS) GOARCH="$(GOARCH)" golangci-lint run ./...

all: fmt lint