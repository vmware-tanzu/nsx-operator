package util

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	admissionregistrationv1client "k8s.io/client-go/kubernetes/typed/admissionregistration/v1"

	"k8s.io/client-go/rest"

	ctrl "sigs.k8s.io/controller-runtime"
)

// Mock types
type mockCoreV1Interface struct {
	typedcorev1.CoreV1Interface
	secretInterface *mockSecretInterface
}

func (m *mockCoreV1Interface) Secrets(namespace string) typedcorev1.SecretInterface {
	return m.secretInterface
}

type mockSecretInterface struct {
	typedcorev1.SecretInterface
	createCalled bool
}

func (m *mockSecretInterface) Create(ctx context.Context, secret *corev1.Secret, opts metav1.CreateOptions) (*corev1.Secret, error) {
	m.createCalled = true
	return secret, nil
}

func TestGenerateWebhookCerts(t *testing.T) {
	// Mock kubernetes.NewForConfigOrDie
	patches := gomonkey.ApplyFunc(kubernetes.NewForConfigOrDie, func(_ *rest.Config) *kubernetes.Clientset {
		return &kubernetes.Clientset{}
	})
	defer patches.Reset()

	// Mock ctrl.GetConfigOrDie
	patches.ApplyFunc(ctrl.GetConfigOrDie, func() *rest.Config {
		return &rest.Config{}
	})

	// Mock os.MkdirAll to avoid permission denied error
	patches.ApplyFunc(os.MkdirAll, func(path string, perm os.FileMode) error {
		// Do nothing and return nil to avoid creating actual directories
		return nil
	})

	// Mock writeSecureFile to avoid writing actual files
	patches.ApplyFunc(writeSecureFile, func(filename string, data []byte, perm os.FileMode) error {
		// Do nothing and return nil to avoid writing actual files
		return nil
	})

	// Create a mock SecretInterface
	mockSecretInterface := &mockSecretInterface{}

	// Mock the CoreV1 method to return our mock interface
	patches.ApplyMethod(reflect.TypeOf(&kubernetes.Clientset{}), "CoreV1",
		func(_ *kubernetes.Clientset) typedcorev1.CoreV1Interface {
			return &mockCoreV1Interface{secretInterface: mockSecretInterface}
		})

	// Mock updateWebhookConfig to do nothing
	patches.ApplyFunc(updateWebhookConfig, func(kubeClient *kubernetes.Clientset, caPEM *bytes.Buffer) error {
		// Do nothing and return nil
		return nil
	})

	// Test
	err := GenerateWebhookCerts()
	if err != nil {
		t.Fatalf("GenerateWebhookCerts failed: %v", err)
	}

	if !mockSecretInterface.createCalled {
		t.Error("Create method was not called")
	}
}

func TestUpdateWebhookConfig(t *testing.T) {
	// Create a mock ValidatingWebhookConfiguration
	mockWebhookConfig := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: validatingWebhookConfiguration,
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "webhook1",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
				},
			},
			{
				Name: "webhook2",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
				},
			},
		},
	}

	// Create a mock clientset
	mockClientset := &kubernetes.Clientset{}

	// Use gomonkey to patch the necessary methods
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	// Mock the Get method
	patches.ApplyMethod(reflect.TypeOf(mockClientset), "AdmissionregistrationV1",
		func(_ *kubernetes.Clientset) admissionregistrationv1client.AdmissionregistrationV1Interface {
			mockAdmissionV1 := &mockAdmissionV1Interface{}
			patches.ApplyMethod(reflect.TypeOf(mockAdmissionV1), "ValidatingWebhookConfigurations",
				func(_ *mockAdmissionV1Interface) admissionregistrationv1client.ValidatingWebhookConfigurationInterface {
					mockValidatingWebhook := &mockValidatingWebhookConfigurationInterface{}
					patches.ApplyMethod(reflect.TypeOf(mockValidatingWebhook), "Get",
						func(_ *mockValidatingWebhookConfigurationInterface, _ context.Context, name string, _ metav1.GetOptions) (*admissionregistrationv1.ValidatingWebhookConfiguration, error) {
							return mockWebhookConfig, nil
						})
					patches.ApplyMethod(reflect.TypeOf(mockValidatingWebhook), "Update",
						func(_ *mockValidatingWebhookConfigurationInterface, _ context.Context, updatedConfig *admissionregistrationv1.ValidatingWebhookConfiguration, _ metav1.UpdateOptions) (*admissionregistrationv1.ValidatingWebhookConfiguration, error) {
							mockWebhookConfig = updatedConfig // Update the mock config
							return updatedConfig, nil
						})
					return mockValidatingWebhook
				})
			return mockAdmissionV1
		})

	// Test case 1: CA bundle needs update
	newCABundle := []byte("new-ca-bundle")
	err := updateWebhookConfig(mockClientset, bytes.NewBuffer(newCABundle))
	if err != nil {
		t.Errorf("updateWebhookConfig returned an error: %v", err)
	}

	// Check if the CABundle was updated
	for _, webhook := range mockWebhookConfig.Webhooks {
		if !bytes.Equal(webhook.ClientConfig.CABundle, newCABundle) {
			t.Errorf("CABundle was not updated. Expected %v, got %v", newCABundle, webhook.ClientConfig.CABundle)
		}
	}

	// Test case 2: CA bundle doesn't need update
	err = updateWebhookConfig(mockClientset, bytes.NewBuffer(newCABundle))
	if err != nil {
		t.Errorf("updateWebhookConfig returned an error: %v", err)
	}
	// No update should occur in this case
}

