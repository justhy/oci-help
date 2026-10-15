NAME := oci-help
VERSION := $(shell git describe --tags || echo "unknown-version")
COMMIT := $(shell git rev-parse HEAD)
BUILDTIME := $(shell date -u "+%Y-%m-%d %H:%M:%S %Z")
BUILD_DIR := build
# 构建信息注入 internal/buildinfo 包（主入口已薄化为 cmd/oci-help）
VAR_SETTING := -X "oci-help/internal/buildinfo.Version=$(VERSION)" \
		-X "oci-help/internal/buildinfo.Commit=$(COMMIT)" \
		-X "oci-help/internal/buildinfo.BuildTime=$(BUILDTIME)"
# 只放公共 flag；-o 输出路径与包路径由各目标自行拼接。
# 注意：GOOS=windows 时 go build 不会自动补 .exe，输出文件名必须显式带 .exe。
GOBUILD = CGO_ENABLED=0 go build -trimpath -ldflags '-s -w -buildid= $(VAR_SETTING)'
# 本机可执行后缀：Windows 为 .exe，其他平台为空
EXE := $(shell go env GOEXE)

PLATFORM_LIST = \
	darwin-amd64 \
	darwin-arm64 \
	linux-amd64 \
	linux-arm64 \
	windows-amd64 \
	windows-arm64
	

zip_release = $(addsuffix .zip, $(PLATFORM_LIST))


.PHONY: build clean release
normal: clean build

clean:
	@rm -rf $(BUILD_DIR)
	@echo "Cleaning up."

$(zip_release): %.zip : %
	@zip -du $(BUILD_DIR)/$(NAME)-$<-$(VERSION).zip -j -m $(BUILD_DIR)/$</$(NAME)*
	@zip -du $(BUILD_DIR)/$(NAME)-$<-$(VERSION).zip *.ini
	@echo "✅ $(NAME)-$<-$(VERSION).zip"

all: linux-amd64 darwin-amd64 windows-amd64 # Most used

all-arch: $(PLATFORM_LIST)

release: $(zip_release)

build:
	@-mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(NAME)$(EXE) ./cmd/oci-help

darwin-amd64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOOS=darwin $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME) ./cmd/oci-help

darwin-arm64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm64 GOOS=darwin $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME) ./cmd/oci-help

linux-386:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=linux $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME) ./cmd/oci-help

linux-amd64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOOS=linux $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME) ./cmd/oci-help

linux-arm:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm GOOS=linux $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME) ./cmd/oci-help

linux-armv5:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm GOOS=linux GOARM=5 $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME) ./cmd/oci-help

linux-armv6:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm GOOS=linux GOARM=6 $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME) ./cmd/oci-help

linux-armv7:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm GOOS=linux GOARM=7 $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME) ./cmd/oci-help

linux-arm64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm64 GOOS=linux $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME) ./cmd/oci-help

freebsd-386:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=freebsd $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME) ./cmd/oci-help

freebsd-amd64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOOS=freebsd $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME) ./cmd/oci-help

freebsd-arm64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm64 GOOS=freebsd $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME) ./cmd/oci-help

windows-386:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=windows $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME).exe ./cmd/oci-help

windows-amd64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOOS=windows $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME).exe ./cmd/oci-help

windows-arm64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm64 GOOS=windows $(GOBUILD) -o $(BUILD_DIR)/$@/$(NAME).exe ./cmd/oci-help
