#!/bin/bash

set -e

for f in $(find scripts/lib -type f -name "*.sh"); do
    source $f
done

VERSION=$1
if [ -z "$VERSION" ] ; then
    echo "Required version argument!" 1>&2
    echo 1>&2
    echo "Usage: $0 VERSION" 1>&2
    exit 1
fi

docker run --rm \
    --env SSH_AUTH_SOCK=$SSH_AUTH_SOCK \
    --volume $SSH_AUTH_SOCK:$SSH_AUTH_SOCK \
    --volume ~/.ssh/known_hosts:/root/.ssh/known_hosts \
    --volume $(pwd):/kubedog \
    --workdir /kubedog \
    flant/werf-builder:v1.3.0 \
    bash -ec "set -e; source scripts/lib/release/build.sh && build_binaries $VERSION"
