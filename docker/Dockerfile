##
# Docker image for developing the application.
#
# This provides a development environment for the application.
#

FROM golang:1.7

MAINTAINER Homme Zwaagstra <hrz@geodata.soton.ac.uk>

COPY ./ /go/src/github.com/geo-data/nicer/

WORKDIR /go/src/github.com/geo-data/nicer/

RUN ./docker/build.sh

# Expose the realize server.
EXPOSE 5000

CMD ["bash"]
