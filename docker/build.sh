#!/bin/bash

##
# This script creates the docker development environment.
#
# This should be run in a clean base image.
#

# Exit on any error.
set -e

GLIDE_VERSION="v0.12.3"

# Install glide.
curl -LsS https://github.com/Masterminds/glide/releases/download/${GLIDE_VERSION}/glide-${GLIDE_VERSION}-linux-amd64.tar.gz \
		| tar xzO linux-amd64/glide > /usr/local/bin/glide
chmod +x /usr/local/bin/glide

# Change to the source root directory.
SRC_DIR="$(dirname $( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd ) )"
cd $SRC_DIR

# Get the golang dependencies.
make clean vendor

# Install realize.
cd ${SRC_DIR}/vendor/github.com/tockins/realize
go get .

# Build the application.
cd $SRC_DIR
make

# Clean up.
apt-get clean
rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*
