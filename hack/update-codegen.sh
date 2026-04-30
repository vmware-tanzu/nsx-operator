#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
CODEGEN_PKG="$(go env GOMODCACHE)/k8s.io/code-generator@v0.35.1"

OUTPUT_PKG=github.com/vmware-tanzu/nsx-operator/pkg/client
APIS_ROOT="${SCRIPT_ROOT}/pkg/apis"
CLIENT_ROOT="${SCRIPT_ROOT}/pkg/client"
BOILERPLATE="${SCRIPT_ROOT}/hack/boilerplate.go.txt"

if [[ -f "${SCRIPT_ROOT}/pkg/client/go.mod" ]]; then
	mv "${SCRIPT_ROOT}/pkg/client/go.mod" "${SCRIPT_ROOT}/client.go.mod"
fi
rm -fr "${SCRIPT_ROOT}/pkg/client"

export KUBE_CODEGEN_TAG=v0.35.1
# shellcheck source=/dev/null
source "${CODEGEN_PKG}/kube_codegen.sh"

export KUBE_VERBOSE="${KUBE_VERBOSE:-10}"

# client-gen and go/packages load types from the module that owns the API
# packages; pkg/apis is its own module, so run codegen from that directory.
#
# Do not run kube::codegen::gen_helpers here: deepcopy-gen would overwrite
# zz_generated.deepcopy.go produced by controller-gen and drop DeepCopyObject
# unless every type carries +k8s:deepcopy-gen:interfaces=...
cd "${APIS_ROOT}"

kube::codegen::gen_client \
	--one-input-api vpc/v1alpha1 \
	--output-dir "${CLIENT_ROOT}" \
	--output-pkg "${OUTPUT_PKG}" \
	--with-watch \
	--boilerplate "${BOILERPLATE}" \
	"${APIS_ROOT}"

cd "${SCRIPT_ROOT}"

cd "${SCRIPT_ROOT}/pkg/client"
if [[ -f "${SCRIPT_ROOT}/client.go.mod" ]]; then
	mv "${SCRIPT_ROOT}/client.go.mod" ./go.mod
else
	git -C "${SCRIPT_ROOT}" show HEAD:pkg/client/go.mod >./go.mod
fi
go mod tidy
cd "${SCRIPT_ROOT}"
