/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsxserviceaccount

import (
	"errors"
	mpmodel "github.com/vmware/vsphere-automation-sdk-go/services/nsxt-mp/nsx/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

// PrincipalIdentityStore is a store for PrincipalIdentity
type PrincipalIdentityStore struct {
	common.ResourceStore
}

func (s *PrincipalIdentityStore) Operate(i interface{}) error {
	pis := i.(*[]mpmodel.PrincipalIdentity)
	for _, pi := range *pis {
		// MP resource doesn't have MarkedForDelete tag.
		err := s.Add(pi)
		log.V(1).Info("add PI to store", "pi", pi)
		if err != nil {
			return err
		}
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

func (s *ClusterControlPlaneStore) Operate(i interface{}) error {
	pis := i.(*[]model.ClusterControlPlane)
	for _, pi := range *pis {
		if pi.MarkedForDelete != nil && *pi.MarkedForDelete {
			err := s.Delete(pi)
			log.V(1).Info("delete PI from store", "pi", pi)
			if err != nil {
				return err
			}
		} else {
			err := s.Add(pi)
			log.V(1).Info("add PI to store", "pi", pi)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// keyFunc returns the key of the object.
func keyFunc(obj interface{}) (string, error) {
	switch v := obj.(type) {
	case model.ClusterControlPlane:
		return *v.Id, nil
	case mpmodel.PrincipalIdentity:
		return *v.Name, nil
	default:
		return "", errors.New("keyFunc doesn't support unknown type")
	}
}

// indexFunc is used to get index of a resource, usually, which is the UID of the CR controller reconciles,
// index is used to filter out resources which are related to the CR
func indexFunc(obj interface{}) ([]string, error) {
	res := make([]string, 0, 5)
	switch o := obj.(type) {
	case model.ClusterControlPlane:
		return filterTag(o.Tags), nil
	case mpmodel.PrincipalIdentity:
		return filterTag(common.ConvertMPTagsToTags(o.Tags)), nil
	default:
		return res, errors.New("indexFunc doesn't support unknown type")
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
