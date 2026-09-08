/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package common

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

// SetSubnetPortDNSRecordCondition merges or sets the DNSRecordReady condition on a SubnetPort CR.
func SetSubnetPortDNSRecordCondition(subnetPort *v1alpha1.SubnetPort, status v1.ConditionStatus, reason, message string) bool {
	newCond := v1alpha1.Condition{
		Type:               v1alpha1.ConditionType(nsxutil.ConditionTypeDNSRecordReady),
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
	for i := range subnetPort.Status.Conditions {
		if subnetPort.Status.Conditions[i].Type == newCond.Type {
			if subnetPort.Status.Conditions[i].Status == newCond.Status &&
				subnetPort.Status.Conditions[i].Reason == newCond.Reason &&
				subnetPort.Status.Conditions[i].Message == newCond.Message {
				return false
			}
			subnetPort.Status.Conditions[i] = newCond
			return true
		}
	}
	subnetPort.Status.Conditions = append(subnetPort.Status.Conditions, newCond)
	return true
}

// SetIPAddressAllocationDNSRecordCondition merges or sets the DNSRecordReady condition on an IPAddressAllocation CR.
func SetIPAddressAllocationDNSRecordCondition(ipa *v1alpha1.IPAddressAllocation, status v1.ConditionStatus, reason, message string) bool {
	newCond := v1alpha1.Condition{
		Type:               v1alpha1.ConditionType(nsxutil.ConditionTypeDNSRecordReady),
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
	for i := range ipa.Status.Conditions {
		if ipa.Status.Conditions[i].Type == newCond.Type {
			if ipa.Status.Conditions[i].Status == newCond.Status &&
				ipa.Status.Conditions[i].Reason == newCond.Reason &&
				ipa.Status.Conditions[i].Message == newCond.Message {
				return false
			}
			ipa.Status.Conditions[i] = newCond
			return true
		}
	}
	ipa.Status.Conditions = append(ipa.Status.Conditions, newCond)
	return true
}

// RemoveSubnetPortDNSRecordCondition removes the DNSRecordReady condition from a SubnetPort CR.
func RemoveSubnetPortDNSRecordCondition(subnetPort *v1alpha1.SubnetPort) bool {
	idx := -1
	for i, c := range subnetPort.Status.Conditions {
		if c.Type == v1alpha1.ConditionType(nsxutil.ConditionTypeDNSRecordReady) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	subnetPort.Status.Conditions = append(subnetPort.Status.Conditions[:idx], subnetPort.Status.Conditions[idx+1:]...)
	return true
}

// RemoveIPAddressAllocationDNSRecordCondition removes the DNSRecordReady condition from an IPAddressAllocation CR.
func RemoveIPAddressAllocationDNSRecordCondition(ipa *v1alpha1.IPAddressAllocation) bool {
	idx := -1
	for i, c := range ipa.Status.Conditions {
		if c.Type == v1alpha1.ConditionType(nsxutil.ConditionTypeDNSRecordReady) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	ipa.Status.Conditions = append(ipa.Status.Conditions[:idx], ipa.Status.Conditions[idx+1:]...)
	return true
}
