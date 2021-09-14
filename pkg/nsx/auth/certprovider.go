// Copyright (c) 2021 VMware, Inc. All rights reserved. VMware Confidential.

package auth

// ClientCertProvider is implementation for client certificate provider
// Responsible for preparing, providing and disposing client certificate
// file. Basic implementation assumes the file exists in the file system
// and does not take responsibility of deleting this sensitive information
// after use.
type ClientCertProvider interface {
	// FileName returns file name of certificate.
	FileName() string
}
