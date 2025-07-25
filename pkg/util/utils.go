/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"context"
	"crypto/sha1" // #nosec G505: not used for security purposes
	"fmt"
	"math/big"
	"strings"

	mapset "github.com/deckarep/golang-set"
	"github.com/google/uuid"
	"github.com/vmware/vsphere-automation-sdk-go/services/nsxt/model"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	t1v1alpha1 "github.com/vmware-tanzu/nsx-operator/pkg/apis/legacy/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/apis/vpc/v1alpha1"
	"github.com/vmware-tanzu/nsx-operator/pkg/logger"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx/services/common"
	nsxutil "github.com/vmware-tanzu/nsx-operator/pkg/nsx/util"
)

const (
	wcpSystemResource = "vmware-system-shared-t1"
	HashCharset       = "0123456789abcdefghijklmnopqrstuvwxyz"
)

var (
	String      = common.String
	clusterUUID uuid.UUID
)

var log = &logger.Log

func truncateLabelHash(data string) string {
	return Sha1(data)[:common.HashLength]
}

func NormalizeLabels(matchLabels *map[string]string) *map[string]string {
	newLabels := make(map[string]string)
	for k, v := range *matchLabels {
		newLabels[NormalizeLabelKey(k, truncateLabelHash)] = NormalizeLabelValue(v, truncateLabelHash)
	}
	return &newLabels
}

func NormalizeLabelKey(key string, shaFn func(data string) string) string {
	if len(key) <= common.MaxTagScopeLength {
		return key
	}
	splitted := strings.Split(key, "/")
	key = splitted[len(splitted)-1]
	return normalizeNameByLimit(key, "", common.MaxTagScopeLength, shaFn)
}

func NormalizeLabelValue(value string, shaFn func(data string) string) string {
	return normalizeNameByLimit(value, "", common.MaxTagValueLength, shaFn)
}

func normalizeNameByLimit(name string, suffix string, limit int, hashFn func(data string) string) string {
	newName := connectStrings(common.ConnectorUnderline, name, suffix)
	if len(newName) <= limit {
		return newName
	}

	hashedTarget := name
	if len(suffix) > 0 {
		hashedTarget = suffix
	}

	hashString := hashFn(hashedTarget)
	nameLength := limit - len(hashString) - 1
	if len(name) < nameLength {
		nameLength = len(name)
	}
	return strings.Join([]string{name[:nameLength], hashString}, common.ConnectorUnderline)
}

func NormalizeId(name string) string {
	newName := strings.ReplaceAll(name, ":", "_")
	if len(newName) <= common.MaxIdLength {
		return newName
	}
	hashString := Sha1(name)
	nameLength := common.MaxIdLength - common.HashLength - 1
	for strings.ContainsAny(string(newName[nameLength-1]), "-._") {
		nameLength--
	}
	newName = fmt.Sprintf("%s-%s", newName[:nameLength], hashString[:common.HashLength])
	return newName
}

func Sha1(data string) string {
	sum := getSha1Bytes(data)
	return fmt.Sprintf("%x", sum)
}

func getSha1Bytes(data string) []byte {
	h := sha1.New() // #nosec G401: not used for security purposes
	h.Write([]byte(data))
	sum := h.Sum(nil)
	return sum
}

// Sha1WithCustomizedCharset uses the chars in `HashCharset` to present the hash result on the input data. We now use Sha1 as
// the hash algorithm.
func Sha1WithCustomizedCharset(data string) string {
	sum := getSha1Bytes(data)
	value := new(big.Int).SetBytes(sum[:])
	base := big.NewInt(int64(len(HashCharset)))
	var result []byte
	for value.Cmp(big.NewInt(0)) > 0 {
		mod := new(big.Int).Mod(value, base)
		result = append(result, HashCharset[mod.Int64()])
		value.Div(value, base)
	}

	// Reverse the result because the encoding process generates characters in reverse order
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return string(result)
}

func RemoveDuplicateStr(strSlice []string) []string {
	stringSet := mapset.NewSet()

	for _, d := range strSlice {
		stringSet.Add(d)
	}
	resultStr := make([]string, len(stringSet.ToSlice()))
	for i, v := range stringSet.ToSlice() {
		resultStr[i] = v.(string)
	}

	return resultStr
}

