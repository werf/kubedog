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

build_binaries $VERSION
