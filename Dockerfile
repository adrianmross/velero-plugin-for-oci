# Velero ObjectStore plugin for OCI Object Storage.
#
# Velero plugins are ordinary binaries that an initContainer copies into a shared
# /target volume; the Velero server then execs them over hashicorp go-plugin. No
# fork of Velero, and the plugin upgrades independently of it.
#
# busybox rather than distroless for the runtime stage: the initContainer's whole
# job is `cp /plugins/* /target/`, which needs a shell.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go vet ./... \
 && go test ./... \
 && go build -trimpath -ldflags="-s -w" -o /out/velero-plugin-for-oci .

FROM busybox:1.37
RUN mkdir -p /plugins
COPY --from=build --chmod=0755 /out/velero-plugin-for-oci /plugins/velero-plugin-for-oci
USER 65532:65532
ENTRYPOINT ["/bin/sh", "-c", "cp /plugins/* /target/"]
