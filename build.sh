#!/bin/bash

set -e
#set -x

# install go
source /root/.gvm/scripts/gvm

# trying to install the latest golang.
V=$(gvm listall | sed -e 's/^   //g' | grep '^go[0-9]' | grep -v rc | grep -v beta | sort | tail -n 1)

# to install the later version of golang, we need 1.4.
gvm install go1.4.3 && gvm use go1.4.3  
gvm install ${V} && gvm use ${V}

mkdir -p /src /go/src/github.com/gliderlabs
cp -r /src /go/src/github.com/gliderlabs/resolvable

export GOPATH=/go

cd /go/src/github.com/gliderlabs/resolvable
go get

export GO_EXTLINK_ENABLED=0
export CGO_ENABLED=0

go build -x --ldflags "-extldflags '-static' -X main.Version=$1" -o /resolvable
