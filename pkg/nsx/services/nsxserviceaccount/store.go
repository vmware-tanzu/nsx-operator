/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"fmt"
	"reflect"

	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// PrincipalIdentityStore is a store for PrincipalIdentity
type PrincipalIdentityStore struct {
	common.ResourceStore
}

func (s *PrincipalIdentityStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	pi := i.(*mpmodel.PrincipalIdentity)
	// MP resource doesn't have MarkedForDelete tag.
	err := s.Add(pi)
	log.Debug("add PI to store", "pi", pi)
	if err != nil {
		return err
	}
	return nil
}

func (s *PrincipalIdentityStore) IsPolicyAPI() bool {
	return false
}

// ClusterControlPlaneStore is a store for ClusterControlPlane
type ClusterControlPlaneStore struct {
	common.ResourceStore
}

func (s *ClusterControlPlaneStore) Apply(i interface{}) error {
	if i == nil {
		return nil
	}
	ccp := i.(*model.ClusterControlPlane)
	if ccp.MarkedForDelete != nil && *ccp.MarkedForDelete {
		err := s.Delete(ccp)
		log.Debug("delete ClusterCP from store", "ClusterCP", ccp)
		if err != nil {
			return err
		}
	} else {
		err := s.Add(ccp)
		log.Debug("add ClusterCP to store", "ClusterCP", ccp)
		if err != nil {
			return err
		}
	}
	return nil
}

// keyFunc returns the key of the object.
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case *model.ClusterControlPlane:
		return *v.Id, nil
	case *mpmodel.PrincipalIdentity:
		return *v.Name, nil
	default:
		return "", fmt.Errorf("keyFunc doesn't support unknown type %v", reflect.TypeOf(obj))
	}
}

// indexFunc is used to get index of a resource, usually, which is the UID of the CR controller reconciles,
// index is used to filter out resources which are related to the CR
func indexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch o := obj.(type) {
	case *model.ClusterControlPlane:
		return filterTag(o.Tags), nil
	case *mpmodel.PrincipalIdentity:
		return filterTag(common.ConvertMPTagsToTags(o.Tags)), nil
	default:
		return res, fmt.Errorf("indexFunc doesn't support unknown type %v", reflect.TypeOf(obj))
	}
}

var filterTag = func(v []model.Tag) []string {
	res := make([]string, 0, 5)
	for _, tag := range v {
		if *tag.Scope == common.TagScopeNSXServiceAccountCRUID {
			res = append(res, *tag.Tag)
		}
	}
	return res
}
