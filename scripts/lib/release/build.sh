export RELEASE_BUILD_DIR=release-build

go_get() {
    for os in linux darwin windows ; do
        for arch in amd64 ; do
            export GOOS=$os
            export GOARCH=$arch
            source $GOPATH/src/github.com/flant/kubedog/go-get.sh
        done
    done
}

go_build() {
    VERSION=$1

    rm -rf $RELEASE_BUILD_DIR/$VERSION
    mkdir -p $RELEASE_BUILD_DIR/$VERSION
    chmod -R 0777 $RELEASE_BUILD_DIR/$VERSION

    for os in linux darwin windows ; do
        for arch in amd64 ; do
            outputFile=$RELEASE_BUILD_DIR/$VERSION/kubedog-$os-$arch-$VERSION
            if [ "$os" == "windows" ] ; then
                outputFile=$outputFile.exe
            fi

            echo "# Building kubedog $VERSION for $os $arch ..."

            GOOS=$os GOARCH=$arch \
              go build -ldflags="-s -w -X github.com/flant/kubedog/pkg/kubedog.Version=$VERSION" \
                       -o $outputFile github.com/flant/kubedog/cmd/kubedog

            echo "# Built $outputFile"
        done
    done

    cd $RELEASE_BUILD_DIR/$VERSION/
    sha256sum kubedog-* > SHA256SUMS
    cd -
}

build_binaries() {
    VERSION=$1

    ( go_get ) || ( exit 1 )
    ( go_build $VERSION ) || ( exit 1 )
}
