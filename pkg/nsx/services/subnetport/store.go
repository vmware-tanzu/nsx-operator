package subnetport

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// keyFunc is used to get the key of a resource, usually, which is the ID of the resource
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.VpcSubnetPort:
		return *v.Id, nil
	case types.UID:
		return string(v), nil
	case string:
		return v, nil
	case *mpmodel.VirtualNetworkInterface:
		// Generate a random UUID as the suffix because VIF doesn't have an Id field and the ExternalId is not unique.
		externalID := ""
		if v.ExternalId != nil && *v.ExternalId != "" {
			externalID = *v.ExternalId
		}
		uid := fmt.Sprintf("%s-%s", externalID, uuid.New().String())
		return uid, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

func filterTag(tags []model.Tag, tagScope string) []string {
	var res []string
	for _, tag := range tags {
		if *tag.Scope == tagScope {
			res = append(res, *tag.Tag)
		}
	}
	return res
}

// subnetPortIndexByCRUID is used to get index of a resource, usually, which is the UID of the CR controller reconciles,
// index is used to filter out resources which are related to the CR
func subnetPortIndexByCRUID(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnetPort:
		return filterTag(o.Tags, common.TagScopeSubnetPortCRUID), nil
	default:
		return nil, errors.New("subnetPortIndexByCRUID doesn't support unknown type")
	}
}

func subnetPortIndexByPodUID(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnetPort:
		return filterTag(o.Tags, common.TagScopePodUID), nil
	default:
		return nil, errors.New("subnetPortIndexByPodUID doesn't support unknown type")
	}
}

func subnetPortIndexBySubnetID(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnetPort:
		vpcInfo, err := common.ParseVPCResourcePath(*o.Path)
		if err != nil {
			return nil, err
		}
		return []string{vpcInfo.ParentID}, nil

	default:
		return nil, errors.New("subnetPortIndexBySubnetID doesn't support unknown type")
	}
}

func subnetPortIndexNamespace(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnetPort:
		return filterTag(o.Tags, common.TagScopeVMNamespace), nil
	default:
		return nil, errors.New("subnetPortIndexNamespace doesn't support unknown type")
	}
}

func subnetPortIndexPodNamespace(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *model.VpcSubnetPort:
		return filterTag(o.Tags, common.TagScopeNamespace), nil
	default:
		return nil, errors.New("subnetPortIndexPodNamespace doesn't support unknown type")
	}
}

// SubnetPortStore is a store for SubnetPorts
type SubnetPortStore struct {
	common.ResourceStore
	// PortCountInfo stores the Subnet and the information
	// regarding SubnetPort count on that Subnet
	PortCountInfo sync.Map
}

type CountInfo struct {
	// dirtyCount defines the number of SubnetPorts under creation in the Subnet
	dirtyCount int
	lock       sync.Mutex
	// totalIP defines the number of available IP in the Subnet
	totalIP            int
	exhaustedCheckTime time.Time
}

func (vs *SubnetPortStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	subnetPort := i.(*model.VpcSubnetPort)
	if subnetPort.MarkedForDelete != nil && *subnetPort.MarkedForDelete {
		err := vs.Delete(subnetPort)
		log.Debug("delete SubnetPort from store", "subnetport", subnetPort)
		if err != nil {
			return err
		}
	} else {
		err := vs.Add(subnetPort)
		log.Debug("add SubnetPort to store", "subnetport", subnetPort)
		if err != nil {
			return err
		}
	}
	return nil
}

func (subnetPortStore *SubnetPortStore) GetByKey(key string) *model.VpcSubnetPort {
	var subnetPort *model.VpcSubnetPort
	obj := subnetPortStore.ResourceStore.GetByKey(key)
	if obj != nil {
		subnetPort = obj.(*model.VpcSubnetPort)
	}
	return subnetPort
}

func (subnetPortStore *SubnetPortStore) GetByIndex(key string, value string) []*model.VpcSubnetPort {
	subnetPorts := make([]*model.VpcSubnetPort, 0)
	objs := subnetPortStore.ResourceStore.GetByIndex(key, value)
	for _, subnetPort := range objs {
		subnetPorts = append(subnetPorts, subnetPort.(*model.VpcSubnetPort))
	}
	return subnetPorts
}

func (subnetPortStore *SubnetPortStore) DeleteMultipleObjects(ports []*model.VpcSubnetPort) {
	for _, port := range ports {
		subnetPortStore.Delete(port)
	}
}

func (subnetPortStore *SubnetPortStore) GetVpcSubnetPortByUID(uid types.UID) (*model.VpcSubnetPort, error) {
	subnetPort := &model.VpcSubnetPort{}
	var indexResults []interface{}
	for _, index := range []string{common.TagScopeSubnetPortCRUID, common.TagScopePodUID} {
		indexResult, err := subnetPortStore.ByIndex(index, string(uid))
		if err != nil {
			log.Error(err, "Failed to get VpcSubnetPort", index, string(uid))
			return nil, err
		}
		indexResults = append(indexResults, indexResult...)
	}

	if len(indexResults) > 0 {
		t := indexResults[0].(*model.VpcSubnetPort)
		subnetPort = t
	} else {
		log.Info("Did not get VpcSubnetPort with index", "UID", string(uid))
		return nil, nil
	}
	return subnetPort, nil
}

type VifStore struct {
	common.ResourceStore
}

func vifIndexByAttachmentID(obj interface{}) ([]string, error) {
	switch o := obj.(type) {
	case *mpmodel.VirtualNetworkInterface:
		if o.LportAttachmentId == nil {
			return []string{}, nil
		}
		return []string{*o.LportAttachmentId}, nil
	default:
		return nil, errors.New("vifIndexByAttachmentID doesn't support unknown type")
	}
}

func NewVifStore() VifStore {
	return VifStore{
		ResourceStore: common.ResourceStore{
			Indexer: cache.NewIndexer(
				keyFunc,
				cache.Indexers{
					common.IndexKeyAttachmentID: vifIndexByAttachmentID,
				}),
			BindingType: model.VirtualNetworkInterfaceBindingType(),
		},
	}
}

func (vifStore *VifStore) GetMACByAttachmentID(attachmentID string) (string, error) {
	macAddresses := sets.New[string]()
	objects := vifStore.ResourceStore.GetByIndex(common.IndexKeyAttachmentID, attachmentID)
	if len(objects) == 0 {
		return "", fmt.Errorf("VIF not found for attachment ID: %s", attachmentID)
	}
	// We observed in some cases, multiple VIFs are returned for the same attachment ID and their MAC addresses are the same.
	// Not sure whether it's a NSX API bug or expected behavior. Whatever, we log a warning here, then continue because this may not break our logic, i.e. multiple vifs may have the same MAC address.
	if len(objects) > 1 {
		log.Warn("Multiple VIFs found for attachment ID", "attachmentID", attachmentID, "objects", objects)
	}
	for _, obj := range objects {
		vif := obj.(*mpmodel.VirtualNetworkInterface)
		if vif.MacAddress != nil && *vif.MacAddress != "" {
			macAddresses.Insert(*vif.MacAddress)
		}
	}
	if macAddresses.Len() == 0 {
		return "", fmt.Errorf("MAC address not found for attachment ID: %s", attachmentID)
	}
	if macAddresses.Len() > 1 {
		return "", fmt.Errorf("multiple MAC addresses found for attachment ID: %s", attachmentID)
	}
	return macAddresses.UnsortedList()[0], nil
}
