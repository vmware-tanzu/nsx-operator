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

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

var (
	validatingWebhookConfiguration = "nsx-operator-validating-webhook-configuration"
	namespace                      = "vmware-system-nsx"
	certName                       = "nsx-operator-webhook-cert"
	easCertSecretName              = "nsx-operator-eas-cert"
	// certDir is the directory where TLS certificates are stored.  It is a package-level
	// variable so that tests can redirect writes to a temp directory without root access.
	certDir = config.WebhookCertDir
)

// writeSecureFile writes data in the file at the given path with secure permissions
func writeSecureFile(filepath string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(filepath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(data)
	if err != nil {
		return err
	}

	// Explicitly set file permissions to ensure they are applied correctly,
	// even when overwriting existing files
	err = f.Chmod(perm)
	if err != nil {
		return err
	}

	return nil
}

func GenerateWebhookCerts() error {
	cfg, err := GetConfig()
	if err != nil {
		log.Error(err, "Failed to get rest config for manager")
		return err
	}
	return generateWebhookCertsWithClient(kubernetes.NewForConfigOrDie(cfg))
}

// generateWebhookCertsWithClient is the testable core of GenerateWebhookCerts.
func generateWebhookCertsWithClient(kubeClient kubernetes.Interface) error {
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

	// PEM encode the server cert and key
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
	if err = os.MkdirAll(certDir, 0750); err != nil {
		log.Error(err, "Failed to create directory", "Dir", certDir)
		return err
	}
	if err = writeSecureFile(path.Join(certDir, "tls.crt"), certSecret.Data["tls.crt"], 0644); err != nil {
		log.Error(err, "Failed to write tls cert", "Path", path.Join(certDir, "tls.crt"))
		return err
	}

	if err = writeSecureFile(path.Join(certDir, "tls.key"), certSecret.Data["tls.key"], 0600); err != nil {
		log.Error(err, "Failed to write tls key", "Path", path.Join(certDir, "tls.key"))
		return err
	}
	return nil
}

// easSecretCertValid returns true when the easCertSecretName Secret already holds a
// non-expired TLS certificate with at least 7 days of validity remaining.  This is
// used to detect a pre-provisioned certificate (e.g. a VMCA-signed cert placed by an
// admin on WCP) so that GenerateEASCerts does not overwrite it with a new self-signed one.
func easSecretCertValid(kubeClient kubernetes.Interface) bool {
	secret, err := kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), easCertSecretName, v1.GetOptions{})
	if err != nil {
		return false
	}
	certPEM, ok := secret.Data["tls.crt"]
	if !ok || len(certPEM) == 0 {
		return false
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, parseErr := x509.ParseCertificate(block.Bytes)
	if parseErr != nil {
		return false
	}
	// Keep at least 7 days of headroom so the running server is never handed an
	// almost-expired cert after a restart.
	return time.Now().Add(7 * 24 * time.Hour).Before(cert.NotAfter)
}

func GenerateEASCerts() ([]byte, error) {
	cfg, err := GetConfig()
	if err != nil {
		log.Error(err, "Failed to get rest config for EAS cert generation")
		return nil, err
	}
	return generateEASCertsWithClient(kubernetes.NewForConfigOrDie(cfg))
}

