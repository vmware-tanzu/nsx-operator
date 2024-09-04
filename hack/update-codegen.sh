#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

APIS_PKG=github.com/vmware-tanzu/nsx-operator/pkg/apis
OUTPUT_PKG=github.com/vmware-tanzu/nsx-operator/pkg/client
GROUP=vpc

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
CODEGEN_PKG=$(go env GOMODCACHE)/k8s.io/code-generator@v0.27.1

rm -fr ./pkg/client

bash "${CODEGEN_PKG}"/generate-groups.sh "deepcopy,client,informer,lister" \
${OUTPUT_PKG} ${APIS_PKG} \
${GROUP}:v1alpha1 \
--go-header-file "${SCRIPT_ROOT}"/hack/boilerplate.go.txt \
--output-base "${SCRIPT_ROOT}" -v 10

mv ./${OUTPUT_PKG} ./pkg/
cd ./pkg/client
go mod init github.com/vmware-tanzu/nsx-operator/pkg/client
go mod tidy
cd ../../
rm -rf ./github.com