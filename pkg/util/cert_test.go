package util

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/vmware-tanzu/nsx-operator/pkg/config"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// generateTestCertPEM returns a DER-encoded, PEM-wrapped self-signed certificate
// whose validity window ends at notAfter.
func generateTestCertPEM(t *testing.T, notAfter time.Time) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-cert"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: certBytes}))
	return buf.Bytes()
}

// overrideCertDir redirects cert file writes to a temp directory for the duration
// of the test and restores the original value in a cleanup function.
func overrideCertDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	orig := certDir
	certDir = tmpDir
	t.Cleanup(func() { certDir = orig })
	return tmpDir
}

// ---------------------------------------------------------------------------
// writeSecureFile
// ---------------------------------------------------------------------------

func TestWriteSecureFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "writeSecureFile_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	tests := []struct {
		name        string
		data        []byte
		perm        os.FileMode
		expectError bool
		setupFunc   func() string
		cleanupFunc func(string)
	}{
		{
			name: "Write file with 0644 permissions",
			data: []byte("test certificate data"),
			perm: 0644,
			setupFunc: func() string {
				return filepath.Join(tempDir, "test_cert.crt")
			},
		},
		{
			name: "Write file with 0600 permissions (private key)",
			data: []byte("test private key data"),
			perm: 0600,
			setupFunc: func() string {
				return filepath.Join(tempDir, "test_key.key")
			},
		},
		{
			name: "Write to existing file (should overwrite)",
			data: []byte("new data"),
			perm: 0644,
			setupFunc: func() string {
				filePath := filepath.Join(tempDir, "existing_file.txt")
				require.NoError(t, os.WriteFile(filePath, []byte("old data"), 0600))
				return filePath
			},
		},
		{
			name:      "Write with empty data",
			data:      []byte(""),
			perm:      0644,
			setupFunc: func() string { return filepath.Join(tempDir, "empty_file.txt") },
		},
		{
			name:        "Write to invalid path (non-existent directory)",
			data:        []byte("test data"),
			perm:        0644,
			expectError: true,
			setupFunc:   func() string { return filepath.Join(tempDir, "nonexistent", "file.txt") },
		},
		{
			name:        "Write to read-only directory",
			data:        []byte("test data"),
			perm:        0644,
			expectError: true,
			setupFunc: func() string {
				roDir := filepath.Join(tempDir, "readonly")
				require.NoError(t, os.Mkdir(roDir, 0755))
				require.NoError(t, os.Chmod(roDir, 0444))
				return filepath.Join(roDir, "file.txt")
			},
			cleanupFunc: func(path string) {
				os.Chmod(filepath.Dir(path), 0755)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := tt.setupFunc()
			if tt.cleanupFunc != nil {
				defer tt.cleanupFunc(filePath)
			}

			err := writeSecureFile(filePath, tt.data, tt.perm)
			if tt.expectError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			content, err := os.ReadFile(filePath)
			require.NoError(t, err)
			assert.Equal(t, tt.data, content)

			fi, err := os.Stat(filePath)
			require.NoError(t, err)
			assert.Equal(t, tt.perm, fi.Mode().Perm())
		})
	}
}

// ---------------------------------------------------------------------------
// updateWebhookConfig
// ---------------------------------------------------------------------------

func TestUpdateWebhookConfig_UpdatesCABundle(t *testing.T) {
	webhookCfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: validatingWebhookConfiguration},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "w1", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: []byte("old")}},
			{Name: "w2", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: []byte("old")}},
		},
	}
	fakeClient := kubefake.NewSimpleClientset(webhookCfg)

	newCA := bytes.NewBufferString("new-ca-bundle")
	require.NoError(t, updateWebhookConfig(fakeClient, newCA))

	updated, err := fakeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().
		Get(context.TODO(), validatingWebhookConfiguration, metav1.GetOptions{})
	require.NoError(t, err)
	for _, wh := range updated.Webhooks {
		assert.Equal(t, []byte("new-ca-bundle"), wh.ClientConfig.CABundle)
	}
}

