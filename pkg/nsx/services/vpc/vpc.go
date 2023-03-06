package vpc

import (
	"sync"

	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

var (
	log             = logger.Log
	ResourceTypeVPC = common.ResourceTypeVPC
	NewConverter    = common.NewConverter
	// The following variables are defined as interface, they should be initialized as concrete type
	vpcStore common.Store
)

type VPCService struct {
	common.Service
	vpcStore *VPCStore
}

// InitializeVPC sync NSX resources
func InitializeVPC(service common.Service) (*VPCService, error) {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(1)

	VPCService := &VPCService{Service: service}

	VPCService.vpcStore = &VPCStore{ResourceStore: common.ResourceStore{
		Indexer:     cache.NewIndexer(keyFunc, cache.Indexers{common.TagScopeVPCCRUID: indexFunc}),
		BindingType: model.VpcBindingType(),
	}}

	go VPCService.InitializeResourceStore(&wg, fatalErrors, ResourceTypeVPC, vpcStore)

	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		close(fatalErrors)
		return VPCService, err
	}

	return VPCService, nil
}

func (s *VPCService) GetVPCsByNamespace(namespace string) []model.Vpc {
	return s.vpcStore.GetVPCsByNamespace(namespace)
}
