/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
)

func TestSha1(t *testing.T) {
	assert.Equal(t, Sha1("name"), "6ae999552a0d2dca14d62e2bc8b764d377b1dd6c")
}

func TestNormalizeName(t *testing.T) {
	shortName := strings.Repeat("a", 256)
	assert.Equal(t, NormalizeLabelValue(shortName, truncateLabelHash), shortName)
	longName := strings.Repeat("a", 257)
	assert.Equal(t, NormalizeLabelValue(longName, truncateLabelHash), fmt.Sprintf("%s_%s", strings.Repeat("a", 256-common.HashLength-1), "0c103888"))
}

func TestNormalizeLabelKey(t *testing.T) {
	shortKey := strings.Repeat("a", 128)
	assert.Equal(t, NormalizeLabelKey(shortKey, truncateLabelHash), shortKey)
	longKey := strings.Repeat("a", 129) + "/def"
	assert.Equal(t, NormalizeLabelKey(longKey, truncateLabelHash), "def")
}

func TestNormalizeLabels(t *testing.T) {
	shortKey := strings.Repeat("a", 128)
	longKey := strings.Repeat("a", 129) + "/def"
	longValue := strings.Repeat("v", 257)
	tests := []struct {
		name           string
		inputLabels    *map[string]string
		expectedLabels *map[string]string
	}{
		{
			name: "labels-with-long-key-including-multiple-layers",
			inputLabels: &map[string]string{
				longKey: longValue,
			},
			expectedLabels: &map[string]string{
				"def": NormalizeLabelValue(longValue, truncateLabelHash),
			},
		},
		{
			name: "labels-with-short-key",
			inputLabels: &map[string]string{
				shortKey: longValue,
			},
			expectedLabels: &map[string]string{
				shortKey: NormalizeLabelValue(longValue, truncateLabelHash),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedLabels, NormalizeLabels(tt.inputLabels))
		})
	}
}

func TestUtil_IsNsInSystemNamespace(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	ctx := context.TODO()
	dummyNs := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "dummy"}}
	client.Create(ctx, dummyNs)
	ns := types.NamespacedName{Namespace: "dummy", Name: "dummy"}

	isCRInSysNs, err := IsSystemNamespace(client, ns.Namespace, nil, false)
	if err != nil {
		t.Fatalf("%s", err.Error())
	}
	if isCRInSysNs {
		t.Fatalf("Non-system namespace identied as a system namespace")
	}
	client.Delete(ctx, dummyNs)

	sysNs := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "sys-ns",
			Namespace:   "sys-ns",
			Annotations: map[string]string{"vmware-system-shared-t1": "true"},
		},
	}
	client.Create(ctx, sysNs)
	ns = types.NamespacedName{Namespace: "sys-ns", Name: "dummy"}

	isCRInSysNs, err = IsSystemNamespace(client, ns.Namespace, nil, false)
	if err != nil {
		t.Fatalf("%s", err.Error())
	}
	if !isCRInSysNs {
		t.Fatalf("System namespace not identied as a system namespace")
	}
	client.Delete(ctx, sysNs)
}

func Test_CheckPodHasNamedPort(t *testing.T) {
	pod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-namespace",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Ports: []v1.ContainerPort{
						{Name: "test-port", ContainerPort: 8080},
					},
				},
			},
		},
	}
	pod3 := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-3",
			Namespace: "test",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Ports: []v1.ContainerPort{},
				},
			},
		},
	}
	type args struct {
		pod *v1.Pod
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{"1", args{&pod}, true},
		{"3", args{&pod3}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, CheckPodHasNamedPort(*tt.args.pod, ""), "checkPodHasNamedPort(%v)", tt.args.pod)
		})
	}
}

func TestToUpper(t *testing.T) {
	type args struct {
		obj interface{}
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"1", args{"test"}, "TEST"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, ToUpper(tt.args.obj), "ToUpper(%v)", tt.args.obj)
		})
	}
}

func TestContains(t *testing.T) {
	type args struct {
		s   []string
		str string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{"1", args{[]string{"test", "test2"}, "test"}, true},
		{"2", args{[]string{"test2", "test3"}, "test"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, Contains(tt.args.s, tt.args.str), "Contains(%v, %v)", tt.args.s, tt.args.str)
			assert.Equal(t, tt.want, Contains(tt.args.s, tt.args.str), "Contains(%v, %v)", tt.args.s, tt.args.str)
		})
	}
}

func TestRemoveDuplicateStr(t *testing.T) {
	type args struct {
		strSlice []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{"1", args{[]string{"test", "test", "test"}}, []string{"test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, RemoveDuplicateStr(tt.args.strSlice), "RemoveDuplicateStr(%v)", tt.args.strSlice)
		})
	}
}