func ToUpper(obj interface{}) string {
	str := fmt.Sprintf("%s", obj)
	return strings.ToUpper(str)
}

func CalculateSubnetSize(mask int) int64 {
	size := 1 << uint(32-mask)
	return int64(size)
}

func IsSystemNamespace(c client.Client, ns string, obj *v1.Namespace, vpcMode bool) (bool, error) {
	// Only check VPC system namespace if VPC mode is enabled
	if vpcMode {
		isSysNs, err := IsVPCSystemNamespace(c, ns, obj)
		if err != nil {
			return false, err
		}
		if isSysNs {
			return true, nil
		}
	} else {
		// Only check T1 system namespace if VPC mode is disabled
		isSysNs, err := IsT1Namespace(c, ns, obj)
		if err != nil {
			return false, err
		}
		if isSysNs {
			return true, nil
		}
	}
	return false, nil
}

func IsT1Namespace(c client.Client, ns string, obj *v1.Namespace) (bool, error) {
	nsObj := &v1.Namespace{}
	if obj != nil {
		nsObj = obj
	} else if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: ns}, nsObj); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if isSysNs, ok := nsObj.Annotations[wcpSystemResource]; ok && strings.ToLower(isSysNs) == "true" {
		return true, nil
	}
	return false, nil
}

func IsVPCSystemNamespace(c client.Client, ns string, obj *v1.Namespace) (bool, error) {
	nsObj := &v1.Namespace{}
	if obj != nil {
		nsObj = obj
	} else if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: ns}, nsObj); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if sharedVPCNs, ok := nsObj.Annotations[common.AnnotationSharedVPCNamespace]; ok && sharedVPCNs == "kube-system" {
		return true, nil
	}
	return false, nil
}

// CheckPodHasNamedPort checks if the pod has a named port, it filters the pod events
// we don't want to give concern.
func CheckPodHasNamedPort(pod v1.Pod, reason string) bool {
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			if port.Name != "" {
				log.V(1).Info(fmt.Sprintf("%s pod %s has a named port %s", reason, pod.Name, port.Name))
				return true
			}
		}
	}
	log.V(1).Info(fmt.Sprintf("%s pod %s has no named port", reason, pod.Name))
	return false
}

func Contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}

func FilterOut(s []string, strToRemove string) []string {
	var result []string
	for _, element := range s {
		if element != strToRemove {
			result = append(result, element)
		}
	}
	return result
}

func If(condition bool, trueVal, falseVal interface{}) interface{} {
	if condition {
		return trueVal
	} else {
		return falseVal
	}
}

// the changes map contains key/value map that you want to change.
// if giving empty value for a key in changes map like: "mykey":"", that means removing this annotation from k8s resource
func UpdateK8sResourceAnnotation(client client.Client, ctx context.Context, k8sObj client.Object, changes map[string]string) error {
	needUpdate := false
	anno := k8sObj.GetAnnotations() // here it may return a nil because ns do not have annotations.
	newAnno := If(anno == nil, map[string]string{}, anno).(map[string]string)
	for key, value := range changes {
		// if value is not none, it means this key/value need to add/update
		if value != "" {
			needUpdate = true
			newAnno[key] = value
		} else { // if value is empty, then this key/value need to be removed from map
			_, exist := newAnno[key]
			if exist {
				delete(newAnno, key)
				needUpdate = true
			} else {
				log.Info("No need to change ns annotation")
				needUpdate = false
			}
		}
	}
	// update k8s object
	k8sObj.SetAnnotations(newAnno)

	// only send update request when it is needed
	if needUpdate {
		err := client.Update(ctx, k8sObj)
		if err != nil {
			return err
		}
	}
	return nil
}

func truncateNameOrIDHash(data string) string {
	return Sha1WithCustomizedCharset(data)[:common.Base62HashLength]
}

func TruncateUIDHash(uid string) string {
	return Sha1WithCustomizedCharset(uid)[:common.UUIDHashLength]
}

