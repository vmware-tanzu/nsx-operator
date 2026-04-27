/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package source

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestAdmissionHostnameFiltersForRouteParent_gatewayAllSections(t *testing.T) {
	h1 := gatewayv1.Hostname("a.example.com")
	h2 := gatewayv1.Hostname("b.example.com")
	gw := &gatewayv1.Gateway{
		Spec: gatewayv1.GatewaySpec{
			Listeners: []gatewayv1.Listener{
				{Name: "http", Hostname: &h1},
				{Name: "https", Hostname: &h2},
			},
		},
	}
	ref := gatewayv1.ParentReference{Name: gatewayv1.ObjectName("gw")}
	out := AdmissionHostnameFiltersForRouteParent(gw, nil, &ref, "ns")
	require.ElementsMatch(t, []string{"a.example.com", "b.example.com"}, out)
}

func TestAdmissionHostnameFiltersForRouteParent_gatewaySection(t *testing.T) {
	h1 := gatewayv1.Hostname("a.example.com")
	h2 := gatewayv1.Hostname("b.example.com")
	sec := gatewayv1.SectionName("https")
	gw := &gatewayv1.Gateway{
		Spec: gatewayv1.GatewaySpec{
			Listeners: []gatewayv1.Listener{
				{Name: "http", Hostname: &h1},
				{Name: "https", Hostname: &h2},
			},
		},
	}
	ref := gatewayv1.ParentReference{Name: gatewayv1.ObjectName("gw"), SectionName: &sec}
	out := AdmissionHostnameFiltersForRouteParent(gw, nil, &ref, "ns")
	require.Equal(t, []string{"b.example.com"}, out)
}

func TestAdmissionHostnameFiltersForRouteParentFromRows_matchesAdmissionHostnameFiltersForRouteParent(t *testing.T) {
	h1 := gatewayv1.Hostname("a.example.com")
	h2 := gatewayv1.Hostname("b.example.com")
	gw := &gatewayv1.Gateway{
		Spec: gatewayv1.GatewaySpec{
			Listeners: []gatewayv1.Listener{
				{Name: "http", Hostname: &h1},
				{Name: "https", Hostname: &h2},
			},
		},
	}
	refAll := gatewayv1.ParentReference{Name: gatewayv1.ObjectName("gw")}
	sec := gatewayv1.SectionName("https")
	refSec := gatewayv1.ParentReference{Name: gatewayv1.ObjectName("gw"), SectionName: &sec}
	h := gatewayv1.Hostname("ls.example.com")
	secLS := gatewayv1.SectionName("one")
	ls := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "ls"},
		Spec: gatewayv1.ListenerSetSpec{
			Listeners: []gatewayv1.ListenerEntry{
				{Name: "zero", Hostname: gatewayHostnamePtr("z.example.com"), Port: 80, Protocol: gatewayv1.HTTPProtocolType},
				{Name: "one", Hostname: &h, Port: 443, Protocol: gatewayv1.HTTPSProtocolType},
			},
		},
	}
	refLS := gatewayv1.ParentReference{
		Kind:        gatewayKindPtr("ListenerSet"),
		Name:        gatewayv1.ObjectName("ls"),
		SectionName: &secLS,
	}
	rows := BuildAdmissionHostCacheRows(gw, []gatewayv1.ListenerSet{*ls})
	require.Equal(t, AdmissionHostnameFiltersForRouteParent(gw, nil, &refAll, "ns"), AdmissionHostnameFiltersForRouteParentFromRows(rows, &refAll, "ns"))
	require.Equal(t, AdmissionHostnameFiltersForRouteParent(gw, nil, &refSec, "ns"), AdmissionHostnameFiltersForRouteParentFromRows(rows, &refSec, "ns"))
	require.Equal(t, AdmissionHostnameFiltersForRouteParent(nil, ls, &refLS, "ns"), AdmissionHostnameFiltersForRouteParentFromRows(rows, &refLS, "ns"))
}

func TestAdmissionHostnameFiltersForRouteParent_listenerSetSection(t *testing.T) {
	h := gatewayv1.Hostname("ls.example.com")
	sec := gatewayv1.SectionName("one")
	ls := &gatewayv1.ListenerSet{
		Spec: gatewayv1.ListenerSetSpec{
			Listeners: []gatewayv1.ListenerEntry{
				{Name: "zero", Hostname: gatewayHostnamePtr("z.example.com"), Port: 80, Protocol: gatewayv1.HTTPProtocolType},
				{Name: "one", Hostname: &h, Port: 443, Protocol: gatewayv1.HTTPSProtocolType},
			},
		},
	}
	ref := gatewayv1.ParentReference{
		Kind:        gatewayKindPtr("ListenerSet"),
		Name:        gatewayv1.ObjectName("ls"),
		SectionName: &sec,
	}
	out := AdmissionHostnameFiltersForRouteParent(nil, ls, &ref, "ns")
	require.Equal(t, []string{"ls.example.com"}, out)
}

func gatewayHostnamePtr(s string) *gatewayv1.Hostname {
	h := gatewayv1.Hostname(s)
	return &h
}

func gatewayKindPtr(k string) *gatewayv1.Kind {
	x := gatewayv1.Kind(k)
	return &x
}
