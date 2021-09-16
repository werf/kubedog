#!/bin/bash -e

export GO111MODULE=on

go install github.com/werf/kubedog/cmd/kubedog