// GenerateIDByObject generate string id for NSX resource using the provided Object's name and the hash of CR uid.
// Note, this function is used on the resources with VPC scenario, and the provided obj is the K8s CR which is
// used to generate the NSX resource.
// Note: This function may use hash(obj.UID)[:5] as the return string's suffix. Since the hash suffix is short,
// it may have collision with the existing NSX resources, the corresponding handle is provided by nsx services layer.
func GenerateIDByObject(obj metav1.Object) string {
	limit := common.MaxIdLength
	uidStr := string(obj.GetUID())
	suffix := TruncateUIDHash(uidStr)
	desiredName := connectStrings(common.ConnectorUnderline, obj.GetName(), suffix)
	if len(desiredName) > limit {
		valueLen := limit - len(suffix) - 1
		desiredName = connectStrings(common.ConnectorUnderline, obj.GetName()[:valueLen], suffix)
	}
	return desiredName
}

// GenerateIDByObjectWithSuffix is only used to generate the NSX Security Rule id for now.
// TODO: remove this function after Security Rule id switch to `GenerateIDByObject`.
func GenerateIDByObjectWithSuffix(obj metav1.Object, suffix string) string {
	limit := common.MaxIdLength
	limit -= len(suffix) + 1
	return connectStrings(common.ConnectorUnderline, normalizeNameByLimit(obj.GetName(), string(obj.GetUID()), limit, truncateNameOrIDHash), suffix)
}

// GenerateID generate id for NSX resource, some resources has complex index, so set its type to string.
// Note, this function is used with T1 scenario, and the VPC resources (e.g., Security Rule) which are not migrated
// to the new desired ID format. For new introduced NSX VPC resources, please use functions like
// "BuildUniqueIDWithRandomUUID" in pkg/services/common/builder.go
func GenerateID(resID, prefix, suffix string, index string) string {
	return connectStrings(common.ConnectorUnderline, prefix, resID, index, suffix)
}

func connectStrings(sep string, parts ...string) string {
	strParts := make([]string, 0)
	for _, part := range parts {
		if len(part) > 0 {
			strParts = append(strParts, part)
		}
	}
	return strings.Join(strParts, sep)
}

func generateDisplayName(connector, resName, prefix, suffix, project, cluster string) string {
	// Return a string in this format:
	// prefix<connector>cluster<connector>resName<connector>project<connector>suffix.
	return connectStrings(connector, prefix, cluster, resName, project, suffix)
}

func GenerateTruncName(limit int, resName string, prefix, suffix, project, cluster string) string {
	adjustedLimit := limit - len(prefix) - len(suffix)
	for _, i := range []string{prefix, suffix} {
		if len(i) > 0 {
			adjustedLimit -= 1
		}
	}
	oldName := generateDisplayName(common.ConnectorUnderline, resName, "", "", project, cluster)
	if len(oldName) > adjustedLimit {
		newName := normalizeNameByLimit(oldName, "", adjustedLimit, TruncateUIDHash)
		return generateDisplayName(common.ConnectorUnderline, newName, prefix, suffix, "", "")
	}
	return generateDisplayName(common.ConnectorUnderline, resName, prefix, suffix, project, cluster)
}

