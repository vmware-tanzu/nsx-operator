/* Copyright © 2024 VMware, Inc. All Rights Reserved.
   SPDX-License-Identifier: Apache-2.0 */

package util

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

var (
	validatingWebhookConfiguration = "nsx-operator-validating-webhook-configuration"
	namespace                      = "vmware-system-nsx"
	certName                       = "nsx-operator-webhook-cert"
)

// WriteFile writes data in the file at the given path
func writeFile(filepath string, cert []byte) error {
	f, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(cert)
	if err != nil {
		return err
	}
	return nil
}

func GenerateWebhookCerts() error {
	var caPEM, serverCertPEM, serverKeyPEM *bytes.Buffer
	// CA config
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	ca := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"broadcom.com"},
			CommonName:   "webhook",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	// CA private key
	caKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		log.Error(err, "Failed to generate private key")
		return err
	}

	// Self-signed CA certificate
	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		log.Error(err, "Failed to generate CA")
		return err
	}

	// PEM encode CA cert
	caPEM = new(bytes.Buffer)
	pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	dnsNames := []string{"vmware-system-nsx-operator-webhook-service", "vmware-system-nsx-operator-webhook-service.vmware-system-nsx", "vmware-system-nsx-operator-webhook-service.vmware-system-nsx.svc"}
	commonName := "vmware-system-nsx-operator-webhook-service.vmware-system-nsx.svc"

	serialNumber, err = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	// server cert config
	cert := &x509.Certificate{
		DNSNames:     dnsNames,
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"broadcom.com"},
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	// server private key
	serverKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		log.Error(err, "Failed to generate server key")
		return err
	}

	// sign the server cert
	serverCertBytes, err := x509.CreateCertificate(rand.Reader, cert, ca, &serverKey.PublicKey, caKey)
	if err != nil {
		log.Error(err, "Failed to sign server certificate")
		return err
	}

	// PEM encode the  server cert and key
	serverCertPEM = new(bytes.Buffer)
	pem.Encode(serverCertPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverCertBytes,
	})

	serverKeyPEM = new(bytes.Buffer)
	pem.Encode(serverKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(serverKey),
	})

	kubeClient := kubernetes.NewForConfigOrDie(ctrl.GetConfigOrDie())
	certSecret := &corev1.Secret{
		TypeMeta: v1.TypeMeta{},
		ObjectMeta: v1.ObjectMeta{
			Namespace: namespace,
			Name:      certName,
		},
		Data: map[string][]byte{
			"tls.key": serverKeyPEM.Bytes(),
			"tls.crt": serverCertPEM.Bytes(),
		},
	}
	if err := retry.OnError(retry.DefaultRetry, func(err error) bool {
		return err != nil
	}, func() error {
		if _, err := kubeClient.CoreV1().Secrets(namespace).Create(context.TODO(), certSecret, v1.CreateOptions{}); err != nil {
			if errors.IsAlreadyExists(err) {
				existingSecret, err := kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), certName, v1.GetOptions{})
				if err != nil {
					return err
				}

				// Update the existing secret with new data
				existingSecret.Data = certSecret.Data

				_, err = kubeClient.CoreV1().Secrets(namespace).Update(context.TODO(), existingSecret, v1.UpdateOptions{})
				if err != nil {
					log.Error(err, "Failed to update secret", "name", certName)
					return err
				}
				log.Info("Secret updated successfully", "name", certName)
			} else {
				log.Error(err, "Failed to create secret", "name", certName)
				return err
			}
		}
		if err = updateWebhookConfig(kubeClient, caPEM); err != nil {
			log.Error(err, "Failed to update webhook configuration", "name", validatingWebhookConfiguration)
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	if err = os.MkdirAll(config.WebhookCertDir, 0755); err != nil {
		log.Error(err, "Failed to create directory", "Dir", config.WebhookCertDir)
		return err
	}
	if err = writeFile(path.Join(config.WebhookCertDir, "tls.crt"), certSecret.Data["tls.crt"]); err != nil {
		log.Error(err, "Failed to write tls cert", "Path", path.Join(config.WebhookCertDir, "tls.crt"))
		return err
	}

	if err = writeFile(path.Join(config.WebhookCertDir, "tls.key"), certSecret.Data["tls.key"]); err != nil {
		log.Error(err, "Failed to write tls cert", "Path", path.Join(config.WebhookCertDir, "tls.key"))
		return err
	}
	return nil
}

func updateWebhookConfig(kubeClient *kubernetes.Clientset, caCert *bytes.Buffer) error {
	webhookCfg, err := kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(context.TODO(), validatingWebhookConfiguration, v1.GetOptions{})
	if err != nil {
		return err
	}
	updated := false
	for idx, webhook := range webhookCfg.Webhooks {
		if bytes.Equal(webhook.ClientConfig.CABundle, caCert.Bytes()) {
			continue
		}
		updated = true
		webhook.ClientConfig.CABundle = caCert.Bytes()
		webhookCfg.Webhooks[idx] = webhook
	}
	if updated {
		if _, err := kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Update(context.TODO(), webhookCfg, v1.UpdateOptions{}); err != nil {
			return err
		}
	}
	return nil
}
