# generate go structures from proto files
define generate_sources
	$(if $(shell command -v protoc --go_out=. 2> /dev/null),$(info golang/protobuf is installed),$(error golang/protobuf missing, please install it with go get github.com/golang/protobuf))
	@echo "# generating sources"
	@cd view && go generate -v
endef

all: generate build

generate:
	$(call generate_sources)
	@echo "# done"

clean:
	rm -f editor/editor
	rm -f view/view
	rm -f watcher/watcher
	rm -f election/election

build:
	cd view && go build -i
	cd editor && go build -i
	cd watcher && go build -i
	cd election && go build -i
