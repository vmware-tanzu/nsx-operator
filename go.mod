module github.com/vmware-tanzu/nsx-operator

go 1.21.9

replace (
	github.com/vmware-tanzu/nsx-operator/pkg/apis => ./pkg/apis
	github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha1 => ./pkg/apis/v1alpha1
	github.com/vmware-tanzu/nsx-operator/pkg/apis/v1alpha2 => ./pkg/apis/v1alpha2
	github.com/vmware-tanzu/nsx-operator/pkg/client => ./pkg/client
)

require (
	github.com/agiledragon/gomonkey/v2 v2.9.0
	github.com/apparentlymart/go-cidr v1.1.0
	github.com/deckarep/golang-set v1.8.0
	github.com/go-logr/logr v1.3.0
	github.com/go-logr/zapr v1.2.4
	github.com/golang-jwt/jwt v3.2.2+incompatible
	github.com/golang/mock v1.6.0
	github.com/google/uuid v1.3.0
	github.com/kevinburke/ssh_config v1.2.0
	github.com/openlyinc/pointy v1.1.2
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.16.0
	github.com/sirupsen/logrus v1.9.0
	github.com/stretchr/testify v1.8.4
	github.com/vmware-tanzu/net-operator-api v0.0.0-20240529180459-ccac5a20bda1
	github.com/vmware-tanzu/nsx-operator/pkg/apis v0.0.0-20240305035435-c992c623aad3
	github.com/vmware-tanzu/nsx-operator/pkg/client v0.0.0-20240102061654-537b080e159f
	github.com/vmware-tanzu/vm-operator/api v1.8.2
	github.com/vmware/govmomi v0.27.4
	github.com/vmware/vsphere-automation-sdk-go/lib v0.7.0
	github.com/vmware/vsphere-automation-sdk-go/runtime v0.7.0
	github.com/vmware/vsphere-automation-sdk-go/services/nsxt v0.12.0
	github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp v0.6.0
	go.uber.org/automaxprocs v1.5.3
	go.uber.org/zap v1.25.0
	golang.org/x/crypto v0.17.0
	golang.org/x/time v0.3.0
	gopkg.in/ini.v1 v1.66.4
	k8s.io/api v0.29.3
	k8s.io/apimachinery v0.29.3
	k8s.io/client-go v0.29.3
	k8s.io/code-generator v0.29.3
	sigs.k8s.io/controller-runtime v0.16.0
)

require (
	github.com/beevik/etree v1.1.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/emicklei/go-restful/v3 v3.11.0 // indirect
	github.com/evanphx/json-patch v5.6.0+incompatible // indirect
	github.com/evanphx/json-patch/v5 v5.6.0 // indirect
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/gibson042/canonicaljson-go v1.0.3 // indirect
	github.com/go-openapi/jsonpointer v0.19.6 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.22.3 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/imdario/mergo v0.3.6 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_model v0.4.0 // indirect
	github.com/prometheus/common v0.44.0 // indirect
	github.com/prometheus/procfs v0.10.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stretchr/objx v0.5.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/exp v0.0.0-20220722155223-a9213eeb770e // indirect
	golang.org/x/mod v0.14.0 // indirect
	golang.org/x/net v0.19.0 // indirect
	golang.org/x/oauth2 v0.10.0 // indirect
	golang.org/x/sys v0.15.0 // indirect
	golang.org/x/term v0.15.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	golang.org/x/tools v0.16.1 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/protobuf v1.33.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/apiextensions-apiserver v0.29.3 // indirect
	k8s.io/component-base v0.29.3 // indirect
	k8s.io/gengo v0.0.0-20230829151522-9cce18d56c01 // indirect
	k8s.io/klog/v2 v2.110.1 // indirect
	k8s.io/kube-openapi v0.0.0-20231010175941-2dd684a91f00 // indirect
	k8s.io/utils v0.0.0-20230726121419-3b25d923346b // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.1 // indirect
	sigs.k8s.io/yaml v1.3.0 // indirect
)
