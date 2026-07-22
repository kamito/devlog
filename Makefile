BINARY := devlog
ADDR   := 0.0.0.0:8080
DIR    := .

.DEFAULT_GOAL := help

## help: このヘルプを表示
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed -e 's/## //' \
		| awk -F': ' '{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

## run: 開発用にサーバーを起動(カレントディレクトリの .md を走査)
.PHONY: run
run:
	go run . -addr $(ADDR) -dir $(DIR)

## build: バイナリをビルド
.PHONY: build
build:
	go build -o $(BINARY) .

## start: ビルド済みバイナリを起動(バイナリ配置先の .md を走査)
.PHONY: start
start: build
	./$(BINARY) -addr $(ADDR)

## fmt: go fmt を実行
.PHONY: fmt
fmt:
	go fmt ./...

## vet: go vet を実行
.PHONY: vet
vet:
	go vet ./...

## tidy: 依存関係を整理
.PHONY: tidy
tidy:
	go mod tidy

## check: fmt・vet・build をまとめて実行
.PHONY: check
check: fmt vet build

## clean: ビルド成果物を削除
.PHONY: clean
clean:
	rm -f $(BINARY)
