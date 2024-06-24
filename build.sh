#!/bin/bash

set -x

OUTPUT_DIR=release
GOBUILD=${GOBUILD:-"go build -v -x"}
COMPRESSION=${COMPRESSION:-true}
DEBUGFLAGS=

if [ -n "$DEBUG" ]; then 
    DEBUGFLAGS="-gcflags=\"all=-N -l\""
fi 

function build_linux() {
    rm -f diagox
    go build -v -o diagox ./cmd/diagox
    upx diagox
}

function build_linux_arm64() {
    rm -f diagox_arm64
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -v -o diagox_arm64 ./cmd/diagox
    upx diagox_arm64
}


function build_docker() {
    imagename=${1:-"emiago/diagox"}

    rm -f diagox
    CGO_ENABLED=0 go build -v -o diagox ./cmd/diagox

    docker --debug build -t  $imagename .
}


case "$1" in
    docker)
        build_docker $2
        ;;
    linux) 
        case "$2" in 
            arm64)
                build_linux_arm64
                ;;
            amd64)
                build_linux
                ;;
            *)
                build_linux
                build_linux_arm64
                ;;
            esac
        ;;
    all)
        build_linux
        build_linux_arm64
        build_docker
esac


# CGO_LDFLAGS="-L$(pwd)/alsa-lib/src/.libs/ -L$(pwd)/whisper.cpp -L$(pwd)/portaudio/lib" \

