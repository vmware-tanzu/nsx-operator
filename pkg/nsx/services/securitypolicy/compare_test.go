package securitypolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"

	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestGroupsEqual(t *testing.T) {
	spNewGroupID := "spNewGroupID"
	tests := []struct {
		name            string
		inputGroup1     []model.Group
		inputGroup2     []model.Group
		expectedResult1 []model.Group
		expectedResult2 []model.Group
	}{
		{
			name: "group-without-additional-properties-true",
			inputGroup1: []model.Group{
				{
					Id: &spGroupID,
				},
			},
			inputGroup2: []model.Group{
				{
					Id: &spGroupID,
				},
			},
			expectedResult1: []model.Group{},
			expectedResult2: []model.Group{},
		},
		{
			name: "group-without-additional-properties-false",
			inputGroup1: []model.Group{
				{
					Id: &spGroupID,
				},
			},
			inputGroup2: []model.Group{
				{
					Id: &spNewGroupID,
				},
			},
			expectedResult1: []model.Group{
				{
					Id: &spNewGroupID,
				},
			},
			expectedResult2: []model.Group{
				{
					Id: &spGroupID,
				},
			},
		},
		{
			name: "group-with-additional-properties",
			inputGroup1: []model.Group{
				{
					Id:               &spGroupID,
					LastModifiedTime: &timeStamp,
				},
			},
			inputGroup2: []model.Group{
				{
					Id: &spGroupID,
				},
			},
			expectedResult1: []model.Group{},
			expectedResult2: []model.Group{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed, stale := common.CompareResources(GroupsToComparable(tt.inputGroup1), GroupsToComparable(tt.inputGroup2))
			changedGroups, staleGroups := ComparableToGroups(changed), ComparableToGroups(stale)
			assert.Equal(t, tt.expectedResult1, changedGroups)
			assert.Equal(t, tt.expectedResult2, staleGroups)
		},
		)
	}
}

func TestRulesEqual(t *testing.T) {
	tests := []struct {
		name            string
		inputRule1      []model.Rule
		inputRule2      []model.Rule
		expectedResult1 []model.Rule
		expectedResult2 []model.Rule
	}{
		{
			name: "rule-without-additional-properties-true",
			inputRule1: []model.Rule{
				{
					Id: &ruleID0,
				},
			},
			inputRule2: []model.Rule{
				{
					Id: &ruleID0,
				},
			},
			expectedResult1: []model.Rule{},
			expectedResult2: []model.Rule{},
		},
		{
			name: "rule-without-additional-properties-false",
			inputRule1: []model.Rule{
				{
					Id: &ruleID0,
				},
			},
			inputRule2: []model.Rule{
				{
					Id: &ruleID1,
				},
			},
			expectedResult1: []model.Rule{
				{
					Id: &ruleID1,
				},
			},
			expectedResult2: []model.Rule{
				{
					Id: &ruleID0,
				},
			},
		},
		{
			name: "rule-with-additional-properties",
			inputRule1: []model.Rule{
				{
					Id:               &ruleID0,
					LastModifiedTime: &timeStamp,
				},
			},
			inputRule2: []model.Rule{
				{
					Id: &ruleID0,
				},
			},
			expectedResult1: []model.Rule{},
			expectedResult2: []model.Rule{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed, stale := common.CompareResources(RulesToComparable(tt.inputRule1), RulesToComparable(tt.inputRule2))
			changedRules, staleRules := ComparableToRules(changed), ComparableToRules(stale)
			assert.Equal(t, tt.expectedResult1, changedRules)
			assert.Equal(t, tt.expectedResult2, staleRules)
		},
		)
	}
}

func TestSecurityPolicyEqual(t *testing.T) {
	tests := []struct {
		name            string
		inputPolicy1    *model.SecurityPolicy
		inputPolicy2    *model.SecurityPolicy
		expectedResult  *model.SecurityPolicy
		expectedResult2 bool
	}{
		{
			name: "security-policy-without-additional-properties-true",
			inputPolicy1: &model.SecurityPolicy{
				Id: &spID,
			},
			inputPolicy2: &model.SecurityPolicy{
				Id: &spID,
			},
			expectedResult: &model.SecurityPolicy{
				Id: &spID,
			},
			expectedResult2: false,
		},
		{
			name: "security-policy-without-additional-properties-false",
			inputPolicy1: &model.SecurityPolicy{
				Id: &spID,
			},
			inputPolicy2: &model.SecurityPolicy{
				Id: &spID2,
			},
			expectedResult: &model.SecurityPolicy{
				Id: &spID2,
			},
			expectedResult2: true,
		},
		{
			name: "security-policy-with-additional-properties",
			inputPolicy1: &model.SecurityPolicy{
				Id:               &spID,
				LastModifiedTime: &timeStamp,
			},
			inputPolicy2: &model.SecurityPolicy{
				Id: &spID,
			},
			expectedResult: &model.SecurityPolicy{
				Id: &spID,
			},
			expectedResult2: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isChanged := common.CompareResource(SecurityPolicyToComparable(tt.inputPolicy1), SecurityPolicyToComparable(tt.inputPolicy2))
			changedSecurityPolicy := tt.inputPolicy2
			assert.Equal(t, tt.expectedResult2, isChanged)
			assert.Equal(t, tt.expectedResult, changedSecurityPolicy)
		},
		)
	}
}
