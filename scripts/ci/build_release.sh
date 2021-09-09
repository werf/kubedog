#!/bin/bash -e

export RELEASE_BUILD_DIR=release-build
export GO111MODULE=on
export CGO_ENABLED=0

go_build_v2() {
    VERSION=$1

    rm -rf $RELEASE_BUILD_DIR/$VERSION
    mkdir -p $RELEASE_BUILD_DIR/$VERSION
    chmod -R 0777 $RELEASE_BUILD_DIR/$VERSION

    for os in linux darwin windows ; do
        for arch in amd64 ; do
            outputFile=$RELEASE_BUILD_DIR/$VERSION/$os-$arch/bin/kubedog
            if [ "$os" == "windows" ] ; then
                outputFile=$outputFile.exe
            fi

            echo "# Building kubedog $VERSION for $os $arch ..."

            GOOS=$os GOARCH=$arch \
              go build -ldflags="-s -w -X github.com/werf/kubedog.Version=$VERSION" \
                       -o $outputFile github.com/werf/kubedog/cmd/kubedog

            echo "# Built $outputFile"
        done
    done
}

VERSION=$1
if [ -z "$VERSION" ] ; then
    echo "Required version argument!" 1>&2
    echo 1>&2
    echo "Usage: $0 VERSION" 1>&2
    exit 1
fi

( go_build_v2 $VERSION ) || ( echo "Failed to build!" 1>&2 && exit 1 )
