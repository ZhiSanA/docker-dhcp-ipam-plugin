BINARY_NAME = docker-dhcp-ipam-plugin
PLUGIN_NAME = dhcp-ipam

# Go build requires go in PATH. Go is installed at /home/tuzi/.go/bin/
# Add it: export PATH=$PATH:/home/tuzi/.go/bin

.PHONY: build plugin clean

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o build/$(BINARY_NAME) .

plugin: build
	mkdir -p plugin/rootfs
	cp build/$(BINARY_NAME) plugin/rootfs/
	cp config.json plugin/
	docker build -t $(PLUGIN_NAME)-rootfs .
	docker create --name tmp $(PLUGIN_NAME)-rootfs
	docker export tmp | tar x -C plugin/rootfs/
	docker rm -vf tmp
	-docker plugin rm $(PLUGIN_NAME) 2>/dev/null
	docker plugin create $(PLUGIN_NAME) plugin/
	docker plugin enable $(PLUGIN_NAME)
	@echo "Plugin $(PLUGIN_NAME) created and enabled"

clean:
	rm -rf build plugin
	-docker plugin rm $(PLUGIN_NAME) 2>/dev/null
	-docker rmi $(PLUGIN_NAME)-rootfs 2>/dev/null