// generateEASCertsWithClient is the testable core of GenerateEASCerts.
func generateEASCertsWithClient(kubeClient kubernetes.Interface) ([]byte, error) {
	// If the Secret already contains a valid, non-expired cert (e.g. a VMCA-signed
	// cert pre-provisioned by an admin on WCP), write it to disk and return without
	// generating a new self-signed cert.  This preserves externally-managed certs
	// across pod restarts.
	if easSecretCertValid(kubeClient) {
		secret, err := kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), easCertSecretName, v1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if err = os.MkdirAll(certDir, 0750); err != nil {
			log.Error(err, "Failed to create directory", "Dir", certDir)
			return nil, err
		}
		if err = writeSecureFile(path.Join(certDir, config.EASCertFile), secret.Data["tls.crt"], 0644); err != nil {
			log.Error(err, "Failed to write tls cert", "Path", path.Join(certDir, config.EASCertFile))
			return nil, err
		}
		if err = writeSecureFile(path.Join(certDir, config.EASKeyFile), secret.Data["tls.key"], 0600); err != nil {
			log.Error(err, "Failed to write tls key", "Path", path.Join(certDir, config.EASKeyFile))
			return nil, err
		}
		log.Info("Using existing EAS cert from Secret (pre-provisioned or VMCA-signed)",
			"name", easCertSecretName)
		// Return nil for caCert: the cert is already trusted by the cluster's kube-apiserver
		// (e.g. VMCA root), so no caBundle injection is needed.
		return nil, nil
	}

	// No valid cert found — generate a self-signed cert (works in non-WCP environments
	// where insecureSkipTLSVerify or caBundle injection is permitted on the APIService,
	// or where direct EAS access is tested via port-forward).
	// On WCP, replace this self-signed cert by pre-provisioning a VMCA-signed cert:
	//   kubectl create secret tls nsx-operator-eas-cert \
	//     --cert=eas-vmca.crt --key=eas-vmca.key -n vmware-system-nsx
	log.Info("No valid EAS cert found in Secret; generating self-signed cert " +
		"(on WCP, pre-provision a VMCA-signed cert in Secret '" + easCertSecretName + "')")

	var caPEM, serverCertPEM, serverKeyPEM *bytes.Buffer
	// CA config
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	ca := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"broadcom.com"},
			CommonName:   "eas",
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
		return nil, err
	}

	// Self-signed CA certificate
	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		log.Error(err, "Failed to generate CA")
		return nil, err
	}

	// PEM encode CA cert
	caPEM = new(bytes.Buffer)
	pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	dnsNames := []string{"nsx-eas", "nsx-eas.vmware-system-nsx", "nsx-eas.vmware-system-nsx.svc"}
	commonName := "nsx-eas.vmware-system-nsx.svc"

	serialNumber, err = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
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
		return nil, err
	}

	// sign the server cert
	serverCertBytes, err := x509.CreateCertificate(rand.Reader, cert, ca, &serverKey.PublicKey, caKey)
	if err != nil {
		log.Error(err, "Failed to sign server certificate")
		return nil, err
	}

	// PEM encode the server cert and key
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

	certSecret := &corev1.Secret{
		TypeMeta: v1.TypeMeta{},
		ObjectMeta: v1.ObjectMeta{
			Namespace: namespace,
			Name:      easCertSecretName,
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
				existingSecret, err := kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), easCertSecretName, v1.GetOptions{})
				if err != nil {
					return err
				}

				// Update the existing secret with new data
				existingSecret.Data = certSecret.Data

				_, err = kubeClient.CoreV1().Secrets(namespace).Update(context.TODO(), existingSecret, v1.UpdateOptions{})
				if err != nil {
					log.Error(err, "Failed to update secret", "name", easCertSecretName)
					return err
				}
				log.Info("Secret updated successfully", "name", easCertSecretName)
			} else {
				log.Error(err, "Failed to create secret", "name", easCertSecretName)
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(certDir, 0750); err != nil {
		log.Error(err, "Failed to create directory", "Dir", certDir)
		return nil, err
	}
	if err = writeSecureFile(path.Join(certDir, config.EASCertFile), certSecret.Data["tls.crt"], 0644); err != nil {
		log.Error(err, "Failed to write tls cert", "Path", path.Join(certDir, config.EASCertFile))
		return nil, err
	}

	if err = writeSecureFile(path.Join(certDir, config.EASKeyFile), certSecret.Data["tls.key"], 0600); err != nil {
		log.Error(err, "Failed to write tls key", "Path", path.Join(certDir, config.EASKeyFile))
		return nil, err
	}
	return caPEM.Bytes(), nil
}

func updateWebhookConfig(kubeClient kubernetes.Interface, caCert *bytes.Buffer) error {
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
