#!/bin/bash

# Script to run only inventory tests for nsx-operator e2e tests
# This script is a wrapper around the go test command

# Set environment variable to enable e2e tests
export e2e=true

# Run only the inventory tests
go test -v github.com/vmware-tanzu/nsx-operator/test/e2e/tests/inventory \
  -remote.kubeconfig /root/.kube/config \
  -operator-cfg-path /etc/nsx-ujo/ncp.ini \
  -test.timeout 90m \
  -coverprofile cover-e2e.out \
  -vc-user administrator@vsphere.local \
  -vc-password UWOYj4Ltlsd.*h8T \
  -debug=true \
  "$@"