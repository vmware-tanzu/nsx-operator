/* Copyright © 2025 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

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

func TestConnectStrings(t *testing.T) {
	string1 := "aa"
	string2 := "bb"
	connectString := connectStrings(ConnectorUnderline, string1, string2)
	expString := "aa" + ConnectorUnderline + "bb"
	assert.Equal(t, connectString, expString)

	connectString = connectStrings("-", string1, string2)
	expString = "aa" + "-" + "bb"
	assert.Equal(t, connectString, expString)

	int1 := 11
	int2 := 22
	connectString = connectStrings(ConnectorUnderline, strconv.Itoa(int1), strconv.Itoa(int2))
	expString = "11" + ConnectorUnderline + "22"
	expString = fmt.Sprintf("%d%s%d", int1, ConnectorUnderline, int2)
	assert.Equal(t, connectString, expString)

	connectString = connectStrings(ConnectorUnderline, string1, strconv.Itoa(int2))
	expString = "aa" + ConnectorUnderline + "22"
	expString = fmt.Sprintf("%s%s%d", string1, ConnectorUnderline, int2)
	assert.Equal(t, connectString, expString)
}
