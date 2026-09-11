# AGENTS.md — NSX Operator Review Guidelines

## Project Overview
NSX Operator is a Kubernetes controller-runtime based operator that orchestrates VMware NSX networking and security resources (VPCs, Subnets, SubnetPorts, SecurityPolicies, etc.) for Kubernetes workloads.

## High-Impact Architecture & Coding Standards

### 1. Controller & Reconcile Loop
- In all controller `Reconcile` loops, ensure the reconciliation logic is fully idempotent and resilient to partial failures.
- In controller status updates, prefer `client.Status().Patch()` over `client.Status().Update()` to minimize optimistic concurrency conflicts.
- Always propagate `context.Context` to external NSX API calls and Kubernetes client operations; never ignore or drop context cancellation signals.
- In controller error handling, distinguish transient errors (which should return an error to trigger controller re-queuing) from permanent configuration errors (which should update the Custom Resource status conditions and return `nil`).

### 2. Concurrency & Performance
- Never hold mutexes (`sync.Mutex` or `sync.RWMutex`) across external network I/O operations, such as NSX REST API invocations or blocking HTTP client calls.
- Always guard access to shared in-memory stores, maps, or global caches with proper synchronization primitives.

### 3. Resource Lifecycle & Finalizers
- In deletion reconciliation handlers, verify that all underlying NSX backend infrastructure resources are confirmed deleted before removing the Kubernetes Finalizer from the Custom Resource.
- Never log plaintext credentials, tokens, TLS private keys, or sensitive backend infrastructure secrets in controller logs.

### 4. Testing Guidelines
- In end-to-end tests under `test/e2e/`, avoid hardcoded `time.Sleep()` calls; use asynchronous polling with `gomega.Eventually` or `wait.PollUntilContextTimeout` to prevent flaky test execution.
- In test suites, ensure all dynamically created namespaces, Custom Resources, and network ports are registered for teardown in `AfterEach` blocks.
- In unit tests, all external network communications, Kubernetes client calls, and NSX API requests must be properly mocked without making real network calls.

## Prohibited Review Comments (Noise Reduction)
- DO NOT flag coding style, naming conventions, or formatting issues in auto-generated files (matching `zz_generated.*`) or mock packages under `pkg/mock/`.
- DO NOT report theoretical performance issues, memory optimizations, or hardcoded dummy test data in `*_test.go` and `test/e2e/`.
- DO NOT make speculative assumptions about undocumented NSX backend API behavior without explicit code or documentation evidence.
