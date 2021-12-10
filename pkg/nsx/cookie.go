/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package nsx

import (
	"net/http"
	"net/url"
	"sync"
)

// Jar holds cookie from different host
type Jar struct {
	sync.RWMutex
	jar map[string][]*http.Cookie
}

// NewJar creates a jar
func NewJar() *Jar {
	j := Jar{}
	j.jar = make(map[string][]*http.Cookie)
	return &j
}

// SetCookies sets cookies of an url
func (jar *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	jar.Lock()
	jar.jar[u.Host] = cookies
	jar.Unlock()
}

// Cookies returns cookies of an url
func (jar *Jar) Cookies(u *url.URL) []*http.Cookie {
	jar.RLock()
	defer jar.RUnlock()
	return jar.jar[u.Host]
}