func TestCalculateSubnetSize(t *testing.T) {
	type args struct {
		mask int
	}
	tests := []struct {
		name string
		args args
		want int64
	}{
		{"1", args{24}, 256},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, CalculateSubnetSize(tt.args.mask), "CalculateSubnetSize(%v)", tt.args.mask)
		})
	}
}

func TestNormalizeId(t *testing.T) {
	type args struct {
		name string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "1",
			args: args{
				name: "k8scl-one-test",
			},
			want: "k8scl-one-test",
		},
		{
			name: "2",
			args: args{
				name: "k8scl-one:test",
			},
			want: "k8scl-one_test",
		},
		{
			name: "3",
			args: args{
				name: "k8scl-one:1234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890",
			},
			want: "k8scl-one_12345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456-e8ad9afc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeId(tt.args.name); got != tt.want {
				t.Errorf("NormalizeId() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateID(t *testing.T) {
	type args struct {
		res_id string
		prefix string
		suffix string
		index  string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "test-1",
			args: args{
				res_id: "1234-456",
				prefix: "sp",
				suffix: "",
				index:  "",
			},
			want: "sp_1234-456",
		},
		{
			name: "test-subfix",
			args: args{
				res_id: "1234-456",
				prefix: "sp",
				suffix: "scope",
				index:  "",
			},
			want: "sp_1234-456_scope",
		},
		{
			name: "test-index",
			args: args{
				res_id: "1234-456",
				prefix: "sp",
				suffix: "scope",
				index:  "4",
			},
			want: "sp_1234-456_4_scope",
		},
		{
			name: "test-scope",
			args: args{
				res_id: "1234-456",
				prefix: "",
				suffix: "scope",
				index:  "",
			},
			want: "1234-456_scope",
		},
		{
			name: "test-complex-index",
			args: args{
				res_id: "1234-456",
				prefix: "",
				suffix: "scope",
				index:  "6_7",
			},
			want: "1234-456_6_7_scope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GenerateID(tt.args.res_id, tt.args.prefix, tt.args.suffix, tt.args.index); got != tt.want {
				t.Errorf("GenerateID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateDisplayName(t *testing.T) {
	type args struct {
		res_name string
		prefix   string
		suffix   string
		project  string
		cluster  string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "test-1",
			args: args{
				res_name: "1234-456",
				prefix:   "sp",
				suffix:   "",
				project:  "",
			},
			want: "sp-1234-456",
		},
		{
			name: "test-suffix",
			args: args{
				res_name: "1234-456",
				prefix:   "sp",
				suffix:   "scope",
				project:  "",
			},
			want: "sp-1234-456-scope",
		},
		{
			name: "test-index",
			args: args{
				res_name: "1234-456",
				prefix:   "sp",
				suffix:   "scope",
				project:  "test",
			},
			want: "sp-1234-456-test-scope",
		},
		{
			name: "test-cluster",
			args: args{
				res_name: "1234-456",
				prefix:   "",
				suffix:   "scope",
				project:  "",
				cluster:  "k8scl-one",
			},
			want: "k8scl-one-1234-456-scope",
		},
		{
			name: "test-project-cluster",
			args: args{
				res_name: "1234-456",
				prefix:   "",
				suffix:   "scope",
				project:  "test",
				cluster:  "k8scl-one",
			},
			want: "k8scl-one-1234-456-test-scope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := generateDisplayName("-", tt.args.res_name, tt.args.prefix, tt.args.suffix, tt.args.project, tt.args.cluster); got != tt.want {
				t.Errorf("generateDisplayName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateTruncName(t *testing.T) {
	type args struct {
		limit    int
		res_name string
		prefix   string
		suffix   string
		project  string
		cluster  string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "test-1",
			args: args{
				limit:    255,
				res_name: "1234-456",
				prefix:   "sp",
				suffix:   "",
				project:  "",
			},
			want: "sp_1234-456",
		},
		{
			name: "test-only-name",
			args: args{
				limit:    255,
				res_name: "1234-456",
				prefix:   "",
				suffix:   "",
				project:  "",
			},
			want: "1234-456",
		},
		{
			name: "test-suffix",
			args: args{
				limit:    255,
				res_name: "1234-456",
				prefix:   "sp",
				suffix:   "scope",
				project:  "",
			},
			want: "sp_1234-456_scope",
		},
		{
			name: "test-index",
			args: args{
				limit:    255,
				res_name: "1234-456",
				prefix:   "sp",
				suffix:   "scope",
				project:  "test",
			},
			want: "sp_1234-456_test_scope",
		},
		{
			name: "test-cluster",
			args: args{
				limit:    255,
				res_name: "1234-456",
				prefix:   "",
				suffix:   "scope",
				project:  "",
				cluster:  "k8scl-one",
			},
			want: "k8scl-one_1234-456_scope",
		},
		{
			name: "test-project-cluster",
			args: args{
				limit:    255,
				res_name: "1234-456",
				prefix:   "sr",
				suffix:   "scope",
				project:  strings.Repeat("s", 300),
				cluster:  "k8scl-one",
			},
			want: "sr_k8scl-one_1234-456_sssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssss_r65nx_scope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GenerateTruncName(tt.args.limit, tt.args.res_name, tt.args.prefix, tt.args.suffix, tt.args.project, tt.args.cluster); got != tt.want {
				t.Errorf("GenerateTruncName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateIDByObject(t *testing.T) {
	for _, tc := range []struct {
		name  string
		obj   metav1.Object
		expID string
	}{
		{
			name:  "no limit set",
			obj:   &metav1.ObjectMeta{Name: "abcdefg", UID: "b720ee2c-5788-4680-9796-0f93db33d8a9"},
			expID: "abcdefg_q3qpx",
		},
		{
			name:  "longer name with truncate",
			obj:   &metav1.ObjectMeta{Name: strings.Repeat("a", 256), UID: "b720ee2c-5788-4680-9796-0f93db33d8a9"},
			expID: fmt.Sprintf("%s_q3qpx", strings.Repeat("a", 249)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := GenerateIDByObject(tc.obj)
			assert.Equal(t, tc.expID, id)
		})
	}
}

func TestConnectStrings(t *testing.T) {
	string1 := "aa"
	string2 := "bb"
	connectString := connectStrings(common.ConnectorUnderline, string1, string2)
	expString := "aa" + common.ConnectorUnderline + "bb"
	assert.Equal(t, connectString, expString)

	connectString = connectStrings("-", string1, string2)
	expString = "aa" + "-" + "bb"
	assert.Equal(t, connectString, expString)

	int1 := 11
	int2 := 22
	connectString = connectStrings(common.ConnectorUnderline, strconv.Itoa(int1), strconv.Itoa(int2))
	expString = "11" + common.ConnectorUnderline + "22"
	expString = fmt.Sprintf("%d%s%d", int1, common.ConnectorUnderline, int2)
	assert.Equal(t, connectString, expString)

	connectString = connectStrings(common.ConnectorUnderline, string1, strconv.Itoa(int2))
	expString = "aa" + common.ConnectorUnderline + "22"
	expString = fmt.Sprintf("%s%s%d", string1, common.ConnectorUnderline, int2)
	assert.Equal(t, connectString, expString)
}

func TestNewSha1(t *testing.T) {
	assert.Equal(t, "chl6tk4k3f8cb0c1lfpdlfjtsyfuess", Sha1WithCustomizedCharset("name"))
	assert.Equal(t, "eqbb380p8jcm2zjaxwy0dmvb4hyevkw", Sha1WithCustomizedCharset("namee"))

	allowedChars := sets.New[rune]()
	for _, c := range HashCharset {
		allowedChars.Insert(c)
	}
	randUID, err := uuid.NewRandom()
	require.NoError(t, err)
	hashString := Sha1WithCustomizedCharset(randUID.String())
	// Verify all chars in the hash string are contained in base62Chars.
	for _, c := range hashString {
		assert.True(t, allowedChars.Has(c))
	}
}

func TestCollisionWithHashCharset(t *testing.T) {
	hashLength := 5
	newUUID, err := uuid.NewRandom()
	require.NoError(t, err)

	hashStr := Sha1WithCustomizedCharset(newUUID.String())[:hashLength]
	timestamp := time.Now().UnixMilli()
	hashStrWithTime := Sha1WithCustomizedCharset(fmt.Sprintf("%s-%d", newUUID.String(), timestamp))[:hashLength]
	require.NotEqual(t, hashStr, hashStrWithTime)
}

func TestGetClusterUUID(t *testing.T) {
	// Test that a new UUID is generated for a given clusterID
	clusterID := "test-cluster"
	uuid1 := GetClusterUUID(clusterID)
	assert.NotEqual(t, uuid.Nil, uuid1, "UUID should not be nil after generation")

	// Test that calling again returns the same UUID (cached)
	uuid2 := GetClusterUUID("another-cluster")
	assert.NotEqual(t, uuid1, uuid2, "UUID should be changed for different input")

	// Test that the UUID is deterministic for the same clusterID if cache is reset
	uuid3 := GetClusterUUID(clusterID)
	assert.Equal(t, uuid1, uuid3, "UUID should be deterministic for the same clusterID")
}

func TestValidateSubnetSize(t *testing.T) {
	assert := assert.New(t)
	nsxClient := &nsx.Client{}
	cluster, _ := nsx.NewCluster(&nsx.Config{})
	nsxClient.Cluster = cluster
	tests := []struct {
		name             string
		featureSupported bool
		subnetSize       int
		wantOK           bool
		wantMsgContains  string
	}{
		{
			name:             "zero size allowed",
			featureSupported: false,
			subnetSize:       0,
			wantOK:           true,
		},
		{
			name:             "not power of two",
			featureSupported: false,
			subnetSize:       3,
			wantOK:           false,
			wantMsgContains:  "Subnet size must be a power of 2",
		},
		{
			name:             "nsx 9.0.0 too small",
			featureSupported: false,
			subnetSize:       8, // less than MinSubnetSizeV90 (16)
			wantOK:           false,
			wantMsgContains:  fmt.Sprintf("%d", MinSubnetSizeV90),
		},
		{
			name:             "nsx 9.0.0 ok at boundary",
			featureSupported: false,
			subnetSize:       MinSubnetSizeV90,
			wantOK:           true,
		},
		{
			name:             "nsx 9.0.0 with suffix still treated as 9.0.0",
			featureSupported: false,
			subnetSize:       8,
			wantOK:           false,
			wantMsgContains:  fmt.Sprintf("%d", MinSubnetSizeV90),
		},
		{
			name:             "other nsx versions too small",
			featureSupported: true,
			subnetSize:       4, // less than MinSubnetSizeV91 (8)
			wantOK:           false,
			wantMsgContains:  fmt.Sprintf("%d", MinSubnetSizeV91),
		},

		{
			name:             "non power of two takes precedence over size limit",
			featureSupported: true,
			subnetSize:       12, // not power of two and also < 16 but main check is power-of-two
			wantOK:           false,
			wantMsgContains:  "Subnet size must be a power of 2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			patches := gomonkey.ApplyMethod(reflect.TypeOf(nsxClient), "NSXCheckVersion", func(_ *nsx.Client, _ int) bool {
				return tc.featureSupported
			})
			defer patches.Reset()
			ok, msg := ValidateSubnetSize(nsxClient, tc.subnetSize)
			assert.Equal(tc.wantOK, ok, "ok mismatch for %s", tc.name)
			if tc.wantMsgContains == "" {
				assert.Equal("", msg, "expected empty message for %s", tc.name)
			} else {
				assert.Contains(msg, tc.wantMsgContains, "message content mismatch for %s", tc.name)
			}
		})
	}
}

func TestBuildBasicTagsWithStatefulSetPod(t *testing.T) {
	cluster := "test-cluster"
	namespaceID := types.UID("ns-uid-123")

	controller := true
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-0",
			Namespace: "default",
			UID:       "pod-uid-123",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       appsv1.SchemeGroupVersion.WithKind("StatefulSet").Kind,
					Name:       "test-sts",
					UID:        "sts-uid-123",
					Controller: &controller,
				},
			},
		},
	}

	tags := BuildBasicTags(cluster, pod, namespaceID)

	assert.Len(t, tags, 8)
	foundCluster := false
	foundVersion := false
	foundNamespace := false
	foundPodName := false
	foundPodUID := false
	foundStsName := false
	foundStsUID := false

	for _, tag := range tags {
		if *tag.Scope == common.TagScopeCluster && *tag.Tag == cluster {
			foundCluster = true
		}
		if *tag.Scope == common.TagScopeVersion {
			foundVersion = true
		}
		if *tag.Scope == common.TagScopeNamespace && *tag.Tag == "default" {
			foundNamespace = true
		}
		if *tag.Scope == common.TagScopePodName && *tag.Tag == "test-pod-0" {
			foundPodName = true
		}
		if *tag.Scope == common.TagScopePodUID && *tag.Tag == "pod-uid-123" {
			foundPodUID = true
		}
		if *tag.Scope == common.TagScopeStatefulSetName && *tag.Tag == "test-sts" {
			foundStsName = true
		}
		if *tag.Scope == common.TagScopeStatefulSetUID && *tag.Tag == "sts-uid-123" {
			foundStsUID = true
		}
	}

	assert.True(t, foundCluster, "should have cluster tag")
	assert.True(t, foundVersion, "should have version tag")
	assert.True(t, foundNamespace, "should have namespace tag")
	assert.True(t, foundPodName, "should have pod name tag")
	assert.True(t, foundPodUID, "should have pod uid tag")
	assert.True(t, foundStsName, "should have statefulset name tag")
	assert.True(t, foundStsUID, "should have statefulset uid tag")
}

func boolPtr(b bool) *bool { return &b }

func TestCRSubnetStaticIPAllocationEnabled(t *testing.T) {
	tests := []struct {
		name string
		obj  client.Object
		want bool
	}{
		{"nil enabled on Subnet", &v1alpha1.Subnet{}, false},
		{"enabled=false on Subnet", &v1alpha1.Subnet{Spec: v1alpha1.SubnetSpec{AdvancedConfig: v1alpha1.SubnetAdvancedConfig{StaticIPAllocation: v1alpha1.StaticIPAllocation{Enabled: boolPtr(false)}}}}, false},
		{"enabled=true on Subnet", &v1alpha1.Subnet{Spec: v1alpha1.SubnetSpec{AdvancedConfig: v1alpha1.SubnetAdvancedConfig{StaticIPAllocation: v1alpha1.StaticIPAllocation{Enabled: boolPtr(true)}}}}, true},
		{"SubnetSet never has static enabled", &v1alpha1.SubnetSet{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, CRSubnetStaticIPAllocationEnabled(tc.obj))
		})
	}
}

