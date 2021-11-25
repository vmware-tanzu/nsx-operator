# Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

# Ensure Make is run with bash shell as some syntax below is bash-specific
SHELL := /usr/bin/env bash

# Code coverage files
COVERAGE_FILE = coverage-unit.txt

.PHONY: all
all: test clean

# Run go fmt
.PHONY: fmt
fmt:
	@echo "Running go fmt for files"
	go fmt ./pkg/... ./third_party/...

# Run go vet
.PHONY: vet
vet:
	@echo "Running go vet for files"
	go vet ./pkg/... ./third_party/...

# Run tests
.PHONY: test
test: fmt vet
	@echo "Running unit test"
	@rm -f $(COVERAGE_FILE)
	go test -race -coverprofile=$(COVERAGE_FILE) -cover ./pkg/... ./third_party/...

# Clean up files from previous run
.PHONY: clean
clean:
	@rm -f $(COVERAGE_FILE)
	go mod tidy


