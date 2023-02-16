module github.com/vmware-tanzu/nsx-operator

go 1.19

replace (
	github.com/vmware/go-vmware-nsxt => github.com/mengdie-song/go-vmware-nsxt v0.0.0-20220921033217-dbd234470e30 // inventory-keeper includes all commits from github.com/gran-vmv/go-vmware-nsxt v0.0.0-20211206034609-cd7ffaf2c996
	github.com/vmware/vsphere-automation-sdk-go/lib => github.com/TaoZou1/vsphere-automation-sdk-go/lib v0.5.2
	github.com/vmware/vsphere-automation-sdk-go/runtime => github.com/TaoZou1/vsphere-automation-sdk-go/runtime v0.5.2
	github.com/vmware/vsphere-automation-sdk-go/services/nsxt => github.com/TaoZou1/vsphere-automation-sdk-go/services/nsxt v0.9.3
)

require (
	github.com/agiledragon/gomonkey v2.0.2+incompatible
	github.com/agiledragon/gomonkey/v2 v2.4.0
	github.com/coreos/go-semver v0.3.0
	github.com/deckarep/golang-set v1.8.0
	github.com/go-logr/logr v1.2.0
	github.com/golang-jwt/jwt v3.2.2+incompatible
	github.com/golang/mock v1.6.0
	github.com/kevinburke/ssh_config v1.2.0
	github.com/openlyinc/pointy v1.1.2
	github.com/prometheus/client_golang v1.11.0
	github.com/sirupsen/logrus v1.8.1
	github.com/stretchr/testify v1.7.0
	github.com/vmware/govmomi v0.27.4
	github.com/vmware/vsphere-automation-sdk-go/lib v0.5.0
	github.com/vmware/vsphere-automation-sdk-go/runtime v0.5.0
	github.com/vmware/vsphere-automation-sdk-go/services/nsxt v0.6.0
	go.uber.org/zap v1.19.1
	golang.org/x/crypto v0.0.0-20210817164053-32db794688a5
	golang.org/x/time v0.0.0-20220210224613-90d013bbcef8
	gopkg.in/ini.v1 v1.66.4
	k8s.io/api v0.23.4
	k8s.io/apimachinery v0.23.4
	k8s.io/client-go v0.23.4
	sigs.k8s.io/controller-runtime v0.11.1
)

require (
	github.com/beevik/etree v1.1.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.1.1 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/evanphx/json-patch v4.12.0+incompatible // indirect
	github.com/fsnotify/fsnotify v1.5.1 // indirect
	github.com/gibson042/canonicaljson-go v1.0.3 // indirect
	github.com/go-logr/zapr v1.2.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/go-cmp v0.5.5 // indirect
	github.com/google/gofuzz v1.1.0 // indirect
	github.com/google/uuid v1.2.0 // indirect
	github.com/googleapis/gnostic v0.5.5 // indirect
	github.com/imdario/mergo v0.3.12 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.2-0.20181231171920-c182affec369 // indirect
	github.com/moby/spdystream v0.2.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_model v0.2.0 // indirect
	github.com/prometheus/common v0.28.0 // indirect
	github.com/prometheus/procfs v0.6.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stretchr/objx v0.1.1 // indirect
	go.uber.org/atomic v1.7.0 // indirect
	go.uber.org/multierr v1.6.0 // indirect
	golang.org/x/net v0.0.0-20211209124913-491a49abca63 // indirect
	golang.org/x/oauth2 v0.0.0-20210819190943-2bc19b11175f // indirect
	golang.org/x/sys v0.0.0-20211029165221-6e7872819dc8 // indirect
	golang.org/x/term v0.0.0-20210615171337-6886f2dfbf5b // indirect
	golang.org/x/text v0.3.7 // indirect
	gomodules.xyz/jsonpatch/v2 v2.2.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/protobuf v1.27.1 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.0 // indirect
	k8s.io/apiextensions-apiserver v0.23.0 // indirect
	k8s.io/component-base v0.23.0 // indirect
	k8s.io/klog/v2 v2.30.0 // indirect
	k8s.io/kube-openapi v0.0.0-20211115234752-e816edb12b65 // indirect
	k8s.io/utils v0.0.0-20211116205334-6203023598ed // indirect
	sigs.k8s.io/json v0.0.0-20211020170558-c049b76a60c6 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.1 // indirect
	sigs.k8s.io/yaml v1.3.0 // indirect
)
