#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
export TERM="xterm-256color"

bold="$(tput bold)"
blue="$(tput setaf 4)"
normal="$(tput sgr0)"

function qualify-gvs() {
  APIS_PKG="$1"
  GROUPS_WITH_VERSIONS="$2"
  join_char=""
  res=""

  for GVs in ${GROUPS_WITH_VERSIONS}; do
    IFS=: read -r G Vs <<<"${GVs}"

    for V in ${Vs//,/ }; do
      res="$res$join_char$APIS_PKG/$G/$V"
      join_char=","
    done
  done

  echo "$res"
}

function qualify-gs() {
  APIS_PKG="$1"
  unset GROUPS
  IFS=' ' read -ra GROUPS <<< "$2"
  join_char=""
  res=""

  for G in "${GROUPS[@]}"; do
    res="$res$join_char$APIS_PKG/$G"
    join_char=","
  done

  echo "$res"
}

VGOPATH="$VGOPATH"
MODELS_SCHEMA="$MODELS_SCHEMA"
CLIENT_GEN="$CLIENT_GEN"
DEEPCOPY_GEN="$DEEPCOPY_GEN"
LISTER_GEN="$LISTER_GEN"
INFORMER_GEN="$INFORMER_GEN"
DEFAULTER_GEN="$DEFAULTER_GEN"
CONVERSION_GEN="$CONVERSION_GEN"
OPENAPI_GEN="$OPENAPI_GEN"
APPLYCONFIGURATION_GEN="$APPLYCONFIGURATION_GEN"

VIRTUAL_GOPATH="$(mktemp -d)"
trap 'rm -rf "$GOPATH"' EXIT

# Setup virtual GOPATH so the codegen tools work as expected.
(cd "$SCRIPT_DIR/.."; go mod download && "$VGOPATH" -o "$VIRTUAL_GOPATH")

export GOROOT="${GOROOT:-"$(go env GOROOT)"}"
export GOPATH="$VIRTUAL_GOPATH"
export GO111MODULE=off

ONMETAL_API_NET_ROOT="github.com/onmetal/onmetal-api-net"
ALL_GROUPS="core"
ALL_VERSION_GROUPS="core:v1alpha1"

echo "${bold}Public types${normal}"

echo "Generating ${blue}deepcopy${normal}"
"$DEEPCOPY_GEN" \
  --output-base "$GOPATH/src" \
  --go-header-file "$SCRIPT_DIR/boilerplate.go.txt" \
  --input-dirs "$(qualify-gvs "$ONMETAL_API_NET_ROOT/api" "$ALL_VERSION_GROUPS")" \
  -O zz_generated.deepcopy

echo "Generating ${blue}openapi${normal}"
"$OPENAPI_GEN" \
  --output-base "$GOPATH/src" \
  --go-header-file "$SCRIPT_DIR/boilerplate.go.txt" \
  --input-dirs "$(qualify-gvs "$ONMETAL_API_NET_ROOT/api" "$ALL_VERSION_GROUPS")" \
  --input-dirs "$ONMETAL_API_NET_ROOT/apimachinery/api/net" \
  --input-dirs "k8s.io/apimachinery/pkg/apis/meta/v1,k8s.io/apimachinery/pkg/runtime,k8s.io/apimachinery/pkg/version" \
  --input-dirs "k8s.io/api/core/v1" \
  --input-dirs "k8s.io/apimachinery/pkg/api/resource" \
  --output-package "$ONMETAL_API_NET_ROOT/client-go/openapi" \
  -O zz_generated.openapi \
  --report-filename "$SCRIPT_DIR/../client-go/openapi/api_violations.report"

echo "Generating ${blue}applyconfiguration${normal}"
applyconfigurationgen_external_apis+=("k8s.io/apimachinery/pkg/apis/meta/v1")
applyconfigurationgen_external_apis+=("$(qualify-gvs "$ONMETAL_API_NET_ROOT/api" "$ALL_VERSION_GROUPS")")
applyconfigurationgen_external_apis_csv=$(IFS=,; echo "${applyconfigurationgen_external_apis[*]}")
"$APPLYCONFIGURATION_GEN" \
  --output-base "$GOPATH/src" \
  --go-header-file "$SCRIPT_DIR/boilerplate.go.txt" \
  --input-dirs "${applyconfigurationgen_external_apis_csv}" \
  --openapi-schema <("$MODELS_SCHEMA" --openapi-package "$ONMETAL_API_NET_ROOT/client-go/openapi" --openapi-title "onmetal-api-net") \
  --output-package "$ONMETAL_API_NET_ROOT/client-go/applyconfigurations"

echo "Generating ${blue}client${normal}"
"$CLIENT_GEN" \
  --output-base "$GOPATH/src" \
  --go-header-file "$SCRIPT_DIR/boilerplate.go.txt" \
  --input "$(qualify-gvs "$ONMETAL_API_NET_ROOT/api" "$ALL_VERSION_GROUPS")" \
  --output-package "$ONMETAL_API_NET_ROOT/client-go" \
  --apply-configuration-package "$ONMETAL_API_NET_ROOT/client-go/applyconfigurations" \
  --clientset-name "onmetalapinet" \
  --input-base ""

echo "Generating ${blue}lister${normal}"
"$LISTER_GEN" \
  --output-base "$GOPATH/src" \
  --go-header-file "$SCRIPT_DIR/boilerplate.go.txt" \
  --input-dirs "$(qualify-gvs "$ONMETAL_API_NET_ROOT/api" "$ALL_VERSION_GROUPS")" \
  --output-package "$ONMETAL_API_NET_ROOT/client-go/listers"

echo "Generating ${blue}informer${normal}"
"$INFORMER_GEN" \
  --output-base "$GOPATH/src" \
  --go-header-file "$SCRIPT_DIR/boilerplate.go.txt" \
  --input-dirs "$(qualify-gvs "$ONMETAL_API_NET_ROOT/api" "$ALL_VERSION_GROUPS")" \
  --versioned-clientset-package "$ONMETAL_API_NET_ROOT/client-go/onmetalapinet" \
  --listers-package "$ONMETAL_API_NET_ROOT/client-go/listers" \
  --output-package "$ONMETAL_API_NET_ROOT/client-go/informers" \
  --single-directory

echo "${bold}Internal types${normal}"

echo "Generating ${blue}deepcopy${normal}"
"$DEEPCOPY_GEN" \
  --output-base "$GOPATH/src" \
  --go-header-file "$SCRIPT_DIR/boilerplate.go.txt" \
  --input-dirs "$(qualify-gs "$ONMETAL_API_NET_ROOT/internal/apis" "$ALL_GROUPS")" \
  -O zz_generated.deepcopy

echo "Generating ${blue}defaulter${normal}"
"$DEFAULTER_GEN" \
  --output-base "$GOPATH/src" \
  --go-header-file "$SCRIPT_DIR/boilerplate.go.txt" \
  --input-dirs "$(qualify-gvs "$ONMETAL_API_NET_ROOT/internal/apis" "$ALL_VERSION_GROUPS")" \
  -O zz_generated.defaults

echo "Generating ${blue}conversion${normal}"
"$CONVERSION_GEN" \
  --output-base "$GOPATH/src" \
  --go-header-file "$SCRIPT_DIR/boilerplate.go.txt" \
  --input-dirs "$(qualify-gs "$ONMETAL_API_NET_ROOT/internal/apis" "$ALL_GROUPS")" \
  --input-dirs "$(qualify-gvs "$ONMETAL_API_NET_ROOT/internal/apis" "$ALL_VERSION_GROUPS")" \
  -O zz_generated.conversion
