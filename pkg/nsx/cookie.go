// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

package nsx

import (
	"net/http"
	"net/url"
	"sync"
)

type jar struct {
	sync.RWMutex
	jar map[string][]*http.Cookie
}

func newJar() *jar {
	j := jar{}
	j.jar = make(map[string][]*http.Cookie)
	return &j
}

func (jar *jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	jar.Lock()
	jar.jar[u.Host] = cookies
	jar.Unlock()
}

func (jar *jar) Cookies(u *url.URL) []*http.Cookie {
	jar.RLock()
	defer jar.RUnlock()
	return jar.jar[u.Host]
}
