/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSha1(t *testing.T) {
	assert.Equal(t, Sha1("name"), "6ae999552a0d2dca14d62e2bc8b764d377b1dd6c")
}

func TestNormalizeName(t *testing.T) {
	shortName := strings.Repeat("a", 256)
	assert.Equal(t, NormalizeName(shortName), shortName)
	longName := strings.Repeat("a", 257)
	assert.Equal(t, NormalizeName(longName), fmt.Sprintf("%s-%s", strings.Repeat("a", 256-HashLength-1), "0c103888"))
}

func TestNormalizeLabelKey(t *testing.T) {
	shortKey := strings.Repeat("a", 128)
	assert.Equal(t, NormalizeLabelKey(shortKey), shortKey)
	longKey := strings.Repeat("a", 129) + "/def"
	assert.Equal(t, NormalizeLabelKey(longKey), "def")
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
				"def": NormalizeName(longValue),
			},
		},
		{
			name: "labels-with-short-key",
			inputLabels: &map[string]string{
				shortKey: longValue,
			},
			expectedLabels: &map[string]string{
				shortKey: NormalizeName(longValue),
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

	isCRInSysNs, err := IsSystemNamespace(client, ns.Namespace, nil)
	if err != nil {
		t.Fatalf(err.Error())
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

	isCRInSysNs, err = IsSystemNamespace(client, ns.Namespace, nil)

	if err != nil {
		t.Fatalf(err.Error())
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
			if got := GenerateDisplayName(tt.args.res_name, tt.args.prefix, tt.args.suffix, tt.args.project, tt.args.cluster); got != tt.want {
				t.Errorf("GenerateDisplayName() = %v, want %v", got, tt.want)
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
			want: "sp-1234-456",
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
			want: "sp-1234-456-scope",
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
			want: "sp-1234-456-test-scope",
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
			want: "k8scl-one-1234-456-scope",
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
			want: "sr-k8scl-one-1234-456-ssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssssss-813dffe8-scope",
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

func Test_rangesAbstractRange(t *testing.T) {
	ranges1 := [][]net.IP{
		{
			net.ParseIP("172.0.0.1"),
			net.ParseIP("172.0.255.255"),
		},
		{
			net.ParseIP("172.2.0.1"),
			net.ParseIP("172.2.255.255"),
		},
	}
	except1 := []net.IP{
		net.ParseIP("172.0.100.1"),
		net.ParseIP("172.0.100.255"),
	}
	want1 := [][]net.IP{
		{
			net.ParseIP("172.0.0.1").To4(),
			net.ParseIP("172.0.100.0").To4(),
		},
		{
			net.ParseIP("172.0.101.0").To4(),
			net.ParseIP("172.0.255.255").To4(),
		},
		{
			net.ParseIP("172.2.0.1").To4(),
			net.ParseIP("172.2.255.255").To4(),
		},
	}
	ranges2 := [][]net.IP{
		{
			net.ParseIP("172.0.0.1"),
			net.ParseIP("172.0.255.255"),
		},
		{
			net.ParseIP("172.2.0.1"),
			net.ParseIP("172.2.255.255"),
		},
	}
	except2 := []net.IP{
		net.ParseIP("172.0.100.1"),
		net.ParseIP("172.2.100.255"),
	}
	want2 := [][]net.IP{
		{
			net.ParseIP("172.0.0.1").To4(),
			net.ParseIP("172.0.100.0").To4(),
		},
		{
			net.ParseIP("172.2.101.0").To4(),
			net.ParseIP("172.2.255.255").To4(),
		},
	}
	type args struct {
		ranges [][]net.IP
		except []net.IP
	}
	tests := []struct {
		name string
		args args
		want [][]net.IP
	}{
		{"1", args{ranges1, except1}, want1},
		{"2", args{ranges2, except2}, want2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rangesAbstractRange(tt.args.ranges, tt.args.except)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s failed: rangesAbstractRange got %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestGetCIDRRangesWithExcept(t *testing.T) {
	cidr1 := "172.17.0.0/16"
	excepts1 := []string{"172.17.1.0/24"}
	want1 := []string{"172.17.0.0-172.17.0.255", "172.17.2.0-172.17.255.255"}
	cidr2 := "172.0.0.0/16"
	excepts2 := []string{"172.0.100.0/24", "172.0.102.0/24"}
	want2 := []string{"172.0.0.0-172.0.99.255", "172.0.101.0-172.0.101.255", "172.0.103.0-172.0.255.255"}
	type args struct {
		cidr    string
		excepts []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{"1", args{cidr1, excepts1}, want1},
		{"2", args{cidr2, excepts2}, want2},
	}
	for _, tt := range tests {
		got, err := GetCIDRRangesWithExcept(tt.args.cidr, tt.args.excepts)
		if err != nil {
			t.Errorf("%s failed: %s", tt.name, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s failed: GetCIDRRangesWithExcept got %s, want %s", tt.name, got, tt.want)
		}
	}
}

func Test_calculateOffsetIP(t *testing.T) {
	ip := net.ParseIP("192.168.0.1")
	offset1 := 1
	want1 := net.ParseIP("192.168.0.2").To4()
	type args struct {
		ip     net.IP
		offset int
	}
	tests := []struct {
		name string
		args args
		want net.IP
	}{{"1", args{ip, offset1}, want1}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := calculateOffsetIP(tt.args.ip, tt.args.offset)
			if err != nil {
				t.Errorf("%s failed: %s", tt.name, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s failed: calculateOffsetIP got %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestGenerateIDByObject(t *testing.T) {
	for _, tc := range []struct {
		name  string
		obj   metav1.Object
		limit int
		expID string
	}{
		{
			name:  "no limit set",
			obj:   &metav1.ObjectMeta{Name: "abcdefg", UID: "b720ee2c-5788-4680-9796-0f93db33d8a9"},
			limit: 0,
			expID: "abcdefg-b720ee2c-5788-4680-9796-0f93db33d8a9",
		},
		{
			name:  "truncate with hash on uid",
			obj:   &metav1.ObjectMeta{Name: "abcdefg", UID: "b720ee2c-5788-4680-9796-0f93db33d8a9"},
			limit: 20,
			expID: "abcdefg-df78acb2",
		},
		{
			name:  "longer name with truncate",
			obj:   &metav1.ObjectMeta{Name: strings.Repeat("a", 256), UID: "b720ee2c-5788-4680-9796-0f93db33d8a9"},
			limit: 0,
			expID: fmt.Sprintf("%s-df78acb2", strings.Repeat("a", 246)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var id string
			if tc.limit == 0 {
				id = GenerateIDByObject(tc.obj)
			} else {
				id = GenerateIDByObjectByLimit(tc.obj, tc.limit)
			}
			assert.Equal(t, tc.expID, id)
		})
	}
}

func TestGenerateIDByObjectWithSuffix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		obj    metav1.Object
		limit  int
		suffix string
		expID  string
	}{
		{
			name:   "no limit set",
			obj:    &metav1.ObjectMeta{Name: "abcdefg", UID: "b720ee2c-5788-4680-9796-0f93db33d8a9"},
			limit:  0,
			suffix: "2",
			expID:  "abcdefg-b720ee2c-5788-4680-9796-0f93db33d8a9_2",
		},
		{
			name:   "longer name with truncate",
			obj:    &metav1.ObjectMeta{Name: strings.Repeat("a", 256), UID: "b720ee2c-5788-4680-9796-0f93db33d8a9"},
			limit:  0,
			suffix: "28e85c0b-21e4-4cab-b1c3-597639dfe752",
			expID:  fmt.Sprintf("%s-df78acb2_28e85c0b-21e4-4cab-b1c3-597639dfe752", strings.Repeat("a", 209)),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := GenerateIDByObjectWithSuffix(tc.obj, tc.suffix)
			assert.Equal(t, tc.expID, id)
		})
	}
}
