set -o xtrace
set -o errexit
set -o nounset
set -o pipefail

NSX_OPERATOR_DIR='/root/nsx-operator'
NSX_OPERATOR_CI_DIR='/root/nsx-operator-ci'
rm -fr $NSX_OPERATOR_DIR $NSX_OPERATOR_CI_DIR
git clone https://github.com/vmware-tanzu/nsx-operator/ $NSX_OPERATOR_DIR
cd $NSX_OPERATOR_DIR
export GO111MODULE=on
export GOPATH=/root/go
export GOROOT=/usr/local/go
export PATH=$GOROOT/bin:$PATH
go version
go env

# if manual trigger, use the default email and author
if [ -z ${ghprbActualCommitAuthorEmail+x} ]; then
    ghprbActualCommitAuthorEmail="manually_trigger@vmware.com"
fi
if [ -z ${ghprbActualCommitAuthor+x} ]; then
    ghprbActualCommitAuthor="manually_trigger"
fi
git config user.email ${ghprbActualCommitAuthorEmail}
git config user.name ${ghprbActualCommitAuthor}
git remote add pr $ghprbAuthorRepoGitUrl
git fetch pr $ghprbSourceBranch:$ghprbSourceBranch
git checkout $ghprbSourceBranch


# go build -o $NSX_OPERATOR_DIR/bin/manager cmd/main.go
make build
md5sum $NSX_OPERATOR_DIR/bin/manager /etc/vmware/wcp/tls/manager

kubectl scale deployment nsx-ncp -n vmware-system-nsx --replicas=0
sleep 15
hostname=$(hostname)
kubectl patch deployment nsx-ncp -n vmware-system-nsx -p "{\"spec\": {\"template\": {\"spec\": {\"nodeSelector\": {\"kubernetes.io/hostname\": \"$hostname\"}}}}}"
cp $NSX_OPERATOR_DIR/bin/manager /etc/vmware/wcp/tls/
md5sum $NSX_OPERATOR_DIR/bin/manager /etc/vmware/wcp/tls/manager
chmod 777 /etc/vmware/wcp/tls/manager

kubectl apply -f $NSX_OPERATOR_DIR/build/yaml/crd/vpc/
kubectl scale deployment nsx-ncp -n vmware-system-nsx --replicas=1
sleep 35

pod_name=$(kubectl get pods -n  vmware-system-nsx -o jsonpath="{.items[0].metadata.name}")
mkdir -p /etc/nsx-ujo/vc
mkdir -p /etc/ncp/
kubectl exec $pod_name -c nsx-ncp -n vmware-system-nsx -- cat /etc/nsx-ujo/ncp.ini > /etc/ncp.ini
kubectl exec $pod_name -c nsx-ncp -n vmware-system-nsx -- cat /etc/ncp/lb-default.cert > /etc/ncp/lb-default.cert
kubectl exec $pod_name -c nsx-ncp -n vmware-system-nsx -- cat /etc/ncp/lb-default.key > /etc/ncp/lb-default.key
kubectl exec $pod_name -c nsx-ncp -n vmware-system-nsx -- cat  /etc/nsx-ujo/nsx_manager_certificate_0 >  /etc/nsx-ujo/nsx_manager_certificate_0
kubectl exec $pod_name -c nsx-ncp -n vmware-system-nsx -- cat  /var/run/secrets/kubernetes.io/serviceaccount/token > /var/run/secrets/kubernetes.io/serviceaccount/token
kubectl exec $pod_name -c nsx-ncp -n vmware-system-nsx -- cat /var/run/secrets/kubernetes.io/serviceaccount/ca.crt  > /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
kubectl exec $pod_name -c nsx-ncp -n vmware-system-nsx -- cat /etc/nsx-ujo/vc/username > /etc/nsx-ujo/vc/username
kubectl exec $pod_name -c nsx-ncp -n vmware-system-nsx -- cat /etc/nsx-ujo/vc/password > /etc/nsx-ujo/vc/password

cp -r /root/test $NSX_OPERATOR_DIR/
# Use the -run parameter to run only specific test cases within certain modules.
# -run '.*Subnet.*|.*SubnetSet.*|.*SecurityPolicy.*|.*NetworkInfo.*'
e2e=true go test -v ./test/e2e -coverpkg=./pkg/... -remote.kubeconfig /root/.kube/config -operator-cfg-path /etc/ncp.ini -test.timeout 15m -coverprofile cover-e2e.out
