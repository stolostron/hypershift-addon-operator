FROM registry.ci.openshift.org/stolostron/builder:go1.17-linux AS builder
WORKDIR /go/src/github.com/stolostron/hypershift-addon-operator
COPY . .
ENV GO_PACKAGE github.com/stolostron/hypershift-addon-operator

# Build
RUN make build --warn-undefined-variables

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM registry.access.redhat.com/ubi8/ubi-minimal:latest


ENV USER_UID=1001

# Add the binaries
COPY --from=builder /go/src/github.com/stolostron/hypershift-addon-operator/bin/hypershift-addon .
COPY --from=builder /go/src/github.com/stolostron/hypershift-addon-operator/bin/hypershift /usr/bin

ENV  PATH="/go/src/github.com/stolostron/hypershift-addon-operator/bin:$PATH"

USER ${USER_UID}
