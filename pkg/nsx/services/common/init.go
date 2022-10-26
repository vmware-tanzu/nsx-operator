package common

import (
	"github.com/openlyinc/pointy"
	"github.com/vmware/vsphere-automation-sdk-go/runtime/bindings"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

type Service struct {
	Client    client.Client
	NSXClient *nsx.Client
	NSXConfig *config.NSXOperatorConfig
}

const (
	PageSize                   int64 = 1000
	ResourceType                     = "resource_type"
	ResourceTypeGroup                = "group"
	ResourceTypeSecurityPolicy       = "securitypolicy"
	ResourceTypeRule                 = "rule"
)

var (
	Converter *bindings.TypeConverter
)

func init() {
	Converter = bindings.NewTypeConverter()
	Converter.SetMode(bindings.REST)
}

var (
	String = pointy.String // address of string
	Int64  = pointy.Int64  // address of int64
)
