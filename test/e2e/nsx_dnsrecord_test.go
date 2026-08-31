package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	servicecommon "github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestDNSRecord(t *testing.T) {
	TrackTest(t)
	StartParallel(t)

	// Clean up namespace when all DNSRecord tests complete
	t.Cleanup(func() { CleanupVCNamespaces(NsDNSRecord) })

	// Use pre-created namespace
	ns := NsDNSRecord

	// ParallelTests: These tests use independent DNSRecord resources and can run concurrently
	RunSubtest(t, "ParallelTests", func(t *testing.T) {
		RunSubtest(t, "testDNSRecordTypeA", func(t *testing.T) {
			StartParallel(t)
			testDNSRecordTypeA(t, ns)
		})
		RunSubtest(t, "testDNSRecordTypeCNAME", func(t *testing.T) {
			StartParallel(t)
			testDNSRecordTypeCNAME(t, ns)
		})
		RunSubtest(t, "testDNSRecordApexRecord", func(t *testing.T) {
			StartParallel(t)
			testDNSRecordApexRecord(t, ns)
		})
	})
}

func testDNSRecordTypeA(t *testing.T, ns string) {
	yamlPath, _ := filepath.Abs("./manifest/testDNSRecord/dnsrecord_a.yaml")
	require.NoError(t, applyYAML(yamlPath, ns))
	defer deleteYAML(yamlPath, ns)

	recordName := "e2e-dnsrecord-a"
	dnsRecord := waitForDNSRecordCRReady(t, ns, recordName)

	assert.Equal(t, "e2e-a.example.com", dnsRecord.Spec.FQDN)
	assert.Contains(t, dnsRecord.Finalizers, servicecommon.DNSRecordFinalizerName)
	assert.Equal(t, v1alpha1.DNSRecordTypeA, dnsRecord.Spec.RecordType)
	assert.Equal(t, []string{"10.0.0.100"}, dnsRecord.Spec.RecordValues)
}

func testDNSRecordTypeCNAME(t *testing.T, ns string) {
	yamlPath, _ := filepath.Abs("./manifest/testDNSRecord/dnsrecord_cname.yaml")
	require.NoError(t, applyYAML(yamlPath, ns))
	defer deleteYAML(yamlPath, ns)

	recordName := "e2e-dnsrecord-cname"
	dnsRecord := waitForDNSRecordCRReady(t, ns, recordName)

	assert.Equal(t, "e2e-cname.example.com", dnsRecord.Spec.FQDN)
	assert.Contains(t, dnsRecord.Finalizers, servicecommon.DNSRecordFinalizerName)
	assert.Equal(t, v1alpha1.DNSRecordTypeCNAME, dnsRecord.Spec.RecordType)

	// Update TTL and verify
	newTTL := int32(1200)
	dnsRecord.Spec.TTL = &newTTL
	updatedRecord, err := testData.crdClientset.CrdV1alpha1().DNSRecords(ns).Update(context.TODO(), dnsRecord, v1.UpdateOptions{})
	require.NoError(t, err)
	assert.Equal(t, int32(1200), *updatedRecord.Spec.TTL)
}

func testDNSRecordApexRecord(t *testing.T, ns string) {
	recordName := fmt.Sprintf("apex-dns-%s", getRandomString())
	ttl := int32(300)
	apexRecord := &v1alpha1.DNSRecord{
		ObjectMeta: v1.ObjectMeta{
			Name:      recordName,
			Namespace: ns,
		},
		Spec: v1alpha1.DNSRecordSpec{
			DomainName:   "example.com",
			RecordName:   "@",
			RecordType:   v1alpha1.DNSRecordTypeA,
			RecordValues: []string{"10.0.0.101"},
			TTL:          &ttl,
		},
	}

	_, err := testData.crdClientset.CrdV1alpha1().DNSRecords(ns).Create(context.TODO(), apexRecord, v1.CreateOptions{})
	require.NoError(t, err)
	defer func() {
		_ = testData.crdClientset.CrdV1alpha1().DNSRecords(ns).Delete(context.TODO(), recordName, v1.DeleteOptions{})
	}()

	dnsRecord := waitForDNSRecordCRReady(t, ns, recordName)
	assert.Equal(t, "example.com", dnsRecord.Spec.FQDN)
	assert.Contains(t, dnsRecord.Finalizers, servicecommon.DNSRecordFinalizerName)
}

func waitForDNSRecordCRReady(t *testing.T, ns, dnsRecordName string) *v1alpha1.DNSRecord {
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
		for _, con := range res.Status.Conditions {
			if con.Type == string(v1alpha1.Ready) && con.Status == v1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err)
	return res
}