func TestUpdateWebhookConfig_NoUpdateWhenSame(t *testing.T) {
	existing := []byte("same-ca")
	webhookCfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: validatingWebhookConfiguration},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "w1", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: existing}},
		},
	}
	fakeClient := kubefake.NewSimpleClientset(webhookCfg)

	require.NoError(t, updateWebhookConfig(fakeClient, bytes.NewBuffer(existing)))
}

func TestUpdateWebhookConfig_NotFound(t *testing.T) {
	fakeClient := kubefake.NewSimpleClientset() // webhook config not pre-loaded
	err := updateWebhookConfig(fakeClient, bytes.NewBufferString("ca"))
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// easSecretCertValid
// ---------------------------------------------------------------------------

func TestEasSecretCertValid_SecretNotFound(t *testing.T) {
	assert.False(t, easSecretCertValid(kubefake.NewSimpleClientset()))
}

func TestEasSecretCertValid_NoCertData(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: easCertSecretName, Namespace: namespace},
		Data:       map[string][]byte{},
	}
	assert.False(t, easSecretCertValid(kubefake.NewSimpleClientset(secret)))
}

func TestEasSecretCertValid_InvalidPEM(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: easCertSecretName, Namespace: namespace},
		Data:       map[string][]byte{"tls.crt": []byte("not-a-pem")},
	}
	assert.False(t, easSecretCertValid(kubefake.NewSimpleClientset(secret)))
}

func TestEasSecretCertValid_ExpiredCert(t *testing.T) {
	certPEM := generateTestCertPEM(t, time.Now().Add(-time.Hour))
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: easCertSecretName, Namespace: namespace},
		Data:       map[string][]byte{"tls.crt": certPEM},
	}
	assert.False(t, easSecretCertValid(kubefake.NewSimpleClientset(secret)))
}

func TestEasSecretCertValid_AlmostExpired(t *testing.T) {
	// 3 days < 7-day headroom required
	certPEM := generateTestCertPEM(t, time.Now().Add(3*24*time.Hour))
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: easCertSecretName, Namespace: namespace},
		Data:       map[string][]byte{"tls.crt": certPEM},
	}
	assert.False(t, easSecretCertValid(kubefake.NewSimpleClientset(secret)))
}

func TestEasSecretCertValid_ValidCert(t *testing.T) {
	certPEM := generateTestCertPEM(t, time.Now().Add(30*24*time.Hour))
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: easCertSecretName, Namespace: namespace},
		Data:       map[string][]byte{"tls.crt": certPEM},
	}
	assert.True(t, easSecretCertValid(kubefake.NewSimpleClientset(secret)))
}

// ---------------------------------------------------------------------------
// generateWebhookCertsWithClient
// ---------------------------------------------------------------------------

func TestGenerateWebhookCerts_Success(t *testing.T) {
	tmpDir := overrideCertDir(t)

	webhookCfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: validatingWebhookConfiguration},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "w1", ClientConfig: admissionregistrationv1.WebhookClientConfig{}},
		},
	}
	fakeClient := kubefake.NewSimpleClientset(webhookCfg)

	require.NoError(t, generateWebhookCertsWithClient(fakeClient))

	assert.FileExists(t, filepath.Join(tmpDir, "tls.crt"))
	assert.FileExists(t, filepath.Join(tmpDir, "tls.key"))
}

func TestGenerateWebhookCerts_SecretAlreadyExists(t *testing.T) {
	tmpDir := overrideCertDir(t)

	// Pre-create the Secret so the fake client returns AlreadyExists on Create
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: certName, Namespace: namespace},
		Data:       map[string][]byte{"tls.crt": []byte("old"), "tls.key": []byte("old")},
	}
	webhookCfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: validatingWebhookConfiguration},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "w1", ClientConfig: admissionregistrationv1.WebhookClientConfig{}},
		},
	}
	fakeClient := kubefake.NewSimpleClientset(existingSecret, webhookCfg)

	require.NoError(t, generateWebhookCertsWithClient(fakeClient))

	assert.FileExists(t, filepath.Join(tmpDir, "tls.crt"))
}

