/* Copyright © 2021 VMware, Inc. All Rights Reserved.

   SPDX-License-Identifier: Apache-2.0 */

package nsxapi

import "k8s.io/client-go/rest"

type Interface interface {
	RESTClient() rest.Interface
}

type nsxClient struct {
	//nsxlib --> nsxlib client
}
