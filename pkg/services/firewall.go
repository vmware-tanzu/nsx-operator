package services

import (
	"sync"

	"k8s.io/client-go/tools/cache"

	"github.com/vmware-tanzu/nsx-operator/pkg/controllers"
	"github.com/vmware-tanzu/nsx-operator/pkg/util"
)

// InitializeSecurityPolicy sync NSX resources
func InitializeSecurityPolicy(r *controllers.SecurityPolicyReconciler) error {
	wg := sync.WaitGroup{}
	wgDone := make(chan bool)
	fatalErrors := make(chan error)

	wg.Add(3)

	r.GroupStore = cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeNamespace: namespaceIndexFunc})
	r.SecurityPolicyStore = cache.NewIndexer(keyFunc, cache.Indexers{util.TagScopeSecurityPolicyCRUID: securityPolicyCRUIDScopeIndexFunc})
	r.RuleStore = cache.NewIndexer(keyFunc, cache.Indexers{})

	go queryGroup(r, &wg, fatalErrors)
	go querySecurityPolicy(r, &wg, fatalErrors)
	go queryRule(r, &wg, fatalErrors)

	go func() {
		wg.Wait()
		close(wgDone)
	}()

	select {
	case <-wgDone:
		break
	case err := <-fatalErrors:
		close(fatalErrors)
		return err
	}

	return nil
}