// ---------------------------------------------------------------------------
// generateEASCertsWithClient
// ---------------------------------------------------------------------------

func TestGenerateEASCerts_GenerateNew(t *testing.T) {
	tmpDir := overrideCertDir(t)

	fakeClient := kubefake.NewSimpleClientset()

	caCert, err := generateEASCertsWithClient(fakeClient)
	require.NoError(t, err)
	assert.NotNil(t, caCert, "new cert generation must return non-nil caBundle")

	assert.FileExists(t, filepath.Join(tmpDir, config.EASCertFile))
	assert.FileExists(t, filepath.Join(tmpDir, config.EASKeyFile))
}

func TestGenerateEASCerts_SecretAlreadyExists(t *testing.T) {
	tmpDir := overrideCertDir(t)

	// Pre-create the Secret so Create returns AlreadyExists → Update path
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: easCertSecretName, Namespace: namespace},
		Data:       map[string][]byte{"tls.crt": []byte("old"), "tls.key": []byte("old")},
	}
	fakeClient := kubefake.NewSimpleClientset(existingSecret)

	caCert, err := generateEASCertsWithClient(fakeClient)
	require.NoError(t, err)
	assert.NotNil(t, caCert, "AlreadyExists path must still return caBundle")

	assert.FileExists(t, filepath.Join(tmpDir, config.EASCertFile))
}

func TestGenerateEASCerts_PreExistingValidCert(t *testing.T) {
	tmpDir := overrideCertDir(t)

	certPEM := generateTestCertPEM(t, time.Now().Add(30*24*time.Hour))
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: easCertSecretName, Namespace: namespace},
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": []byte("fake-key"),
		},
	}
	fakeClient := kubefake.NewSimpleClientset(secret)

	caCert, err := generateEASCertsWithClient(fakeClient)
	require.NoError(t, err)
	assert.Nil(t, caCert, "pre-existing cert path must return nil caBundle")

	assert.FileExists(t, filepath.Join(tmpDir, config.EASCertFile))
	assert.FileExists(t, filepath.Join(tmpDir, config.EASKeyFile))
}

// ---------------------------------------------------------------------------
// Helpers for error-path tests
// ---------------------------------------------------------------------------

// readOnlyCertDir sets certDir to a path inside a read-only parent directory so that
// os.MkdirAll fails because the parent directory is not writable.
func readOnlyCertDir(t *testing.T) {
	t.Helper()
	parentDir := t.TempDir()
	require.NoError(t, os.Chmod(parentDir, 0444))
	t.Cleanup(func() { os.Chmod(parentDir, 0755) })
	orig := certDir
	certDir = filepath.Join(parentDir, "certs")
	t.Cleanup(func() { certDir = orig })
}

// writeOnlyCertDir sets certDir to an existing but read-only directory so that
// os.MkdirAll succeeds (directory exists) but writing files inside fails.
func writeOnlyCertDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	orig := certDir
	certDir = tmpDir
	require.NoError(t, os.Chmod(tmpDir, 0444))
	t.Cleanup(func() {
		os.Chmod(tmpDir, 0755)
		certDir = orig
	})
	return tmpDir
}

// prependCreateReactor adds a reactor that makes every Create on "secrets" return err.
func prependCreateReactor(t *testing.T, fakeClient *kubefake.Clientset, err error) {
	t.Helper()
	fakeClient.PrependReactor("create", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, err
	})
}

// prependUpdateReactor adds a reactor that makes every Update on the given resource return err.
func prependUpdateReactor(t *testing.T, fakeClient *kubefake.Clientset, resource string, err error) {
	t.Helper()
	fakeClient.PrependReactor("update", resource, func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, err
	})
}

// ---------------------------------------------------------------------------
// generateWebhookCertsWithClient – AlreadyExists secret update/get error paths
// ---------------------------------------------------------------------------

