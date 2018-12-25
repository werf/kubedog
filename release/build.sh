#!/bin/bash

set -e

VERSION=$1

if [ -z "$VERSION" ] ; then
  echo "Usage: $0 VERSION"
  echo
  exit 1
fi

RELEASE_BUILD_DIR=$(pwd)/release/build

rm -rf $RELEASE_BUILD_DIR

for arch in linux darwin ; do
  outputDir=$RELEASE_BUILD_DIR/$arch-amd64

  mkdir -p $outputDir

  echo "Building kubedog for $arch, version $VERSION"
  GOOS=$arch GOARCH=amd64 go build -ldflags="-s -w -X github.com/flant/kubedog/pkg/kubedog.Version=$VERSION" -o $outputDir/kubedog github.com/flant/kubedog/cmd/kubedog

  echo "Calculating checksum kubedog.sha"
  sha256sum $outputDir/kubedog | cut -d' ' -f 1 > $outputDir/kubedog.sha
done
