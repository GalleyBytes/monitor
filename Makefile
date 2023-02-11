DOCKER_REPO ?= ghcr.io/galleybytes
IMAGE_NAME ?= monitor
VERSION ?= $(shell  git describe --tags --dirty --match 'monitor-*'|sed s,monitor-,,)
ifeq ($(VERSION),)
VERSION := 0.0.0
endif
IMG ?= ${DOCKER_REPO}/${IMAGE_NAME}:${VERSION}

MANAGER_DIR = projects/manager
MUTATING_WEBHOOK_DIR = projects/webhook
RELEASE_PROJECT = true

build:
	docker build . -t ${IMG}

build-local:
	GOOS=linux GOARCH=amd64 go build -v -installsuffix cgo -o bin/monitor main.go

reload-to-kind: build
	kind load docker-image ${IMG}

release: build
	docker push ${IMG}

ghactions-release:
	CGO_ENABLED=0 go build -v -o bin/monitor main.go
	docker build . -t ${IMG}
	docker push ${IMG}

projects:
	RELEASE_PROJECT=true ${MUTATING_WEBHOOK_DIR}/release.sh
	RELEASE_PROJECT=true ${MANAGER_DIR}/release.sh


.PHONY: build build-local reload-to-kind release projects