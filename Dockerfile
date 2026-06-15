FROM registry.ci.openshift.org/stolostron/builder:go1.26-linux AS builder

WORKDIR /go/src/github.com/stolostron/hypershift-addon-operator
COPY . .
ENV GO_PACKAGE github.com/stolostron/hypershift-addon-operator
# Fall back to direct VCS download when the Go module proxy returns an error,
# and skip the checksum database to reduce the number of network round-trips.
ENV GOPROXY=https://proxy.golang.org,direct
ENV GONOSUMDB=*

# Build
RUN make build --warn-undefined-variables

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

# Add the binaries
COPY --from=builder /go/src/github.com/stolostron/hypershift-addon-operator/bin/hypershift-addon .
