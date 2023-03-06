/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGenerateCertificate(t *testing.T) {
	type args struct {
		subject   *pkix.Name
		validDays int
	}
	var tests = []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "default",
			args: args{
				subject:   nil,
				validDays: 0,
			},
			wantErr: false,
		},
		{
			name: "standard",
			args: args{
				subject: &pkix.Name{
					Country:            []string{"US"},
					Organization:       []string{"VMware"},
					OrganizationalUnit: []string{"Antrea Cluster"},
					Locality:           []string{"Palo Alto"},
					Province:           []string{"CA"},
					CommonName:         "standard",
				},
				validDays: 365,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1, err := GenerateCertificate(tt.args.subject, tt.args.validDays)
			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateCertificate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			decodedGot, rest := pem.Decode([]byte(got))
			assert.Equal(t, 0, len(rest))
			cert, err := x509.ParseCertificate(decodedGot.Bytes)
			assert.Nil(t, err)
			assert.Equal(t, x509.SHA256WithRSA, cert.SignatureAlgorithm)
			assert.Equal(t, 3, cert.Version)
			assert.Equal(t, cert.Subject, cert.Issuer)
			assert.True(t, time.Now().After(cert.NotBefore) && time.Now().Sub(cert.NotBefore) < time.Minute, "NotBefore is invalid")
			validDays := tt.args.validDays
			if validDays <= 0 {
				validDays = DefaultValidDays
			}
			assert.True(t, cert.NotAfter == cert.NotBefore.AddDate(0, 0, validDays))
			expectedSubject := tt.args.subject
			if expectedSubject == nil {
				expectedSubject = &DefaultSubject
			}
			actualSubject := cert.Subject
			actualSubject.Names = nil
			assert.Equal(t, *expectedSubject, actualSubject)

			decodedGot1, rest := pem.Decode([]byte(got1))
			assert.Equal(t, 0, len(rest))
			priv, err := x509.ParsePKCS1PrivateKey(decodedGot1.Bytes)
			assert.Nil(t, err)
			assert.Equal(t, 2048, priv.Size()*8)
		})
	}
}