func BuildBasicTags(cluster string, obj interface{}, namespaceID types.UID) []model.Tag {
	tags := []model.Tag{
		{
			Scope: String(common.TagScopeCluster),
			Tag:   String(cluster),
		},
		{
			Scope: String(common.TagScopeVersion),
			Tag:   String(strings.Join(common.TagValueVersion, ".")),
		},
	}
	switch i := obj.(type) {
	case *v1alpha1.StaticRoute:
		tags = append(tags, model.Tag{Scope: String(common.TagScopeNamespace), Tag: String(i.ObjectMeta.Namespace)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeStaticRouteCRName), Tag: String(i.ObjectMeta.Name)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeStaticRouteCRUID), Tag: String(string(i.UID))})
	case *t1v1alpha1.SecurityPolicy:
		tags = append(tags, model.Tag{Scope: String(common.TagScopeNamespace), Tag: String(i.ObjectMeta.Namespace)})
	case *networkingv1.NetworkPolicy:
		tags = append(tags, model.Tag{Scope: String(common.TagScopeNamespace), Tag: String(i.ObjectMeta.Namespace)})
	case *v1alpha1.Subnet:
		tags = append(tags, model.Tag{Scope: String(common.TagScopeSubnetCRName), Tag: String(i.ObjectMeta.Name)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeSubnetCRUID), Tag: String(string(i.UID))})
	case *v1alpha1.SubnetSet:
		tags = append(tags, model.Tag{Scope: String(common.TagScopeSubnetSetCRName), Tag: String(i.ObjectMeta.Name)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeSubnetSetCRUID), Tag: String(string(i.UID))})
	case *v1alpha1.SubnetPort:
		tags = append(tags, model.Tag{Scope: String(common.TagScopeNamespace), Tag: String(i.ObjectMeta.Namespace)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeVMNamespace), Tag: String(i.ObjectMeta.Namespace)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeVMNamespaceUID), Tag: String(string(namespaceID))})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeSubnetPortCRName), Tag: String(i.ObjectMeta.Name)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeSubnetPortCRUID), Tag: String(string(i.UID))})
	case *v1.Pod:
		tags = append(tags, model.Tag{Scope: String(common.TagScopeNamespace), Tag: String(i.ObjectMeta.Namespace)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopePodName), Tag: String(i.ObjectMeta.Name)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopePodUID), Tag: String(string(i.UID))})
	case *v1alpha1.NetworkInfo:
		tags = append(tags, model.Tag{Scope: String(common.TagScopeNamespace), Tag: String(i.ObjectMeta.Namespace)})
	case *v1alpha1.IPAddressAllocation:
		tags = append(tags, model.Tag{Scope: String(common.TagScopeNamespace), Tag: String(i.ObjectMeta.Namespace)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeIPAddressAllocationCRName), Tag: String(i.ObjectMeta.Name)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeIPAddressAllocationCRUID), Tag: String(string(i.UID))})
	case *v1alpha1.SubnetConnectionBindingMap:
		tags = append(tags, model.Tag{Scope: String(common.TagScopeNamespace), Tag: String(i.ObjectMeta.Namespace)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeSubnetBindingCRName), Tag: String(i.ObjectMeta.Name)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeSubnetBindingCRUID), Tag: String(string(i.ObjectMeta.UID))})
	case *v1alpha1.AddressBinding:
		tags = append(tags, model.Tag{Scope: String(common.TagScopeNamespace), Tag: String(i.ObjectMeta.Namespace)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeAddressBindingCRName), Tag: String(i.ObjectMeta.Name)})
		tags = append(tags, model.Tag{Scope: String(common.TagScopeAddressBindingCRUID), Tag: String(string(i.UID))})
	default:
		log.Info("Unknown obj type", "obj", obj)
	}

	if len(namespaceID) > 0 {
		tags = append(tags, model.Tag{Scope: String(common.TagScopeNamespaceUID), Tag: String(string(namespaceID))})
	}
	return tags
}

func Capitalize(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func GetRandomIndexString() string {
	uuidStr := uuid.NewString()
	return Sha1(uuidStr)[:common.HashLength]
}

// IsPowerOfTwo checks if a given number is a power of 2
func IsPowerOfTwo(n int) bool {
	return n > 0 && (n&(n-1)) == 0
}

func GetClusterUUID(clusterID string) uuid.UUID {
	if clusterUUID == uuid.Nil {
		clusterUUID = uuid.NewSHA1(uuid.NameSpaceX500, []byte(clusterID))
	}
	return clusterUUID
}

func NSXSubnetDHCPEnabled(nsxSubnet *model.VpcSubnet) bool {
	return nsxSubnet.SubnetDhcpConfig != nil && nsxSubnet.SubnetDhcpConfig.Mode != nil && *nsxSubnet.SubnetDhcpConfig.Mode != nsxutil.ParseDHCPMode(v1alpha1.DHCPConfigModeDeactivated)
}

func CRSubnetDHCPEnabled(obj client.Object) bool {
	mode := ""
	switch o := obj.(type) {
	case *v1alpha1.Subnet:
		mode = string(o.Spec.SubnetDHCPConfig.Mode)
	case *v1alpha1.SubnetSet:
		mode = string(o.Spec.SubnetDHCPConfig.Mode)
	}
	return mode == v1alpha1.DHCPConfigModeServer || mode == v1alpha1.DHCPConfigModeRelay
}
