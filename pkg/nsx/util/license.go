package util

import (
	"sync"
)

const (
	FeatureContainer        = "CONTAINER"
	FeatureDFW              = "DFW"
	LicenseContainerNetwork = "CONTAINER_NETWORKING"
	LicenseDFW              = "DFW"
	LicenseContainer        = "CONTAINER"
)

var (
	licenseMutex        sync.Mutex
	licenseMap          = map[string]bool{}
	Features_to_check   = []string{}
	Feature_license_map = map[string][]string{
		FeatureContainer: {
			LicenseContainerNetwork,
			LicenseContainer,
		},
		FeatureDFW: {LicenseDFW},
	}
)

func init() {
	for k := range Feature_license_map {
		Features_to_check = append(Features_to_check, k)
		licenseMap[k] = false
	}
}

type NsxLicense struct {
	Results []struct {
		FeatureName string `json:"feature_name"`
		IsLicensed  bool   `json:"is_licensed"`
	} `json:"results"`
	ResultCount int `json:"result_count"`
}

func IsLicensed(feature string) bool {
	licenseMutex.Lock()
	defer licenseMutex.Unlock()
	return licenseMap[feature]
}

func UpdateLicense(feature string, isLicensed bool) {
	licenseMutex.Lock()
	licenseMap[feature] = isLicensed
	licenseMutex.Unlock()
}

func searchLicense(licenses *NsxLicense, licenseNames []string) bool {
	license := false
	for _, licenseName := range licenseNames {
		for _, feature := range licenses.Results {
			if feature.FeatureName == licenseName {
				return feature.IsLicensed
			}
		}
	}
	return license
}

func UpdateFeatureLicense(licenses *NsxLicense) {
	for _, feature := range Features_to_check {
		licenseNames := Feature_license_map[feature]
		license := searchLicense(licenses, licenseNames)
		UpdateLicense(feature, license)
		log.Debug("Update license", "feature", feature, "license", license)
	}
}
