#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

APIS=./pkg/apis
APIS_PKG=github.com/vmware-tanzu/nsx-operator/pkg/apis
OUTPUT_PKG=github.com/vmware-tanzu/nsx-operator/pkg/client
GROUP=nsx.vmware.com
VERSION=v1alpha1
GROUP_VERSION=$GROUP:$VERSION

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
CODEGEN_PKG=$(go env GOMODCACHE)/k8s.io/code-generator@v0.27.1

rm -fr "${APIS:?}/${GROUP:?}"
rm -fr ./pkg/client
mkdir -p "${APIS}/${GROUP}/${VERSION}"
cp -r "${APIS}/${VERSION}"/* "${APIS}/${GROUP}/${VERSION}/"


bash "${CODEGEN_PKG}"/generate-groups.sh "client,lister,informer" \
  ${OUTPUT_PKG} ${APIS_PKG} \
  ${GROUP_VERSION} \
  --go-header-file "${SCRIPT_ROOT}"/hack/boilerplate.go.txt \
  --output-base "${SCRIPT_ROOT}" -v 10

mv ./${OUTPUT_PKG} ./pkg/
rm -rf ./github.com