func TestWriteSecureFile(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "writeSecureFile_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tests := []struct {
		name        string
		filepath    string
		data        []byte
		perm        os.FileMode
		expectError bool
		setupFunc   func() string
		cleanupFunc func(string)
	}{
		{
			name:        "Write file with 0644 permissions",
			data:        []byte("test certificate data"),
			perm:        0644,
			expectError: false,
			setupFunc: func() string {
				return filepath.Join(tempDir, "test_cert.crt")
			},
		},
		{
			name:        "Write file with 0600 permissions (private key)",
			data:        []byte("test private key data"),
			perm:        0600,
			expectError: false,
			setupFunc: func() string {
				return filepath.Join(tempDir, "test_key.key")
			},
		},
		{
			name:        "Write to existing file (should overwrite)",
			data:        []byte("new data"),
			perm:        0644,
			expectError: false,
			setupFunc: func() string {
				filePath := filepath.Join(tempDir, "existing_file.txt")
				// Pre-create the file with different content
				err := os.WriteFile(filePath, []byte("old data"), 0600)
				if err != nil {
					t.Fatalf("Failed to setup existing file: %v", err)
				}
				return filePath
			},
		},
		{
			name:        "Write with empty data",
			data:        []byte(""),
			perm:        0644,
			expectError: false,
			setupFunc: func() string {
				return filepath.Join(tempDir, "empty_file.txt")
			},
		},
		{
			name:        "Write to invalid path (non-existent directory)",
			data:        []byte("test data"),
			perm:        0644,
			expectError: true,
			setupFunc: func() string {
				return filepath.Join(tempDir, "nonexistent", "file.txt")
			},
		},
		{
			name:        "Write to read-only directory",
			data:        []byte("test data"),
			perm:        0644,
			expectError: true,
			setupFunc: func() string {
				roDir := filepath.Join(tempDir, "readonly")
				err := os.Mkdir(roDir, 0755)
				if err != nil {
					t.Fatalf("Failed to create readonly dir: %v", err)
				}
				// Make the directory read-only
				err = os.Chmod(roDir, 0444)
				if err != nil {
					t.Fatalf("Failed to make dir readonly: %v", err)
				}
				return filepath.Join(roDir, "file.txt")
			},
			cleanupFunc: func(path string) {
				// Restore write permissions for cleanup
				dir := filepath.Dir(path)
				os.Chmod(dir, 0755)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var filePath string
			if tt.setupFunc != nil {
				filePath = tt.setupFunc()
			} else {
				filePath = tt.filepath
			}

			if tt.cleanupFunc != nil {
				defer tt.cleanupFunc(filePath)
			}

			// Test the function
			err := writeSecureFile(filePath, tt.data, tt.perm)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			// Verify the file was created and has the correct content
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Errorf("Failed to read created file: %v", err)
				return
			}

			if !bytes.Equal(content, tt.data) {
				t.Errorf("File content mismatch. Expected: %s, Got: %s", string(tt.data), string(content))
			}

			// Verify file permissions
			fileInfo, err := os.Stat(filePath)
			if err != nil {
				t.Errorf("Failed to stat file: %v", err)
				return
			}

			actualPerm := fileInfo.Mode().Perm()
			if actualPerm != tt.perm {
				t.Errorf("File permission mismatch. Expected: %v, Got: %v", tt.perm, actualPerm)
			}
		})
	}
}

// Mock types
type mockAdmissionV1Interface struct {
	admissionregistrationv1client.AdmissionregistrationV1Interface
}

type mockValidatingWebhookConfigurationInterface struct {
	admissionregistrationv1client.ValidatingWebhookConfigurationInterface
}
