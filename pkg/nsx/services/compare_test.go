package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
)

func TestSecurityPolicyEqual(t *testing.T) {
	tests := []struct {
		name           string
		inputPolicy1   *model.SecurityPolicy
		inputPolicy2   *model.SecurityPolicy
		expectedResult bool
	}{
		{
			name: "security-policy-without-additional-properties-true",
			inputPolicy1: &model.SecurityPolicy{
				Id: &spID,
			},
			inputPolicy2: &model.SecurityPolicy{
				Id: &spID,
			},
			expectedResult: true,
		},
		{
			name: "security-policy-without-additional-properties-false",
			inputPolicy1: &model.SecurityPolicy{
				Id: &spID,
			},
			inputPolicy2: &model.SecurityPolicy{
				Id: &spID2,
			},
			expectedResult: false,
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
			expectedResult: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedResult, SecurityPolicyEqual(tt.inputPolicy1, tt.inputPolicy2))
		},
		)
	}
}

func TestRulesEqual(t *testing.T) {
	tests := []struct {
		name           string
		inputRule1     []model.Rule
		inputRule2     []model.Rule
		expectedResult bool
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
			expectedResult: true,
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
			expectedResult: false,
		},
		{
			name: "rule-with-additional-properties",
			inputRule1: []model.Rule{
				{
					Id: &ruleID0,
				},
				{
					Id: &ruleID1,
				},
			},
			inputRule2: []model.Rule{
				{
					Id: &ruleID0,
				},
			},
			expectedResult: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := RulesEqual(tt.inputRule1, tt.inputRule2)
			assert.Equal(t, tt.expectedResult, e)
		},
		)
	}
}

func TestGroupsEqual(t *testing.T) {
	spNewGroupID := "spNewGroupID"
	tests := []struct {
		name           string
		inputGroup1    []model.Group
		inputGroup2    []model.Group
		expectedResult bool
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
			expectedResult: true,
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
			expectedResult: false,
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
			expectedResult: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isEqual, _ := GroupsEqual(tt.inputGroup1, tt.inputGroup2)
			assert.Equal(t, tt.expectedResult, isEqual)
		},
		)
	}
}
