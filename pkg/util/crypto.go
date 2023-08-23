/* Copyright © 2022 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"
)

const (
	DefaultRSABits = 2048
	// For now the ClusterControlPlane API doesn't support rotating certificate. We set long valid time to avoid certificate expiration.
	DefaultValidDays             = 3650
	DefaultValidDaysWithRotation = 365
	DefaultRotateDays            = 7
	DefaultSerialNumberLength    = 160
)

var (
	DefaultSubject = pkix.Name{
		Country:            []string{"US"},
		Organization:       []string{"VMware"},
		OrganizationalUnit: []string{"Antrea Cluster"},
		Locality:           []string{"Palo Alto"},
		Province:           []string{"CA"},
		CommonName:         "dummy",
	}
)

// GenerateCertificate returns generated certificate and private key in PEM format
func GenerateCertificate(subject *pkix.Name, validDays int) (string, string, error) {
	if subject == nil {
		defaultSubject := DefaultSubject
		subject = &defaultSubject
	}
	if validDays <= 0 {
		validDays = DefaultValidDays
	}

	priv, err := rsa.GenerateKey(rand.Reader, DefaultRSABits)
	if err != nil {
		log.Error(err, "failed to generate RSA key")
		return "", "", err
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), DefaultSerialNumberLength))
	if err != nil {
		log.Error(err, "failed to generate serial number")
		return "", "", err
	}
	notBefore := time.Now()
	notAfter := notBefore.AddDate(0, 0, validDays)
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      *subject,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		log.Error(err, "failed to create certificate")
		return "", "", err
	}

	certOut := &bytes.Buffer{}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyOut := &bytes.Buffer{}
	privBytes := x509.MarshalPKCS1PrivateKey(priv)
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: privBytes})
	return string(certOut.Bytes()), string(keyOut.Bytes()), nil
}
