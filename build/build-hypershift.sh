#!/bin/bash

set -e

# build the hypershift binary and embed it to our image since there isn't
# a good to get the hypershift binary

cur=$(pwd)

binDir=$cur/bin

if [ ! -d "$binDir" ]; then
    mkdir $binDir
fi

workDir="/tmp/hypershift-repo"

rm -rf $workDir
rm -rf $binDir/hypershift

targetHypershiftRelease="release-4.10"

echo "# clone the hypershift repo"
git clone git@github.com:openshift/hypershift.git $workDir 1> /dev/null

cd $workDir

git checkout $targetHypershiftRelease

echo "# build hypershift binary"
export GOOS=linux
make hypershift

cp $workDir/bin/hypershift $binDir/hypershift
