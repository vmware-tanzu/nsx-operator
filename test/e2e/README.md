# NSX Operator E2E Tests

This directory contains end-to-end tests for the NSX Operator.

## Running Tests

### Running All E2E Tests

To run all e2e tests, use the following command:

```bash
e2e=true go test -v github.com/vmware-tanzu/nsx-operator/test/e2e \
  -remote.kubeconfig /root/.kube/config \
  -operator-cfg-path /etc/nsx-ujo/ncp.ini \
  -test.timeout 90m \
  -coverprofile cover-e2e.out \
  -vc-user administrator@vsphere.local \
  -vc-password UWOYj4Ltlsd.*h8T \
  -debug=true
```

### Running Only Inventory Tests

To run only the inventory tests, use the provided script:

```bash
./run_inventory_tests.sh
```

This script will run only the tests in the `test/e2e/tests/inventory` package.

## Test Structure

The e2e tests are organized into the following directories:

- `framework/`: Contains the test framework code
- `clients/`: Contains client implementations for NSX and vCenter
- `tests/`: Contains the actual test implementations
  - `inventory/`: Tests for inventory synchronization
  - `subnet/`: Tests for subnet functionality
  - And other test categories...

## Adding New Tests

When adding new tests, please follow the existing patterns and organize them into the appropriate test category directory.