package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsLicensed(t *testing.T) {
	licenseMap[FeatureContainer] = true
	assert.True(t, IsLicensed(FeatureContainer))

	licenseMap[FeatureDFW] = false
	assert.False(t, IsLicensed(FeatureDFW))
}

func TestUpdateLicense(t *testing.T) {
	UpdateLicense(FeatureDFW, true)
	assert.True(t, licenseMap[FeatureDFW])

	UpdateLicense(FeatureDFW, false)
	assert.False(t, licenseMap[FeatureDFW])
}

func TestSearchLicense(t *testing.T) {
	licenses := &NsxLicense{
		Results: []struct {
			FeatureName string `json:"feature_name"`
			IsLicensed  bool   `json:"is_licensed"`
		}{
			{
				FeatureName: LicenseContainer,
				IsLicensed:  true,
			},
			{
				FeatureName: LicenseDFW,
				IsLicensed:  false,
			},
		},
	}

	// Search for license that exists
	assert.True(t, searchLicense(licenses, FeatureLicenseMap[FeatureContainer]))

	// Search for license that does not exist
	assert.False(t, searchLicense(licenses, []string{"IDFW"}))

	// Search with empty results
	licenses.Results = []struct {
		FeatureName string `json:"feature_name"`
		IsLicensed  bool   `json:"is_licensed"`
	}{}
	assert.False(t, searchLicense(licenses, FeatureLicenseMap[FeatureContainer]))

	licenses = &NsxLicense{
		Results: []struct {
			FeatureName string `json:"feature_name"`
			IsLicensed  bool   `json:"is_licensed"`
		}{
			{
				FeatureName: LicenseContainerNetwork,
				IsLicensed:  true,
			},
			{
				FeatureName: LicenseDFW,
				IsLicensed:  false,
			},
			{
				FeatureName: LicenseContainer,
				IsLicensed:  false,
			},
		},
	}
	assert.True(t, searchLicense(licenses, FeatureLicenseMap[FeatureContainer]))

	licenses = &NsxLicense{
		Results: []struct {
			FeatureName string `json:"feature_name"`
			IsLicensed  bool   `json:"is_licensed"`
		}{
			{
				FeatureName: LicenseContainerNetwork,
				IsLicensed:  false,
			},

			{
				FeatureName: LicenseContainer,
				IsLicensed:  true,
			},
		},
	}
	assert.False(t, searchLicense(licenses, FeatureLicenseMap[FeatureContainer]))
}

