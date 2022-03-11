#!/bin/bash

set -e

# build the hypershift binary and embed it to our image

cur=$(pwd)

binDir=$cur/bin

if [ ! -d "$binDir" ]; then
    mkdir $binDir
fi

workDir="/tmp/hypershift-repo"

rm -rf $workDir
rm -rf $binDir/hypershift

echo "# clone the hypershift repo"
git clone https://github.com/openshift/hypershift.git $workDir 1> /dev/null

cd $workDir

git checkout $TargetHypershiftRelease

echo "# build hypershift binary"
GOOS=linux CGO_ENABLED=0 GO111MODULE=on GOFLAGS=-mod=vendor go build -gcflags=all='-N -l' -o bin/hypershift .

cp $workDir/bin/hypershift $binDir/hypershift
