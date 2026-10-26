package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
)

func TestDNSRecord(t *testing.T) {
	TrackTest(t)
	StartParallel(t)

	// Clean up namespace when all DNSRecord tests complete
	t.Cleanup(func() { CleanupVCNamespaces(NsDNSRecord) })

	// Use pre-created namespace
	ns := NsDNSRecord
	networkInfo := getNetworkInfoWithCondition(t, ns, ns, func(ni *v1alpha1.NetworkInfo) (bool, error) {
		return len(ni.VPCs) > 0, nil
	})
	require.NotEmpty(t, networkInfo.AllowedDNSRecordDomains,
		"DNSRecord E2E requires a DNS zone in the namespace's VPCNetworkConfiguration")
	domain := strings.TrimSuffix(networkInfo.AllowedDNSRecordDomains[0], ".")

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
