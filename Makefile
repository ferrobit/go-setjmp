GO ?= go
CC ?= gcc
QEMU ?= qemu-aarch64-static
CROSS ?= aarch64-linux-gnu
BUILD := build
LIBRARY := $(BUILD)/libsetjmp.so
APPLICATION := $(BUILD)/application
EXAMPLES := examples
CGO_EXAMPLE := $(EXAMPLES)/cgo
GO_EXAMPLES := parser samefringe
SOURCES := $(wildcard *.go *.s $(CGO_EXAMPLE)/library/*.go)

.PHONY: all test stress run examples test-arm64 run-arm64 examples-arm64 clean

all: $(APPLICATION)

$(LIBRARY): $(SOURCES) go.mod
	$(GO) build -buildmode=c-shared -o $@ ./$(CGO_EXAMPLE)/library

$(APPLICATION): $(CGO_EXAMPLE)/application/main.c $(LIBRARY)
	$(CC) -O2 -Wall -Wextra -I$(BUILD) -o $@ $< -L$(BUILD) -lsetjmp -lpthread -Wl,-rpath,'$$ORIGIN'

test:
	$(GO) vet ./...
	$(GO) test -count=1 -cover .

stress:
	GOGC=1 GODEBUG=gccheckmark=1,clobberfree=1 $(GO) test -count=10 -cpu=1,2,4 .

run: $(APPLICATION)
	$(APPLICATION)
	GOGC=1 GODEBUG=gccheckmark=1,clobberfree=1 $(APPLICATION) > /dev/null

examples:
	for name in $(GO_EXAMPLES); do $(GO) run ./$(EXAMPLES)/$$name || exit 1; done

test-arm64:
	GOARCH=arm64 $(GO) vet .
	GOARCH=arm64 GODEBUG=asyncpreemptoff=1 $(GO) test -count=1 -exec $(QEMU) -skip TestSet_Traceback .

run-arm64:
	mkdir -p $(BUILD)/arm64
	CGO_ENABLED=1 GOARCH=arm64 CC=$(CROSS)-gcc $(GO) build -buildmode=c-shared -o $(BUILD)/arm64/libsetjmp.so ./$(CGO_EXAMPLE)/library
	$(CROSS)-gcc -O2 -Wall -Wextra -I$(BUILD)/arm64 -o $(BUILD)/arm64/application $(CGO_EXAMPLE)/application/main.c -L$(BUILD)/arm64 -lsetjmp -lpthread -Wl,-rpath,'$$ORIGIN'
	$(QEMU) -L /usr/$(CROSS) $(BUILD)/arm64/application

examples-arm64:
	for name in $(GO_EXAMPLES); do \
		GOARCH=arm64 GODEBUG=asyncpreemptoff=1 $(GO) run -exec $(QEMU) ./$(EXAMPLES)/$$name || exit 1; \
	done

clean:
	rm -rf $(BUILD)
