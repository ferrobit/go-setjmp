GO ?= go
CC ?= gcc
QEMU ?= qemu-aarch64-static
CROSS ?= aarch64-linux-gnu
BUILD := build
LIBRARY := $(BUILD)/libsetjmp.so
APPLICATION := $(BUILD)/application
SOURCES := $(wildcard *.go *.s library/*.go)

.PHONY: all test stress run test-arm64 run-arm64 clean

all: $(APPLICATION)

$(LIBRARY): $(SOURCES) go.mod
	$(GO) build -buildmode=c-shared -o $@ ./library

$(APPLICATION): application/main.c $(LIBRARY)
	$(CC) -O2 -Wall -Wextra -I$(BUILD) -o $@ $< -L$(BUILD) -lsetjmp -lpthread -Wl,-rpath,'$$ORIGIN'

test:
	$(GO) vet ./...
	$(GO) test -count=1 -cover .

stress:
	GOGC=1 GODEBUG=gccheckmark=1,clobberfree=1 $(GO) test -count=10 -cpu=1,2,4 .

run: $(APPLICATION)
	$(APPLICATION)
	GOGC=1 GODEBUG=gccheckmark=1,clobberfree=1 $(APPLICATION) > /dev/null

test-arm64:
	GOARCH=arm64 $(GO) vet .
	GOARCH=arm64 GODEBUG=asyncpreemptoff=1 $(GO) test -count=1 -exec $(QEMU) -skip TestSet_Traceback .

run-arm64:
	mkdir -p $(BUILD)/arm64
	CGO_ENABLED=1 GOARCH=arm64 CC=$(CROSS)-gcc $(GO) build -buildmode=c-shared -o $(BUILD)/arm64/libsetjmp.so ./library
	$(CROSS)-gcc -O2 -Wall -Wextra -I$(BUILD)/arm64 -o $(BUILD)/arm64/application application/main.c -L$(BUILD)/arm64 -lsetjmp -lpthread -Wl,-rpath,'$$ORIGIN'
	$(QEMU) -L /usr/$(CROSS) $(BUILD)/arm64/application

clean:
	rm -rf $(BUILD)
