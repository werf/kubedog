#!/bin/bash

RELEASE_BUILD_DIR=$(pwd)/release-build

rm -rf $RELEASE_BUILD_DIR

for osname in linux darwin ; do
  output=$RELEASE_BUILD_DIR/$osname-amd64/kubedog

  mkdir -p $(dirname $output)

  GOOS=$osname GOARCH=amd64 go build -ldflags="-s -w" -o $output github.com/flant/kubedog/cmd/kubedog
done
