package securitypolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
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
			e1, e2 := service.groupsCompare(tt.inputGroup1, tt.inputGroup2)
			assert.Equal(t, tt.expectedResult1, e1)
			assert.Equal(t, tt.expectedResult2, e2)
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
			e1, e2 := service.rulesCompare(tt.inputRule1, tt.inputRule2)
			assert.Equal(t, tt.expectedResult1, e1)
			assert.Equal(t, tt.expectedResult2, e2)
		},
		)
	}
}

func TestSecurityPolicyEqual(t *testing.T) {
	tests := []struct {
		name           string
		inputPolicy1   *model.SecurityPolicy
		inputPolicy2   *model.SecurityPolicy
		expectedResult *model.SecurityPolicy
	}{
		{
			name: "security-policy-without-additional-properties-true",
			inputPolicy1: &model.SecurityPolicy{
				Id: &spID,
			},
			inputPolicy2: &model.SecurityPolicy{
				Id: &spID,
			},
			expectedResult: nil,
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
			expectedResult: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedResult, service.securityPolicyCompare(tt.inputPolicy1, tt.inputPolicy2))
		},
		)
	}
}
