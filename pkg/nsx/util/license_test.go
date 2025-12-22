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
	SetEnableVpcNetwork(true)
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

	// enable vpc network: true; LicenseVPCSecurity: true, GetDFWLicense: true
	SetEnableVpcNetwork(true)
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

	// enable vpc network: false; LicenseDFW: false, GetDFWLicense: false
	SetEnableVpcNetwork(false)
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

func TestSetEnableVpcNetwork(t *testing.T) {
	// Test enabling VPC network
	SetEnableVpcNetwork(true)
	assert.Equal(t, []string{LicenseVPCSecurity}, FeatureLicenseMap[FeatureVPCSecurity])

	// Test disabling VPC network
	SetEnableVpcNetwork(false)
	assert.Equal(t, []string{LicenseDFW}, FeatureLicenseMap[FeatureDFW])

	// Test toggling back to enabled
	SetEnableVpcNetwork(true)
	assert.Equal(t, []string{LicenseVPCSecurity}, FeatureLicenseMap[FeatureVPCSecurity])
}
func TestGetSecurityPolicyLicense(t *testing.T) {
	// Test with VPC network disabled, DFW licensed
	SetEnableVpcNetwork(false)
	UpdateLicense(LicenseDFW, true)
	assert.True(t, GetDFWLicense())

	// Test with VPC network disabled, DFW not licensed
	UpdateLicense(LicenseDFW, false)
	assert.False(t, GetDFWLicense())

	// Test with VPC network enabled, VPC security licensed
	SetEnableVpcNetwork(true)
	UpdateLicense(LicenseVPCSecurity, true)
	assert.True(t, GetDFWLicense())

	// Test with VPC network enabled, VPC security not licensed
	UpdateLicense(LicenseVPCSecurity, false)
	assert.False(t, GetDFWLicense())

	// Clean up
	SetEnableVpcNetwork(false)
}
func TestUpdateDFWLicense(t *testing.T) {
	// Test with VPC network disabled, updating to licensed
	SetEnableVpcNetwork(false)
	UpdateDFWLicense(true)
	assert.True(t, licenseMap[LicenseDFW])

	// Test with VPC network disabled, updating to not licensed
	UpdateDFWLicense(false)
	assert.False(t, licenseMap[LicenseDFW])

	// Test with VPC network enabled, updating to licensed
	SetEnableVpcNetwork(true)
	UpdateDFWLicense(true)
	assert.True(t, licenseMap[LicenseVPCSecurity])

	// Test with VPC network enabled, updating to not licensed
	UpdateDFWLicense(false)
	assert.False(t, licenseMap[LicenseVPCSecurity])

	// Clean up
	SetEnableVpcNetwork(false)
}