func TestGenerateWebhookCerts_AlreadyExists_UpdateFails(t *testing.T) {
	// Secret pre-exists → Create returns AlreadyExists → Get succeeds → Update fails.
	overrideCertDir(t)
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: certName, Namespace: namespace},
		Data:       map[string][]byte{},
	}
	fakeClient := kubefake.NewSimpleClientset(existingSecret)
	prependUpdateReactor(t, fakeClient, "secrets", fmt.Errorf("simulated update error"))
	err := generateWebhookCertsWithClient(fakeClient)
	assert.Error(t, err)
}

func TestGenerateWebhookCerts_AlreadyExists_GetFails(t *testing.T) {
	// Secret pre-exists → Create returns AlreadyExists → Get fails.
	overrideCertDir(t)
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: certName, Namespace: namespace},
	}
	fakeClient := kubefake.NewSimpleClientset(existingSecret)
	fakeClient.PrependReactor("get", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated get error")
	})
	err := generateWebhookCertsWithClient(fakeClient)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// generateWebhookCertsWithClient – additional error-path tests
// ---------------------------------------------------------------------------

func TestGenerateWebhookCerts_UpdateWebhookConfigFails(t *testing.T) {
	overrideCertDir(t)
	// No webhook config pre-loaded → updateWebhookConfig returns NotFound → retry exhausts
	fakeClient := kubefake.NewSimpleClientset()
	err := generateWebhookCertsWithClient(fakeClient)
	assert.Error(t, err)
}

func TestGenerateWebhookCerts_CreateSecretFails(t *testing.T) {
	overrideCertDir(t)
	webhookCfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: validatingWebhookConfiguration},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "w1"}},
	}
	fakeClient := kubefake.NewSimpleClientset(webhookCfg)
	prependCreateReactor(t, fakeClient, fmt.Errorf("simulated create error"))
	err := generateWebhookCertsWithClient(fakeClient)
	assert.Error(t, err)
}

func TestGenerateWebhookCerts_MkdirAllFails(t *testing.T) {
	// certDir is under a read-only parent → os.MkdirAll fails
	readOnlyCertDir(t)
	webhookCfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: validatingWebhookConfiguration},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "w1"}},
	}
	fakeClient := kubefake.NewSimpleClientset(webhookCfg)
	err := generateWebhookCertsWithClient(fakeClient)
	assert.Error(t, err)
}

func TestGenerateWebhookCerts_CertWriteFails(t *testing.T) {
	// certDir is read-only → writeSecureFile for tls.crt fails
	writeOnlyCertDir(t)
	webhookCfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: validatingWebhookConfiguration},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "w1"}},
	}
	fakeClient := kubefake.NewSimpleClientset(webhookCfg)
	err := generateWebhookCertsWithClient(fakeClient)
	assert.Error(t, err)
}

func TestGenerateWebhookCerts_KeyWriteFails(t *testing.T) {
	// tls.key file pre-created as read-only → cert write succeeds, key write fails
	tmpDir := overrideCertDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "tls.key"), []byte("old"), 0400))
	webhookCfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: validatingWebhookConfiguration},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "w1"}},
	}
	fakeClient := kubefake.NewSimpleClientset(webhookCfg)
	err := generateWebhookCertsWithClient(fakeClient)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// easSecretCertValid – invalid DER bytes inside valid PEM block
// ---------------------------------------------------------------------------

func TestEasSecretCertValid_InvalidCertBytes(t *testing.T) {
	// PEM decodes successfully but x509.ParseCertificate fails on the inner bytes
	var buf bytes.Buffer
	require.NoError(t, pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: []byte("not-valid-asn1")}))
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: easCertSecretName, Namespace: namespace},
		Data:       map[string][]byte{"tls.crt": buf.Bytes()},
	}
	assert.False(t, easSecretCertValid(kubefake.NewSimpleClientset(secret)))
}

// ---------------------------------------------------------------------------
// generateEASCertsWithClient – pre-existing cert path error tests
// ---------------------------------------------------------------------------

