.PHONY: build clean deploy run

APP_NAME := leeforest
BUILD_DIR := bin
REMOTE_USER := leeforest
REMOTE_HOST := 66.29.133.233
REMOTE_PATH := /opt/leeforest

build:
	mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/gateway

run: build
	./$(BUILD_DIR)/$(APP_NAME) -config config.json

clean:
	rm -rf $(BUILD_DIR)

deploy: build
	scp $(BUILD_DIR)/$(APP_NAME) $(REMOTE_USER)@$(REMOTE_HOST):/tmp/$(APP_NAME).new
	ssh $(REMOTE_USER)@$(REMOTE_HOST) "sudo mv /tmp/$(APP_NAME).new $(REMOTE_PATH)/$(APP_NAME) && sudo systemctl restart leeforest"

# Sync static files
deploy-www:
	rsync -avz --delete www/ $(REMOTE_USER)@$(REMOTE_HOST):$(REMOTE_PATH)/www/

# Sync config
deploy-config:
	scp config.json $(REMOTE_USER)@$(REMOTE_HOST):/tmp/config.json
	ssh $(REMOTE_USER)@$(REMOTE_HOST) "sudo mv /tmp/config.json $(REMOTE_PATH)/config.json && sudo systemctl restart leeforest"