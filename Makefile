DOCKER_REPO ?= ghcr.io/galleybytes
IMAGE_NAME ?= monitor
VERSION ?= $(shell  git describe --tags --dirty)
ifeq ($(VERSION),)
VERSION := v0.0.0
endif
IMG ?= ${DOCKER_REPO}/${IMAGE_NAME}:${VERSION}

build:
	docker build . -t ${IMG}

build-local:
	GOOS=linux GOARCH=amd64 go build -a -installsuffix cgo -o bin//monitor main.go

reload-to-kind: build
	kind load docker-image ${IMG}

release: build
	docker push ${IMG}

.PHONY: build build-local reload-to-kind release