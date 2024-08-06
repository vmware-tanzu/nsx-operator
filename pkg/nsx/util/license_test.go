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
	assert.True(t, searchLicense(licenses, Feature_license_map[FeatureContainer]))

	// Search for license that does not exist
	assert.False(t, searchLicense(licenses, []string{"IDFW"}))

	// Search with empty results
	licenses.Results = []struct {
		FeatureName string `json:"feature_name"`
		IsLicensed  bool   `json:"is_licensed"`
	}{}
	assert.False(t, searchLicense(licenses, Feature_license_map[FeatureContainer]))

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
	assert.True(t, searchLicense(licenses, Feature_license_map[FeatureContainer]))

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
	assert.False(t, searchLicense(licenses, Feature_license_map[FeatureContainer]))
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

	// Empty license list
	licenses.Results = nil
	UpdateFeatureLicense(licenses)
	assert.False(t, IsLicensed(FeatureDFW))
	assert.False(t, IsLicensed(FeatureContainer))

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
}
