package util

import (
	"bytes"
	"context"
	"os"
	"path"
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

func TestWriteFile(t *testing.T) {
	tempDir := t.TempDir()
	testFile := path.Join(tempDir, "test.txt")
	testContent := []byte("test content")

	err := writeFile(testFile, testContent)
	if err != nil {
		t.Fatalf("writeFile failed: %v", err)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}

	if string(content) != string(testContent) {
		t.Errorf("File content mismatch. Expected %s, got %s", testContent, content)
	}
}

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

	// You can add more specific assertions here, e.g., check if the secret was "created" correctly
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

// Mock types
type mockAdmissionV1Interface struct {
	admissionregistrationv1client.AdmissionregistrationV1Interface
}

type mockValidatingWebhookConfigurationInterface struct {
	admissionregistrationv1client.ValidatingWebhookConfigurationInterface
}