func TestUpdateFeatureLicense(t *testing.T) {
	t.Cleanup(func() { SetHasVPCNamespacesFunc(nil) })
	SetHasVPCNamespacesFunc(nil)

	// Normal case
	licenses := &NsxLicense{
		Results: []struct {
			FeatureName string `json:"feature_name"`
			IsLicensed  bool   `json:"is_licensed"`
		}{
			{FeatureName: LicenseDFW, IsLicensed: true},
			{FeatureName: LicenseContainer, IsLicensed: true},
		},
	}

	UpdateFeatureLicense(licenses)
	assert.True(t, IsLicensed(FeatureDFW))
	assert.True(t, IsLicensed(FeatureContainer))

	// Empty license list; remain the old licenses
	licenses.Results = nil
	UpdateFeatureLicense(licenses)
	assert.True(t, IsLicensed(FeatureDFW))
	assert.True(t, IsLicensed(FeatureContainer))

	licenses = &NsxLicense{
		Results: []struct {
			FeatureName string `json:"feature_name"`
			IsLicensed  bool   `json:"is_licensed"`
		}{
			{FeatureName: LicenseDFW, IsLicensed: false},
			{FeatureName: LicenseContainerNetwork, IsLicensed: false},
			{FeatureName: LicenseContainer, IsLicensed: true},
		},
	}

	UpdateFeatureLicense(licenses)
	assert.False(t, IsLicensed(FeatureDFW))
	assert.False(t, IsLicensed(FeatureContainer))

	assert.False(t, IsLicensed(FeatureVPC))
	// Equivalent to legacy SetEnableVpcNetwork(true): cluster has VPC namespaces.
	SetHasVPCNamespacesFunc(func() bool { return true })
	licenses = &NsxLicense{
		Results: []struct {
			FeatureName string `json:"feature_name"`
			IsLicensed  bool   `json:"is_licensed"`
		}{
			{FeatureName: LicenseDFW, IsLicensed: false},
			{FeatureName: LicenseContainerNetwork, IsLicensed: false},
			{FeatureName: LicenseContainer, IsLicensed: true},
			{FeatureName: LicenseVPCNetworking, IsLicensed: true},
		},
	}
	UpdateFeatureLicense(licenses)
	assert.False(t, IsLicensed(FeatureDFW))
	assert.False(t, IsLicensed(FeatureContainer))
	assert.True(t, IsLicensed(FeatureVPC))

	// Has VPC namespaces; LicenseVPCSecurity: true, GetDFWLicense: true (mirrors main SetEnableVpcNetwork(true))
	SetHasVPCNamespacesFunc(func() bool { return true })
	licenses = &NsxLicense{
		Results: []struct {
			FeatureName string `json:"feature_name"`
			IsLicensed  bool   `json:"is_licensed"`
		}{
			{FeatureName: LicenseDFW, IsLicensed: true},
			{FeatureName: LicenseContainerNetwork, IsLicensed: false},
			{FeatureName: LicenseContainer, IsLicensed: true},
			{FeatureName: LicenseVPCNetworking, IsLicensed: true},
			{FeatureName: LicenseVPCSecurity, IsLicensed: true},
		},
	}
	UpdateFeatureLicense(licenses)
	assert.True(t, GetDFWLicense())
	assert.False(t, IsLicensed(FeatureContainer))
	assert.True(t, IsLicensed(FeatureVPC))

	// Equivalent to legacy SetEnableVpcNetwork(false): no VPC namespaces, use DFW license for GetDFWLicense.
	SetHasVPCNamespacesFunc(nil)
	licenses = &NsxLicense{
		Results: []struct {
			FeatureName string `json:"feature_name"`
			IsLicensed  bool   `json:"is_licensed"`
		}{
			{FeatureName: LicenseDFW, IsLicensed: false},
			{FeatureName: LicenseContainerNetwork, IsLicensed: false},
			{FeatureName: LicenseContainer, IsLicensed: true},
			{FeatureName: LicenseVPCNetworking, IsLicensed: true},
			{FeatureName: LicenseVPCSecurity, IsLicensed: true},
		},
	}
	UpdateFeatureLicense(licenses)
	assert.False(t, GetDFWLicense())
	assert.False(t, IsLicensed(FeatureContainer))
	assert.True(t, IsLicensed(FeatureVPC))
}

func TestGetSecurityPolicyLicense(t *testing.T) {
	// Test with VPC namespaces disabled (callback nil or returns false), DFW licensed
	SetHasVPCNamespacesFunc(nil)
	UpdateLicense(LicenseDFW, true)
	assert.True(t, GetDFWLicense())

	// Test with VPC namespaces disabled, DFW not licensed
	UpdateLicense(LicenseDFW, false)
	assert.False(t, GetDFWLicense())

	// Test with VPC namespaces enabled (callback returns true), VPC security licensed
	SetHasVPCNamespacesFunc(func() bool { return true })
	UpdateLicense(LicenseVPCSecurity, true)
	assert.True(t, GetDFWLicense())

	// Test with VPC namespaces enabled, VPC security not licensed
	UpdateLicense(LicenseVPCSecurity, false)
	assert.False(t, GetDFWLicense())

	// Clean up
	SetHasVPCNamespacesFunc(nil)
}

func TestUpdateDFWLicense(t *testing.T) {
	// Test with VPC namespaces disabled, updating to licensed
	SetHasVPCNamespacesFunc(nil)
	UpdateDFWLicense(true)
	assert.True(t, licenseMap[LicenseDFW])

	// Test with VPC namespaces disabled, updating to not licensed
	UpdateDFWLicense(false)
	assert.False(t, licenseMap[LicenseDFW])

	// Test with VPC namespaces enabled, updating to licensed
	SetHasVPCNamespacesFunc(func() bool { return true })
	UpdateDFWLicense(true)
	assert.True(t, licenseMap[LicenseVPCSecurity])

	// Test with VPC namespaces enabled, updating to not licensed
	UpdateDFWLicense(false)
	assert.False(t, licenseMap[LicenseVPCSecurity])

	// Clean up
	SetHasVPCNamespacesFunc(nil)
}
