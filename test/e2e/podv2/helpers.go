// Copyright © 2026 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

// Package podv2 contains pure helpers shared by the isolated Pod v2 e2e suite.
package podv2

import (
	"bytes"
	"fmt"
	"net"
	"sort"
	"strings"

	"gopkg.in/ini.v1"
)

// Config changes only the feature switches used by the suite. Callers keep
// the original bytes for rollback; reserializing INI is not a rollback.
func Config(original []byte, v2, enhance, restore, vif bool) ([]byte, error) {
	f, err := ini.Load(original)
	if err != nil {
		return nil, err
	}
	for key, value := range map[string]bool{"pod_v2": v2, "vpc_wcp_enhance": enhance, "restore_vif": vif} {
		f.Section("nsx_v3").Key(key).SetValue(fmt.Sprint(value))
	}
	f.Section("k8s").Key("enable_restore").SetValue(fmt.Sprint(restore))
	var b bytes.Buffer
	_, err = f.WriteTo(&b)
	return b.Bytes(), err
}

// PortPath refuses anything except a complete policy VPC port path.
func PortPath(path string) ([]string, error) {
	p := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(p) != 10 {
		return nil, fmt.Errorf("invalid VPC port path %q", path)
	}
	for i, key := range []string{"orgs", "projects", "vpcs", "subnets", "ports"} {
		if p[2*i] != key || p[2*i+1] == "" || p[2*i+1] == "." || p[2*i+1] == ".." {
			return nil, fmt.Errorf("invalid VPC port path %q", path)
		}
	}
	return []string{p[1], p[3], p[5], p[7], p[9]}, nil
}

// IPs normalizes addresses from Pod status and CR CIDRs for comparison.
func IPs(values []string) ([]string, error) {
	var out []string
	for _, value := range values {
		if value == "" {
			continue
		}
		ip := net.ParseIP(strings.SplitN(value, "/", 2)[0])
		if ip == nil {
			return nil, fmt.Errorf("invalid IP %q", value)
		}
		out = append(out, ip.String())
	}
	sort.Strings(out)
	return out, nil
}
