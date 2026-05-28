// Copyright 2017 The Kubernetes Authors.
// Copyright 2026 Broadcom, Inc.
//
// SPDX-License-Identifier: Apache-2.0
//
// Derived from sigs.k8s.io/external-dns/endpoint/endpoint.go (core types + constructors, trimmed).
// Attribution: see package doc.go.

package endpoint

import (
	"cmp"
	"slices"
	"strings"
)

const (
	RecordTypeA     = "A"
	RecordTypeAAAA  = "AAAA"
	RecordTypeCNAME = "CNAME"
	RecordTypeTXT   = "TXT"
	RecordTypeSRV   = "SRV"
	RecordTypeNS    = "NS"
	RecordTypePTR   = "PTR"
	RecordTypeMX    = "MX"
	RecordTypeNAPTR = "NAPTR"

	ProviderSpecificRecordType = "record-type"
	providerSpecificAlias      = "alias"
)

// TTL defines the TTL of a DNS record.
type TTL int64

// IsConfigured returns true if TTL is configured.
func (ttl TTL) IsConfigured() bool {
	return ttl > 0
}

// Targets is a list of targets for an endpoint.
type Targets []string

// NewTargets returns sorted unique targets (ExternalDNS-compatible).
func NewTargets(target ...string) Targets {
	seen := make(map[string]struct{})
	var out Targets
	for _, t := range target {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// ProviderSpecificProperty holds provider-specific configuration.
type ProviderSpecificProperty struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

// ProviderSpecific is a list of provider-specific properties.
type ProviderSpecific []ProviderSpecificProperty

// Labels is ExternalDNS-style optional metadata on an endpoint; nsx-operator leaves it unset for produced endpoints.
type Labels map[string]string

// Endpoint mirrors sigs.k8s.io/external-dns/endpoint.Endpoint for provider-agnostic DNS planning.
type Endpoint struct {
	DNSName          string           `json:"dnsName,omitempty"`
	Targets          Targets          `json:"targets,omitempty"`
	RecordType       string           `json:"recordType,omitempty"`
	SetIdentifier    string           `json:"setIdentifier,omitempty"`
	RecordTTL        TTL              `json:"recordTTL,omitempty"`
	Labels           Labels           `json:"labels,omitempty"`
	ProviderSpecific ProviderSpecific `json:"providerSpecific,omitempty"`
}

// EndpointKey separates endpoints for deduplication (subset of upstream Key()).
type EndpointKey struct {
	DNSName       string
	RecordType    string
	SetIdentifier string
}

func (ep EndpointKey) String() string {
	return ep.DNSName + "/" + ep.RecordType + "/" + ep.SetIdentifier
}

// NewEndpoint creates an endpoint with default TTL 0.
func NewEndpoint(dnsName, recordType string, targets ...string) *Endpoint {
	return NewEndpointWithTTL(dnsName, recordType, TTL(0), targets...)
}

// NewEndpointWithTTL creates an endpoint with the given TTL.
func NewEndpointWithTTL(dnsName, recordType string, ttl TTL, targets ...string) *Endpoint {
	cleanTargets := make([]string, 0, len(targets))
	for _, target := range targets {
		switch recordType {
		case RecordTypeTXT, RecordTypeNAPTR, RecordTypeSRV:
			cleanTargets = append(cleanTargets, target)
		default:
			cleanTargets = append(cleanTargets, strings.TrimSuffix(target, "."))
		}
	}
	for _, label := range strings.Split(dnsName, ".") {
		if len(label) > 63 {
			return nil
		}
	}
	return &Endpoint{
		DNSName:    strings.TrimSuffix(dnsName, "."),
		Targets:    cleanTargets,
		RecordType: recordType,
		RecordTTL:  ttl,
	}
}

// Key returns a deduplication key for this endpoint.
func (e *Endpoint) Key() EndpointKey {
	return EndpointKey{
		DNSName:       e.DNSName,
		RecordType:    e.RecordType,
		SetIdentifier: e.SetIdentifier,
	}
}

// WithSetIdentifier sets the set identifier.
func (e *Endpoint) WithSetIdentifier(setIdentifier string) *Endpoint {
	e.SetIdentifier = setIdentifier
	return e
}

// WithProviderSpecific attaches a provider-specific property.
func (e *Endpoint) WithProviderSpecific(key, value string) *Endpoint {
	e.SetProviderSpecificProperty(key, value)
	return e
}

// GetProviderSpecificProperty returns a provider-specific value by name.
func (e *Endpoint) GetProviderSpecificProperty(key string) (string, bool) {
	for _, p := range e.ProviderSpecific {
		if p.Name == key {
			return p.Value, true
		}
	}
	return "", false
}

// SetProviderSpecificProperty sets or replaces a provider-specific property.
func (e *Endpoint) SetProviderSpecificProperty(key, value string) {
	for i := range e.ProviderSpecific {
		if e.ProviderSpecific[i].Name == key {
			e.ProviderSpecific[i].Value = value
			return
		}
	}
	e.ProviderSpecific = append(e.ProviderSpecific, ProviderSpecificProperty{Name: key, Value: value})
}

// WithLabel adds or updates a label.
func (e *Endpoint) WithLabel(key, value string) *Endpoint {
	if e.Labels == nil {
		e.Labels = make(Labels)
	}
	e.Labels[key] = value
	return e
}

// GetBoolProviderSpecificProperty parses a boolean provider-specific property.
func (e *Endpoint) GetBoolProviderSpecificProperty(key string) (bool, bool) {
	prop, ok := e.GetProviderSpecificProperty(key)
	if !ok {
		return false, false
	}
	switch prop {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, true
	}
}

func (e *Endpoint) supportsAlias() bool {
	switch e.RecordType {
	case RecordTypeA, RecordTypeAAAA, RecordTypeCNAME:
		return true
	default:
		return false
	}
}

func (e *Endpoint) isAlias() bool {
	val, ok := e.GetBoolProviderSpecificProperty(providerSpecificAlias)
	return ok && val
}

// CheckEndpoint validates basic alias / IP constraints (subset of upstream).
func (e *Endpoint) CheckEndpoint() bool {
	if !e.supportsAlias() {
		if _, ok := e.GetBoolProviderSpecificProperty(providerSpecificAlias); ok {
			return false
		}
	}
	return true
}

// RetainProviderProperties sorts provider-specific entries (upstream subset).
func (e *Endpoint) RetainProviderProperties(provider string) {
	if len(e.ProviderSpecific) == 0 {
		return
	}
	if provider != "" && provider != "cloudflare" {
		prefix := provider + "/"
		e.ProviderSpecific = slices.DeleteFunc(e.ProviderSpecific, func(prop ProviderSpecificProperty) bool {
			return strings.Contains(prop.Name, "/") && !strings.HasPrefix(prop.Name, prefix)
		})
	}
	slices.SortFunc(e.ProviderSpecific, func(a, b ProviderSpecificProperty) int {
		return cmp.Compare(a.Name, b.Name)
	})
}
