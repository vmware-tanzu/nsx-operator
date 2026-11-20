package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/orgs/projects"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestDNSRecord(t *testing.T) {
	TrackTest(t)
	StartParallel(t)

	// Also clean up if fixture discovery fails before it registers its own cleanup.
	t.Cleanup(func() {
		if t.Skipped() && !t.Failed() {
			// A skipped test has no functional assertions left to run. Keep the
			// namespace tracked for the suite's final deletion request instead of
			// turning an unsupported testbed into a failure on deletion timeout.
			log.Info("Deferring skipped DNSRecord namespace cleanup to suite cleanup", "namespace", NsDNSRecord)
			return
		}
		if err := CleanupVCNamespaces(NsDNSRecord); err != nil {
			t.Errorf("cleanup VC namespace: %v", err)
		}
	})

	// Use pre-created namespace
	ns := NsDNSRecord
	domain := prepareDNSRecordNamespace(t, ns)
	networkInfo := getNetworkInfoWithCondition(t, ns, ns, func(ni *v1alpha1.NetworkInfo) (bool, error) {
		ready := len(ni.VPCs) > 0 && slices.Contains(ni.AllowedDNSRecordDomains, domain)
		if !ready {
			log.Debug("Waiting for DNSRecord network prerequisites", "namespace", ns, "expectedDomain", domain, "allowedDomains", ni.AllowedDNSRecordDomains, "vpcCount", len(ni.VPCs))
		}
		return ready, nil
	})
	require.NotEmpty(t, networkInfo.AllowedDNSRecordDomains,
		"DNSRecord E2E requires a DNS zone in the namespace's VPCNetworkConfiguration")
	require.Contains(t, networkInfo.AllowedDNSRecordDomains, domain)

	// ParallelTests: These tests use independent DNSRecord resources and can run concurrently
	RunSubtest(t, "ParallelTests", func(t *testing.T) {
		RunSubtest(t, "testDNSRecordTypeA", func(t *testing.T) {
			StartParallel(t)
			testDNSRecordTypeA(t, ns, domain)
		})
		RunSubtest(t, "testDNSRecordTypeAAAA", func(t *testing.T) {
			StartParallel(t)
			testDNSRecordTypeAAAA(t, ns, domain)
		})
		RunSubtest(t, "testDNSRecordApexRecord", func(t *testing.T) {
			StartParallel(t)
			testDNSRecordApexRecord(t, ns, domain)
		})
	})
}

func testDNSRecordTypeA(t *testing.T, ns, domain string) {
	recordName := "e2e-dnsrecord-a"
	createDNSRecordForTest(t, &v1alpha1.DNSRecord{
		ObjectMeta: v1.ObjectMeta{Name: recordName, Namespace: ns},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName: domain, RecordName: "e2e-a",
			RecordType: v1alpha1.DNSRecordTypeA, RecordValues: []string{"10.0.0.100"},
		},
	})
	dnsRecord := waitForDNSRecordCRReady(t, ns, recordName, 0)

	assert.Equal(t, "e2e-a."+domain, dnsRecord.Status.FQDN)
	assert.Empty(t, dnsRecord.Finalizers)
	assert.Equal(t, v1alpha1.DNSRecordTypeA, dnsRecord.Spec.RecordType)
	assert.Equal(t, []string{"10.0.0.100"}, dnsRecord.Spec.RecordValues)
}

func testDNSRecordTypeAAAA(t *testing.T, ns, domain string) {
	recordName := "e2e-dnsrecord-aaaa"
	createDNSRecordForTest(t, &v1alpha1.DNSRecord{
		ObjectMeta: v1.ObjectMeta{Name: recordName, Namespace: ns},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName: domain, RecordName: "e2e-aaaa",
			RecordType: v1alpha1.DNSRecordTypeAAAA, RecordValues: []string{"2001:db8::100"},
		},
	})
	dnsRecord := waitForDNSRecordCRReady(t, ns, recordName, 0)

	assert.Equal(t, "e2e-aaaa."+domain, dnsRecord.Status.FQDN)
	assert.Empty(t, dnsRecord.Finalizers)
	assert.Equal(t, v1alpha1.DNSRecordTypeAAAA, dnsRecord.Spec.RecordType)
	assert.Equal(t, []string{"2001:db8::100"}, dnsRecord.Spec.RecordValues)

	// Update TTL and verify
	newTTL := int32(1200)
	dnsRecord.Spec.TTL = &newTTL
	updatedRecord, err := testData.crdClientset.CrdV1alpha1().DNSRecords(ns).Update(context.TODO(), dnsRecord, v1.UpdateOptions{})
	require.NoError(t, err)
	dnsRecord = waitForDNSRecordCRReady(t, ns, recordName, updatedRecord.Generation)
	require.NotNil(t, dnsRecord.Spec.TTL)
	assert.Equal(t, newTTL, *dnsRecord.Spec.TTL)
}