func TestGenerateEASCerts_PreExistingCert_MkdirAllFails(t *testing.T) {
	readOnlyCertDir(t)
	certPEM := generateTestCertPEM(t, time.Now().Add(30*24*time.Hour))
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: easCertSecretName, Namespace: namespace},
		Data:       map[string][]byte{"tls.crt": certPEM, "tls.key": []byte("k")},
	}
	_, err := generateEASCertsWithClient(kubefake.NewSimpleClientset(secret))
	assert.Error(t, err)
}

func TestGenerateEASCerts_PreExistingCert_CertWriteFails(t *testing.T) {
	writeOnlyCertDir(t)
	certPEM := generateTestCertPEM(t, time.Now().Add(30*24*time.Hour))
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: easCertSecretName, Namespace: namespace},
		Data:       map[string][]byte{"tls.crt": certPEM, "tls.key": []byte("k")},
	}
	_, err := generateEASCertsWithClient(kubefake.NewSimpleClientset(secret))
	assert.Error(t, err)
}

func TestGenerateEASCerts_PreExistingCert_KeyWriteFails(t *testing.T) {
	tmpDir := overrideCertDir(t)
	// Pre-create the key file as read-only so cert write succeeds but key write fails
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, config.EASKeyFile), []byte("old"), 0400))
	certPEM := generateTestCertPEM(t, time.Now().Add(30*24*time.Hour))
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: easCertSecretName, Namespace: namespace},
		Data:       map[string][]byte{"tls.crt": certPEM, "tls.key": []byte("k")},
	}
	_, err := generateEASCertsWithClient(kubefake.NewSimpleClientset(secret))
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// generateEASCertsWithClient – new cert path error tests
// ---------------------------------------------------------------------------

func TestGenerateEASCerts_GenerateNew_CreateSecretFails(t *testing.T) {
	overrideCertDir(t)
	fakeClient := kubefake.NewSimpleClientset()
	prependCreateReactor(t, fakeClient, fmt.Errorf("simulated create error"))
	_, err := generateEASCertsWithClient(fakeClient)
	assert.Error(t, err)
}

func TestGenerateEASCerts_GenerateNew_MkdirAllFails(t *testing.T) {
	readOnlyCertDir(t)
	_, err := generateEASCertsWithClient(kubefake.NewSimpleClientset())
	assert.Error(t, err)
}

func TestGenerateEASCerts_GenerateNew_CertWriteFails(t *testing.T) {
	writeOnlyCertDir(t)
	_, err := generateEASCertsWithClient(kubefake.NewSimpleClientset())
	assert.Error(t, err)
}

func TestGenerateEASCerts_GenerateNew_KeyWriteFails(t *testing.T) {
	tmpDir := overrideCertDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, config.EASKeyFile), []byte("old"), 0400))
	_, err := generateEASCertsWithClient(kubefake.NewSimpleClientset())
	assert.Error(t, err)
}

func TestGenerateEASCerts_AlreadyExists_UpdateFails(t *testing.T) {
	overrideCertDir(t)
	// Pre-create the Secret so Create returns AlreadyExists, then Update fails
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: easCertSecretName, Namespace: namespace},
		Data:       map[string][]byte{},
	}
	fakeClient := kubefake.NewSimpleClientset(existingSecret)
	prependUpdateReactor(t, fakeClient, "secrets", fmt.Errorf("simulated update error"))
	_, err := generateEASCertsWithClient(fakeClient)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// updateWebhookConfig – Update failure
// ---------------------------------------------------------------------------

func TestUpdateWebhookConfig_UpdateFails(t *testing.T) {
	webhookCfg := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: validatingWebhookConfiguration},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "w1", ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: []byte("old")}},
		},
	}
	fakeClient := kubefake.NewSimpleClientset(webhookCfg)
	prependUpdateReactor(t, fakeClient, "validatingwebhookconfigurations", fmt.Errorf("update webhook failed"))

	err := updateWebhookConfig(fakeClient, bytes.NewBufferString("new-ca"))
	assert.Error(t, err)
}
