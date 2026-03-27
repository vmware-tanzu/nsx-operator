/* Copyright © 2024 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
)

const readMethodsAllowHeader = "GET, HEAD"

// resourceHandler groups the dispatch functions for a single EAS resource type.
type resourceHandler struct {
	kind        string
	namespaced  func(w http.ResponseWriter, r *http.Request, ns, name string)
	clusterWide func(w http.ResponseWriter, r *http.Request)
}

// resourceOps captures type-specific operations needed to serve a resource.
// Item is the singular resource type; List is the corresponding list type.
type resourceOps[Item any, List any] struct {
	kind    string
	get     func(context.Context, string, string) (*Item, error) // nil → list-and-filter fallback
	list    func(context.Context, string) (*List, error)
	items   func(*List) *[]Item // pointer to the Items field for read/append
	newList func() *List        // empty list with correct TypeMeta
	columns []metav1.TableColumnDefinition
	toRow   func(*Item) metav1.TableRow
	getName func(*Item) string
}

func easListTypeMeta(kind string) metav1.TypeMeta {
	return metav1.TypeMeta{APIVersion: easv1alpha1.GroupVersion.String(), Kind: kind}
}

func findItemByName[Item any](items []Item, name string, getName func(*Item) string) (*Item, bool) {
	for i := range items {
		if getName(&items[i]) == name {
			return &items[i], true
		}
	}
	return nil, false
}

func tableRowsForItems[Item any](items []Item, toRow func(*Item) metav1.TableRow) []metav1.TableRow {
	rows := make([]metav1.TableRow, 0, len(items))
	for i := range items {
		rows = append(rows, toRow(&items[i]))
	}
	return rows
}

// registerResource builds namespaced and cluster-wide handlers from generic ops.
func registerResource[Item any, List any](s *EASServer, name string, ops resourceOps[Item, List]) {
	s.handlers[name] = resourceHandler{
		kind: ops.kind,
		namespaced: func(w http.ResponseWriter, r *http.Request, ns, resName string) {
			ctx := r.Context()
			// Direct Get when available.
			if resName != "" && ops.get != nil {
				result, err := ops.get(ctx, ns, resName)
				if err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				writeResponse(w, r, result, ops.columns, []metav1.TableRow{ops.toRow(result)})
				return
			}
			// List (or list-and-filter when direct Get is unavailable).
			listResult, err := ops.list(ctx, ns)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			all := *ops.items(listResult)
			if resName != "" {
				if item, ok := findItemByName(all, resName, ops.getName); ok {
					writeResponse(w, r, item, ops.columns, []metav1.TableRow{ops.toRow(item)})
					return
				}
				writeError(w, http.StatusNotFound, fmt.Sprintf("%s %q not found in namespace %q", name, resName, ns))
				return
			}
			writeResponse(w, r, listResult, ops.columns, tableRowsForItems(all, ops.toRow))
		},
		clusterWide: func(w http.ResponseWriter, r *http.Request) {
			if !allowReadMethod(w, r, name) {
				return
			}
			log := logger.Log
			namespaces := s.vpcProvider.ListAllVPCNamespaces()
			log.Info("Cluster-wide list", "resource", name, "namespaceCount", len(namespaces))
			ctx := r.Context()
			merged := ops.newList()
			for _, ns := range namespaces {
				result, err := ops.list(ctx, ns)
				if err != nil {
					log.Error(err, "Failed to list "+name, "namespace", ns)
					continue
				}
				dst := ops.items(merged)
				*dst = append(*dst, *ops.items(result)...)
			}
			all := *ops.items(merged)
			log.Info("Cluster-wide list complete", "resource", name, "totalItems", len(all))
			writeResponse(w, r, merged, ops.columns, tableRowsForItems(all, ops.toRow))
		},
	}
}

// initHandlers builds the resource → handler dispatch table.
func (s *EASServer) initHandlers() {
	s.handlers = make(map[string]resourceHandler)

	registerResource(s, "vpcipaddressusages", resourceOps[easv1alpha1.VPCIPAddressUsage, easv1alpha1.VPCIPAddressUsageList]{
		kind: "VPCIPAddressUsage",
		get:  s.vpcIPUsage.Get, list: s.vpcIPUsage.List,
		items: func(l *easv1alpha1.VPCIPAddressUsageList) *[]easv1alpha1.VPCIPAddressUsage { return &l.Items },
		newList: func() *easv1alpha1.VPCIPAddressUsageList {
			return &easv1alpha1.VPCIPAddressUsageList{TypeMeta: easListTypeMeta("VPCIPAddressUsageList")}
		},
		columns: vpcIPUsageColumns,
		toRow: func(item *easv1alpha1.VPCIPAddressUsage) metav1.TableRow {
			return tableRow(item.Name, item.Namespace, vpcIPBlocksSummary(item))
		},
		getName: func(item *easv1alpha1.VPCIPAddressUsage) string { return item.Name },
	})

	registerResource(s, "ipblockusages", resourceOps[easv1alpha1.IPBlockUsage, easv1alpha1.IPBlockUsageList]{
		kind:  "IPBlockUsage",
		list:  s.ipBlockUsage.List, // no direct Get; uses list-and-filter
		items: func(l *easv1alpha1.IPBlockUsageList) *[]easv1alpha1.IPBlockUsage { return &l.Items },
		newList: func() *easv1alpha1.IPBlockUsageList {
			return &easv1alpha1.IPBlockUsageList{TypeMeta: easListTypeMeta("IPBlockUsageList")}
		},
		columns: ipBlockUsageColumns,
		toRow: func(item *easv1alpha1.IPBlockUsage) metav1.TableRow {
			return tableRow(item.Name, item.Namespace, ipBlockRangesSummary(item.Spec.UsedIpRanges), ipBlockRangesSummary(item.Spec.AvailableIpRanges))
		},
		getName: func(item *easv1alpha1.IPBlockUsage) string { return item.Name },
	})

	registerResource(s, "subnetippools", resourceOps[easv1alpha1.SubnetIPPools, easv1alpha1.SubnetIPPoolsList]{
		kind: "SubnetIPPools",
		get:  s.subnetIPPools.Get, list: s.subnetIPPools.List,
		items: func(l *easv1alpha1.SubnetIPPoolsList) *[]easv1alpha1.SubnetIPPools { return &l.Items },
		newList: func() *easv1alpha1.SubnetIPPoolsList {
			return &easv1alpha1.SubnetIPPoolsList{TypeMeta: easListTypeMeta("SubnetIPPoolsList")}
		},
		columns: subnetIPPoolsColumns,
		toRow: func(item *easv1alpha1.SubnetIPPools) metav1.TableRow {
			return tableRow(item.Name, item.Namespace, subnetIPPoolsSummary(item))
		},
		getName: func(item *easv1alpha1.SubnetIPPools) string { return item.Name },
	})

	registerResource(s, "subnetdhcpserverconfigstats", resourceOps[easv1alpha1.SubnetDHCPServerConfigStats, easv1alpha1.SubnetDHCPServerConfigStatsList]{
		kind: "SubnetDHCPServerConfigStats",
		get:  s.subnetDHCPStats.Get, list: s.subnetDHCPStats.List,
		items: func(l *easv1alpha1.SubnetDHCPServerConfigStatsList) *[]easv1alpha1.SubnetDHCPServerConfigStats {
			return &l.Items
		},
		newList: func() *easv1alpha1.SubnetDHCPServerConfigStatsList {
			return &easv1alpha1.SubnetDHCPServerConfigStatsList{TypeMeta: easListTypeMeta("SubnetDHCPServerConfigStatsList")}
		},
		columns: subnetDHCPColumns,
		toRow: func(item *easv1alpha1.SubnetDHCPServerConfigStats) metav1.TableRow {
			return tableRow(item.Name, item.Namespace, subnetDHCPStatsSummary(item))
		},
		getName: func(item *easv1alpha1.SubnetDHCPServerConfigStats) string { return item.Name },
	})
}

// registerRoutes sets up all HTTP routes.
func (s *EASServer) registerRoutes(mux *http.ServeMux) {
	s.initHandlers()

	// API discovery endpoint.
	mux.HandleFunc(APIBasePath, s.handleAPIResourceList)

	// Cluster-wide list (kubectl get <resource> -A).
	for res, h := range s.handlers {
		mux.HandleFunc(APIBasePath+"/"+res, func(w http.ResponseWriter, r *http.Request) {
			h.clusterWide(w, r)
		})
	}

	// Namespaced GET/LIST.
	mux.HandleFunc(APIBasePath+"/namespaces/", s.handleNamespacedResource)
}

// handleAPIResourceList returns the API resource list for discovery.
func (s *EASServer) handleAPIResourceList(w http.ResponseWriter, r *http.Request) {
	if !allowReadMethod(w, r, "apiresources") {
		return
	}
	log := logger.Log
	log.Debug("API discovery request", "path", r.URL.Path)

	resources := make([]metav1.APIResource, 0, len(s.handlers))
	for name, h := range s.handlers {
		resources = append(resources, metav1.APIResource{
			Name: name, Namespaced: true, Kind: h.kind, Verbs: metav1.Verbs{"get", "list"},
		})
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].Name < resources[j].Name })

	writeJSON(w, http.StatusOK, &metav1.APIResourceList{
		TypeMeta:     metav1.TypeMeta{Kind: "APIResourceList", APIVersion: metav1.Unversioned.String()},
		GroupVersion: easv1alpha1.GroupVersion.String(),
		APIResources: resources,
	})
}

// handleNamespacedResource routes namespaced GET/LIST to the registered handler.
func (s *EASServer) handleNamespacedResource(w http.ResponseWriter, r *http.Request) {
	log := logger.Log
	path := strings.TrimPrefix(r.URL.Path, APIBasePath+"/namespaces/")
	namespace, resource, name, ok := parseNamespacedResourcePath(path)
	if !ok {
		writeError(w, http.StatusNotFound, "invalid resource path")
		return
	}

	h, ok := s.handlers[resource]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown resource: %s", resource))
		return
	}
	if !allowReadMethod(w, r, resource) {
		return
	}

	log.Info("Handling namespaced request", "resource", resource, "namespace", namespace, "name", name)
	h.namespaced(w, r, namespace, name)
}

func allowReadMethod(w http.ResponseWriter, r *http.Request, resource string) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", readMethodsAllowHeader)
	writeAPIError(w, apierrors.NewMethodNotSupported(schema.GroupResource{
		Group:    easv1alpha1.GroupVersion.Group,
		Resource: resource,
	}, r.Method))
	return false
}

func parseNamespacedResourcePath(path string) (namespace, resource, name string, ok bool) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "", "", "", false
	}

	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 && len(parts) != 3 {
		return "", "", "", false
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", "", false
	}

	namespace = parts[0]
	resource = parts[1]
	if len(parts) == 3 {
		if parts[2] == "" {
			return "", "", "", false
		}
		name = parts[2]
	}

	return namespace, resource, name, true
}