func testDNSRecordApexRecord(t *testing.T, ns, domain string) {
	recordName := fmt.Sprintf("apex-dns-%s", getRandomString())
	ttl := int32(300)
	apexRecord := &v1alpha1.DNSRecord{
		ObjectMeta: v1.ObjectMeta{
			Name:      recordName,
			Namespace: ns,
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   domain,
			RecordName:   "@",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.101"},
			TTL:          &ttl,
		},
	}

	createDNSRecordForTest(t, apexRecord)

	dnsRecord := waitForDNSRecordCRReady(t, ns, recordName, 0)
	assert.Equal(t, domain, dnsRecord.Status.FQDN)
	assert.Empty(t, dnsRecord.Finalizers)
}

func createDNSRecordForTest(t *testing.T, obj *v1alpha1.DNSRecord) {
	t.Helper()
	_, err := testData.crdClientset.CrdV1alpha1().DNSRecords(obj.Namespace).Create(context.Background(), obj, v1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = testData.crdClientset.CrdV1alpha1().DNSRecords(obj.Namespace).Delete(context.Background(), obj.Name, v1.DeleteOptions{})
	})
}

func waitForDNSRecordCRReady(t *testing.T, ns, dnsRecordName string, minimumGeneration int64) *v1alpha1.DNSRecord {
	t.Helper()
	log.Info("Waiting for DNSRecord CR to be ready", "ns", ns, "dnsRecordName", dnsRecordName)
	var res *v1alpha1.DNSRecord
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer deadlineCancel()
	err := wait.PollUntilContextTimeout(deadlineCtx, 1*time.Second, defaultTimeout, false, func(ctx context.Context) (done bool, err error) {
		res, err = testData.crdClientset.CrdV1alpha1().DNSRecords(ns).Get(ctx, dnsRecordName, v1.GetOptions{})
		if err != nil {
			log.Error(err, "Error fetching DNSRecord", "namespace", ns, "name", dnsRecordName)
			return false, nil
		}
		log.Info("DNSRecord status", "status", res.Status)
		if res.Generation < minimumGeneration {
			return false, nil
		}
		for _, con := range res.Status.Conditions {
			if con.Type == string(v1alpha1.Ready) && con.Status == v1.ConditionTrue && con.ObservedGeneration == res.Generation {
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err)
	return res
}

func selectDNSRecordServicePath(project, configured string, candidates []string) (string, error) {
	if configured == "" {
		if len(candidates) == 0 {
			return "", fmt.Errorf("no project DNS Service is available (candidates: %v)", candidates)
		}
		candidates = slices.Clone(candidates)
		slices.Sort(candidates)
		configured = candidates[0]
	}
	prefix := strings.TrimSuffix(project, "/") + "/dns-services/"
	id := strings.TrimPrefix(configured, prefix)
	if !strings.HasPrefix(configured, prefix) || id == "" || strings.ContainsAny(id, "/?#% \t\n\r") || id == "." || id == ".." {
		return "", fmt.Errorf("DNS service %q must be a project DNS service path under %s", configured, prefix)
	}
	return configured, nil
}

func newDNSRecordNetworkConfig(original *v1alpha1.VPCNetworkConfiguration, name, zonePath string) *v1alpha1.VPCNetworkConfiguration {
	// Copy only spec: never duplicate default annotations, ownership or status.
	spec := original.Spec.DeepCopy()
	spec.DNSZones = []string{zonePath}
	return &v1alpha1.VPCNetworkConfiguration{ObjectMeta: v1.ObjectMeta{Name: name}, Spec: *spec}
}

func dnsRecordSchemaSupportsField(raw []byte, field []string, expectedType string) (bool, error) {
	var schema struct {
		Spec struct {
			Versions []struct {
				Name   string
				Served bool
				Schema struct{ OpenAPIV3Schema map[string]interface{} }
			}
		}
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return false, err
	}
	for _, version := range schema.Spec.Versions {
		if version.Name != "v1alpha1" || !version.Served {
			continue
		}
		node := version.Schema.OpenAPIV3Schema
		for _, key := range field {
			properties, _ := node["properties"].(map[string]interface{})
			node, _ = properties[key].(map[string]interface{})
		}
		return node != nil && node["type"] == expectedType, nil
	}
	return false, nil
}

// Only setup uses this helper. Functional failures after setup still fail tests.
func dnsRecordPrerequisite(t *testing.T, satisfied bool, format string, args ...interface{}) {
	t.Helper()
	if !satisfied {
		reason := fmt.Sprintf(format, args...)
		log.Info("Skipping DNSRecord E2E: testbed prerequisites unavailable", "reason", reason)
		t.Skipf("DNSRecord E2E prerequisites unavailable: %s", reason)
	}
}

// prepareDNSRecordNamespace creates an isolated zone/configuration instead of
// requiring manual DNS configuration. Unsupported or incomplete testbeds skip
// the parent test before any DNSRecord functional assertions run.
func prepareDNSRecordNamespace(t *testing.T, ns string) string {
	t.Helper()
	ctx := context.Background()
	dnsRecordPrerequisite(t, testData != nil && testData.nsxClient != nil && testData.nsxClient.Client != nil, "NSX client is unavailable")
	dnsRecordPrerequisite(t, testData.nsxClient.NSXCheckVersion(nsx.DNSRecord), "NSX 9.2.0+ DNSRecord support is required")
	checkDNSRecordSchemas(t)

	configs := testData.crdClientset.CrdV1alpha1().VPCNetworkConfigurations()
	namespaces := testData.clientset.CoreV1().Namespaces()
	namespace, err := namespaces.Get(ctx, ns, v1.GetOptions{})
	dnsRecordPrerequisite(t, err == nil, "prerequisite API operation failed: %v", err)
	const annotation = "nsx.vmware.com/vpc_network_config"
	configName := namespace.Annotations[annotation]
	var original *v1alpha1.VPCNetworkConfiguration
	if configName != "" {
		original, err = configs.Get(ctx, configName, v1.GetOptions{})
		dnsRecordPrerequisite(t, err == nil, "prerequisite API operation failed: %v", err)
	} else {
		list, listErr := configs.List(ctx, v1.ListOptions{})
		dnsRecordPrerequisite(t, listErr == nil, "prerequisite API list failed: %v", listErr)
		for i := range list.Items {
			if common.IsDefaultNetworkConfigCR(&list.Items[i]) {
				dnsRecordPrerequisite(t, original == nil, "DNSRecord setup requires a unique default VPCNetworkConfiguration")
				original = &list.Items[i]
			}
		}
	}
	dnsRecordPrerequisite(t, original != nil, "Cannot resolve DNSRecord Namespace network configuration")
	orgID, projectID, err := common.NSXProjectPathToId(original.Spec.NSXProject)
	dnsRecordPrerequisite(t, err == nil, "prerequisite API operation failed: %v", err)
	servicePath := ""
	projectPath := strings.TrimSuffix(original.Spec.NSXProject, "/")
	paths, listErr := listDNSRecordPrerequisitePaths(projectPath + "/dns-services")
	dnsRecordPrerequisite(t, listErr == nil, "Cannot discover project DNS services: %v", listErr)
	serviceCreated := false
	serviceSafeToDelete := true
	serviceClient := projects.NewDnsServicesClient(testData.nsxClient.Cluster.NewRestConnector())
	if len(paths) == 0 {
		profilePath := original.Spec.VPCConnectivityProfile
		dnsRecordPrerequisite(t, profilePath != "", "Cannot create DNS Service: network configuration has no VPC connectivity profile")
		profile, profileErr := testData.nsxClient.Cluster.HttpGet(PolicyAPI + profilePath)
		dnsRecordPrerequisite(t, profileErr == nil, "Cannot read VPC connectivity profile: %v", profileErr)
		gateway, _ := profile["transit_gateway_path"].(string)
		dnsRecordPrerequisite(t, gateway != "", "Cannot create DNS Service: connectivity profile has no transit_gateway_path")
		allocations, allocationErr := listDNSRecordPrerequisitePaths(projectPath + "/ip-address-allocations")
		dnsRecordPrerequisite(t, allocationErr == nil, "Cannot list project listener allocations: %v", allocationErr)
		dnsRecordPrerequisite(t, len(allocations) > 0, "Cannot create DNS Service: no project IP Address Allocation is available for a listener")
		servicePath = projectPath + "/dns-services/e2e-dns-" + getRandomString()
		serviceID := strings.TrimPrefix(servicePath, projectPath+"/dns-services/")
		err = serviceClient.Patch(orgID, projectID, serviceID, model.DnsService{
			Id: ptr.To(serviceID), DisplayName: ptr.To(serviceID), ResourceType: ptr.To("DnsService"),
			TransitGateway: ptr.To(gateway), AllocatedListenerIps: []string{allocations[0]},
		})
		dnsRecordPrerequisite(t, err == nil, "Cannot provision project DNS Service using discovered gateway/listener: %v", err)
		serviceCreated = true
	} else {
		servicePath, err = selectDNSRecordServicePath(projectPath, "", paths)
	}

	dnsRecordPrerequisite(t, err == nil, "prerequisite API operation failed: %v", err)
	if serviceCreated {
		id := strings.TrimPrefix(servicePath, projectPath+"/dns-services/")
		t.Cleanup(func() {
			if !serviceSafeToDelete {
				log.Info("Retaining test DNS Service because dependent resources could not be cleaned up", "path", servicePath)
				return
			}
			assertDNSRecordCleanup(t, serviceClient.Delete(orgID, projectID, id), "delete test DNS service")
		})
	}
	serviceID := strings.TrimPrefix(servicePath, projectPath+"/dns-services/")
	_, err = testData.nsxClient.Cluster.HttpGet(PolicyAPI + servicePath)
	dnsRecordPrerequisite(t, err == nil, "DNS Service is unavailable: %v", err)

	zoneID := "e2e-dns-" + getRandomString()
	domain := zoneID + ".example.test"
	zonePath := servicePath + "/zones/" + zoneID
	isolatedName := ns + "-dns"
	createdConfig := false
	zoneCreated := false
	// Subtest DNSRecords are cleaned up before this parent callback. Delete the
	// Namespace before its network config and zone, so controllers can finish cleanup.
	t.Cleanup(func() {
		if !assertDNSRecordCleanup(t, testData.deleteVCNamespace(ns), "delete test Namespace") {
			return
		}
		if createdConfig {
			cleanupErr := wait.PollUntilContextTimeout(ctx, time.Second, 2*defaultTimeout, true, func(ctx context.Context) (bool, error) {
				config, getErr := configs.Get(ctx, isolatedName, v1.GetOptions{})
				if apierrors.IsNotFound(getErr) {
					return true, nil
				}
				if getErr != nil {
					return false, getErr
				}
				return len(config.Status.VPCs) == 0, nil
			})
			if !assertDNSRecordCleanup(t, cleanupErr, "wait for isolated network configuration to detach") {
				return
			}
			deleteErr := configs.Delete(ctx, isolatedName, v1.DeleteOptions{})
			if apierrors.IsNotFound(deleteErr) {
				deleteErr = nil
			}
			if !assertDNSRecordCleanup(t, deleteErr, "delete isolated network configuration") {
				return
			}
		}
		if zoneCreated {
			serviceSafeToDelete = assertDNSRecordCleanup(t, testData.nsxClient.DnsZoneClient.Delete(orgID, projectID, serviceID, zoneID), "delete test DNS zone")
		}
	})
	err = testData.nsxClient.DnsZoneClient.Patch(orgID, projectID, serviceID, zoneID, model.DnsZone{
		Id: ptr.To(zoneID), DisplayName: ptr.To(zoneID), DnsDomainName: ptr.To(domain), ResourceType: ptr.To("DnsZone"),
	})
	dnsRecordPrerequisite(t, err == nil, "Create dedicated DNSRecord test zone: %v", err)
	zoneCreated = true
	serviceSafeToDelete = false
	isolated := newDNSRecordNetworkConfig(original, isolatedName, zonePath)
	_, err = configs.Create(ctx, isolated, v1.CreateOptions{})
	dnsRecordPrerequisite(t, err == nil, "Create isolated DNSRecord network configuration: %v", err)
	createdConfig = true
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, getErr := namespaces.Get(ctx, ns, v1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		if current.Annotations == nil {
			current.Annotations = map[string]string{}
		}
		current.Annotations[annotation] = isolatedName
		_, updateErr := namespaces.Update(ctx, current, v1.UpdateOptions{})
		return updateErr
	})
	dnsRecordPrerequisite(t, err == nil, "Associate test Namespace with isolated DNS configuration: %v", err)
	log.Info("Prepared DNSRecord fixture", "namespace", ns, "networkConfig", isolatedName, "zone", zonePath, "domain", domain)
	readinessErr := wait.PollUntilContextTimeout(ctx, time.Second, 2*defaultTimeout, true, func(ctx context.Context) (bool, error) {
		ni, getErr := testData.crdClientset.CrdV1alpha1().NetworkInfos(ns).Get(ctx, ns, v1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			return false, nil
		}
		if getErr != nil {
			return false, getErr
		}
		for _, allowed := range ni.AllowedDNSRecordDomains {
			if allowed == domain && len(ni.VPCs) > 0 {
				return true, nil
			}
		}
		return false, nil
	})
	dnsRecordPrerequisite(t, readinessErr == nil, "Namespace %s did not publish DNS domain %s in NetworkInfo (check operator DNS support, RBAC, NSX access and namespace conditions): %v", ns, domain, readinessErr)
	return domain
}

func assertDNSRecordCleanup(t *testing.T, err error, operation string) bool {
	t.Helper()
	if err != nil {
		if t.Skipped() {
			log.Info("DNSRecord skipped-test cleanup incomplete; retaining dependent fixture resources", "operation", operation, "error", err)
			t.Logf("WARNING: DNSRecord skipped-test cleanup incomplete: %s: %v", operation, err)
		} else {
			t.Errorf("DNSRecord fixture cleanup: %s: %v", operation, err)
		}
		return false
	}
	return true
}

func checkDNSRecordSchemas(t *testing.T) {
	t.Helper()
	for resource, field := range map[string][]string{
		"dnsrecords":               {"status", "fqdn"},
		"networkinfos":             {"allowedDNSRecordDomains"},
		"vpcnetworkconfigurations": {"spec", "dnsZones"},
	} {
		raw, err := testData.clientset.Discovery().RESTClient().Get().AbsPath("/apis/apiextensions.k8s.io/v1/customresourcedefinitions/" + resource + ".crd.nsx.vmware.com").Do(context.Background()).Raw()
		dnsRecordPrerequisite(t, err == nil, "Cannot inspect %s CRD: %v", resource, err)
		expectedType := "array"
		if resource == "dnsrecords" {
			expectedType = "string"
		}
		found, schemaErr := dnsRecordSchemaSupportsField(raw, field, expectedType)
		dnsRecordPrerequisite(t, schemaErr == nil, "Cannot decode %s CRD: %v", resource, schemaErr)
		dnsRecordPrerequisite(t, found, "%s CRD does not serve v1alpha1 field %s; deploy CRDs matching the operator", resource, strings.Join(field, "."))
	}
}

func listDNSRecordPrerequisitePaths(path string) ([]string, error) {
	paths := []string{}
	cursor := ""
	seen := map[string]bool{}
	for {
		endpoint := PolicyAPI + path + "?page_size=1000"
		if cursor != "" {
			endpoint += "&cursor=" + url.QueryEscape(cursor)
		}
		page, err := testData.nsxClient.Cluster.HttpGet(endpoint)
		if err != nil {
			return nil, err
		}
		rows, ok := page["results"].([]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid list response for %s", path)
		}
		for _, row := range rows {
			object, ok := row.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("invalid result for %s", path)
			}
			if deleted, _ := object["marked_for_delete"].(bool); deleted {
				continue
			}
			candidate, _ := object["path"].(string)
			if candidate != "" {
				paths = append(paths, candidate)
			}
		}
		cursor, _ = page["cursor"].(string)
		if cursor == "" {
			break
		}
		if seen[cursor] {
			return nil, fmt.Errorf("repeated cursor for %s", path)
		}
		seen[cursor] = true
	}
	sort.Strings(paths)
	return paths, nil
}
