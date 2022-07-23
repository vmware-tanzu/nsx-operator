/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewJar(t *testing.T) {
	j := Jar{}
	j.jar = make(map[string][]*http.Cookie)
	tests := []struct {
		name string
		want *Jar
	}{
		{"1", &j},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewJar(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewJar() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJar_Cookies(t *testing.T) {
	url2 := &url.URL{Host: "test"}
	j := NewJar()
	j.SetCookies(url2, []*http.Cookie{{}})
	assert.NotNil(t, j.Cookies(url2))
}
