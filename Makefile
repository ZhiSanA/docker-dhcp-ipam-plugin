GOROOT := $(shell command -v go >/dev/null 2>&1 && go env GOROOT || echo "$(HOME)/.go")
PATH := $(GOROOT)/bin:$(HOME)/.local/bin:$(PATH)

BINARY_NAME = docker-dhcp-ipam-plugin
PLUGIN_NAME  = dhcp-ipam
IMAGE_NAME   = $(PLUGIN_NAME)-builder

export GOROOT
export PATH

.PHONY: build plugin-rootfs plugin clean fmt vet all

all: fmt vet build

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o build/$(BINARY_NAME) ./cmd/$(BINARY_NAME)

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test -v -count=1 -race ./pkg/...

plugin-rootfs: build
	mkdir -p plugin-rootfs/rootfs
	cp build/$(BINARY_NAME) plugin-rootfs/rootfs/
	docker build -t $(IMAGE_NAME) .
	docker create --name $(PLUGIN_NAME)-export $(IMAGE_NAME)
	docker export $(PLUGIN_NAME)-export | tar x -C plugin-rootfs/rootfs/
	docker rm $(PLUGIN_NAME)-export
	cp config.json plugin-rootfs/

plugin: plugin-rootfs
	-docker plugin rm $(PLUGIN_NAME) 2>/dev/null
	docker plugin create $(PLUGIN_NAME) plugin-rootfs/
	docker plugin enable $(PLUGIN_NAME)
	@echo "Plugin $(PLUGIN_NAME) created and enabled"

clean:
	rm -rf build rootfs plugin-rootfs
	-docker plugin rm $(PLUGIN_NAME) 2>/dev/null
	-docker rmi $(IMAGE_NAME) 2>/dev/null