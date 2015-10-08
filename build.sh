#!/bin/sh
set -e
set -x

rm -rf /var/lib/apt/lists/*
sed -i -e 's/archive/kr.&/g' /etc/apt/sources.list
apt-get update && apt-get install build-essential git mercurial -y

# install go
curl -o /tmp/go.tar.gz https://storage.googleapis.com/golang/go1.5.1.linux-amd64.tar.gz
(cd /tmp/; tar zxf /tmp/go.tar.gz)

export GOROOT=/tmp/go
export PATH=${GOROOT}/bin:$PATH

mkdir -p /go/src/github.com/gliderlabs
cp -r /src /go/src/github.com/gliderlabs/resolvable
cd /go/src/github.com/gliderlabs/resolvable
export GOPATH=/go
go get
go build -ldflags "-X main.Version=$1" -o /bin/resolvable

apt-get remove -y binutils cpp cpp-4.8 dpkg-dev fakeroot fontconfig-config fonts-dejavu-core g++ g++-4.8 gcc gcc-4.8 git-man libalgorithm-diff-perl libalgorithm-diff-xs-perl libalgorithm-merge-perl libasan0 libatomic1 libc-dev-bin libc6-dev libcloog-isl4 libdpkg-perl libdrm-intel1 libdrm-nouveau2 libdrm-radeon1 libelf1 liberror-perl libfakeroot libfile-fcntllock-perl libfontconfig1 libfontenc1 libfreetype6 libgcc-4.8-dev libgl1-mesa-dri libgl1-mesa-glx libglapi-mesa libgmp10 libgomp1 libice6 libisl10 libitm1 libllvm3.4 libmpc3 libmpfr4 libpciaccess0 libpython-stdlib libpython2.7-minimal libpython2.7-stdlib libquadmath0 libsm6 libstdc++-4.8-dev libtcl8.6 libtimedate-perl libtk8.6 libtsan0 libtxc-dxtn-s2tc0 libutempter0 libx11-6 libx11-data libx11-xcb1 libxau6 libxaw7 libxcb-dri2-0 libxcb-dri3-0 libxcb-glx0 libxcb-present0 libxcb-shape0 libxcb-sync1 libxcb1 libxcomposite1 libxdamage1 libxdmcp6 libxext6 libxfixes3 libxft2 libxi6 libxinerama1 libxmu6 libxmuu1 libxpm4 libxrandr2 libxrender1 libxshmfence1 libxss1 libxt6 libxtst6 libxv1 libxxf86dga1 libxxf86vm1 linux-libc-dev make manpages manpages-dev mercurial-common patch python python-minimal python2.7 python2.7-minimal rsync tcl tcl8.6 tk tk8.6 x11-common x11-utils xbitmaps xterm
apt-get autoremove -y
apt-get clean

rm -rf /go
rm -rf /tmp
rm -rf /var/lib/apt/lists/*