func TestCRPoolRangesToNSX(t *testing.T) {
	tests := []struct {
		name string
		in   []v1alpha1.IPAddressRange
		want []string
	}{
		{"nil -> nil", nil, nil},
		{"empty -> nil", []v1alpha1.IPAddressRange{}, nil},
		{"single IP collapses", []v1alpha1.IPAddressRange{{Start: "1.2.3.4", End: "1.2.3.4"}}, []string{"1.2.3.4"}},
		{"range", []v1alpha1.IPAddressRange{{Start: "1.2.3.4", End: "1.2.3.10"}}, []string{"1.2.3.4-1.2.3.10"}},
		{"mixed", []v1alpha1.IPAddressRange{{Start: "1.2.3.4", End: "1.2.3.10"}, {Start: "2001:db8::1", End: "2001:db8::1"}}, []string{"1.2.3.4-1.2.3.10", "2001:db8::1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CRPoolRangesToNSX(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNSXPoolRangesToCR(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []v1alpha1.IPAddressRange
	}{
		{"nil -> nil", nil, nil},
		{"empty -> nil", []string{}, nil},
		{"single IP", []string{"1.2.3.4"}, []v1alpha1.IPAddressRange{{Start: "1.2.3.4", End: "1.2.3.4"}}},
		{"range", []string{"1.2.3.4-1.2.3.10"}, []v1alpha1.IPAddressRange{{Start: "1.2.3.4", End: "1.2.3.10"}}},
		{"IPv6 range", []string{"2001:db8::1-2001:db8::ff"}, []v1alpha1.IPAddressRange{{Start: "2001:db8::1", End: "2001:db8::ff"}}},
		{"malformed skipped", []string{"garbage", "1.2.3.4"}, []v1alpha1.IPAddressRange{{Start: "1.2.3.4", End: "1.2.3.4"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NSXPoolRangesToCR(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCRPoolRangesRoundTrip(t *testing.T) {
	orig := []v1alpha1.IPAddressRange{
		{Start: "1.2.3.4", End: "1.2.3.10"},
		{Start: "2001:db8::1", End: "2001:db8::1"},
	}
	round := NSXPoolRangesToCR(CRPoolRangesToNSX(orig))
	assert.Equal(t, orig, round)
}

func TestParseIPRange(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantErr   bool
		wantStart string
		wantEnd   string
	}{
		{"single IPv4", "1.2.3.4", false, "1.2.3.4", "1.2.3.4"},
		{"IPv4 range", "1.2.3.4-1.2.3.10", false, "1.2.3.4", "1.2.3.10"},
		{"IPv4 range with spaces", " 1.2.3.4 - 1.2.3.10 ", false, "1.2.3.4", "1.2.3.10"},
		{"single IPv6", "2001:db8::1", false, "2001:db8::1", "2001:db8::1"},
		{"IPv6 range", "2001:db8::1-2001:db8::ff", false, "2001:db8::1", "2001:db8::ff"},
		{"empty", "", true, "", ""},
		{"invalid start", "garbage-1.2.3.4", true, "", ""},
		{"invalid end", "1.2.3.4-garbage", true, "", ""},
		{"mixed family", "1.2.3.4-2001:db8::1", true, "", ""},
		{"end < start", "1.2.3.10-1.2.3.4", true, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, end, err := ParseIPRange(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.wantStart, start.String())
			assert.Equal(t, tc.wantEnd, end.String())
		})
	}
}
