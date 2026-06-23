package util

import (
	"sync"
)

const (
	FeatureContainer        = "CONTAINER"
	FeatureDFW              = "DFW"
	FeatureVPC              = "VPC"
	FeatureVPCSecurity      = "VPC_SECURITY"
	LicenseContainerNetwork = "CONTAINER_NETWORKING"
	LicenseDFW              = "DFW"
	LicenseContainer        = "CONTAINER"
	LicenseVPCSecurity      = "VPC_SECURITY"
	LicenseVPCNetworking    = "VPC_NETWORKING"
)

var (
	licenseMutex         sync.Mutex
	licenseMap           = map[string]bool{}
	hasVPCNamespacesFunc func() bool // set from cmd/main after mixed-mode init to avoid import cycle
	FeaturesToCheck      = []string{}
	FeatureLicenseMap    = map[string][]string{
		FeatureContainer: {
			LicenseContainerNetwork,
			LicenseContainer,
		},
		FeatureDFW:         {LicenseDFW},
		FeatureVPCSecurity: {LicenseVPCSecurity},
		FeatureVPC:         {LicenseVPCNetworking},
	}
)

func init() {
	for k := range FeatureLicenseMap {
		FeaturesToCheck = append(FeaturesToCheck, k)
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

// SetHasVPCNamespacesFunc sets the callback used by GetDFWLicense/UpdateDFWLicense.
// Must be called from cmd/main after mixed-mode init to avoid config->util import cycle.
func SetHasVPCNamespacesFunc(f func() bool) {
	hasVPCNamespacesFunc = f
}

func hasVPCNamespaces() bool {
	if hasVPCNamespacesFunc != nil {
		return hasVPCNamespacesFunc()
	}
	return false
}

func GetDFWLicense() bool {
	if hasVPCNamespaces() {
		return IsLicensed(LicenseVPCSecurity)
	}
	return IsLicensed(LicenseDFW)
}

func UpdateDFWLicense(isLicensed bool) {
	if hasVPCNamespaces() {
		UpdateLicense(LicenseVPCSecurity, isLicensed)
	} else {
		UpdateLicense(LicenseDFW, isLicensed)
	}
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
	if licenses == nil || len(licenses.Results) == 0 {
		log.Warn("No license information found in NSX")
		return
	}
	for _, feature := range FeaturesToCheck {
		licenseNames := FeatureLicenseMap[feature]
		license := searchLicense(licenses, licenseNames)
		UpdateLicense(feature, license)
		log.Debug("Update license", "feature", feature, "license name", licenseNames, "license", license)
	}
}
