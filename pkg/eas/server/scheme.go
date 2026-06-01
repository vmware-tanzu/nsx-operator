/* Copyright © 2026 Broadcom, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package server

import (
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	easv1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/eas/v1alpha1"
)

// scheme, codecs and parameterCodec are initialised in init() so that codecs
// captures the scheme only after all EAS types have been registered.
var (
	scheme         *runtime.Scheme
	codecs         serializer.CodecFactory
	parameterCodec runtime.ParameterCodec
)

func init() {
	scheme = runtime.NewScheme()

	// EAS resource types (VPCIPAddressUsage, IPBlockUsage, SubnetIPPools, SubnetDHCPServerStats …).
	utilruntime.Must(easv1alpha1.AddToScheme(scheme))

	// meta.k8s.io/v1 — required for Status, ListOptions, GetOptions, TableOptions.
	metav1.AddToGroupVersion(scheme, schema.GroupVersion{Version: "v1"})

	// meta.k8s.io internal version — required by rest.Lister (ListOptions parameter).
	utilruntime.Must(metainternalversion.AddToScheme(scheme))

	codecs = serializer.NewCodecFactory(scheme)
	parameterCodec = runtime.NewParameterCodec(scheme)
}
