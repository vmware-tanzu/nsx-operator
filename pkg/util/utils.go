/* Copyright © 2021 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"crypto/sha1"
	"fmt"
	"sort"
	"strings"

	mapset "github.com/deckarep/golang-set"
)

func NormalizeLabels(matchLabels *map[string]string) *map[string]string {
	newLabels := make(map[string]string)
	for k, v := range *matchLabels {
		newLabels[NormalizeLabelKey(k)] = NormalizeName(v)
	}
	return &newLabels
}

func NormalizeLabelKey(key string) string {
	if len(key) <= MaxTagLength {
		return key
	}
	splitted := strings.Split(key, "/")
	key = splitted[len(splitted)-1]
	return NormalizeName(key)
}

func NormalizeName(name string) string {
	if len(name) <= MaxTagLength {
		return name
	}
	hashString := Sha1(name)
	nameLength := MaxTagLength - HashLength - 1
	newName := fmt.Sprintf("%s-%s", name[:nameLength], hashString[:HashLength])
	return newName
}

func Sha1(data string) string {
	h := sha1.New()
	h.Write([]byte(data))
	sum := h.Sum(nil)
	return fmt.Sprintf("%x", sum)
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

func MergeMaps(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

func MergeAddressByPort(ipPorts []Address) []Address {
	var portIPs []Address
	var sortKeys []int
	mappedPorts := make(map[int][]string)
	for _, ipPort := range ipPorts {
		if _, ok := mappedPorts[ipPort.Port]; !ok {
			sortKeys = append(sortKeys, ipPort.Port)
			mappedPorts[ipPort.Port] = ipPort.IPs
		} else {
			mappedPorts[ipPort.Port] = append(mappedPorts[ipPort.Port], ipPort.IPs...)
		}
	}
	sort.Ints(sortKeys)
	for _, key := range sortKeys {
		portIPs = append(portIPs, Address{Port: key, IPs: mappedPorts[key]})
	}
	return portIPs
}

func ToUpper(obj interface{}) string {
	str := fmt.Sprintf("%s", obj)
	return strings.ToUpper(str)
}
