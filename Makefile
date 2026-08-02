IMAGE ?= ghcr.io/adrianmross/velero-plugin-for-oci
TAG   ?= dev

.PHONY: test vet build image

test:
	go test ./...

vet:
	go vet ./...

build:
	CGO_ENABLED=0 go build -trimpath -o bin/velero-plugin-for-oci .

image:
	docker build -t $(IMAGE):$(TAG) .
