package e2e

import (
	"testing"
)

func setupTest(tb testing.TB, testNamespace string) {
	tb.Logf("Creating '%s' K8s Namespace", testNamespace)
	if err := testData.createNamespace(testNamespace); err != nil {
		tb.Fatalf("Error when setting up test: %v", err)
	}
}

func teardownTest(tb testing.TB, testNamespace string) {
	tb.Logf("Deleting '%s' K8s Namespace", testNamespace)
	if err := testData.deleteNamespace(testNamespace, defaultTimeout); err != nil {
		tb.Fatalf("Error when tearing down test: %v", err)
	}
}
