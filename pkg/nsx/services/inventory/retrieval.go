package inventory

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetPodIDsFromEndpoint(ctx context.Context, c client.Client, name string, namespace string) (podIDs []string, hasAddr bool) {
	// Initialize return values
	podIDs = []string{}
	hasAddr = false

	// Get the endpoint object corresponding to the service
	endpoint := &v1.Endpoints{}
	err := c.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, endpoint)

	if err != nil {
		log.Error(err, "Failed to get endpoints for Service", "Service", name, "Namespace", namespace)
		return
	}

	// Check for addresses in endpoints
	for _, subset := range endpoint.Subsets {
		for _, address := range subset.Addresses {
			hasAddr = true
			if address.TargetRef != nil && (address.TargetRef.Kind == "Pod" || address.TargetRef.Kind == "VirtualMachine") {
				podIDs = append(podIDs, string(address.TargetRef.UID))
			}
		}

		// Even if there are no ready addresses, check for not-ready ones
		// This would indicate the service has endpoints, but they're not ready
		if !hasAddr && len(subset.NotReadyAddresses) > 0 {
			hasAddr = true
		}
	}

	return podIDs, hasAddr
}

func GetPodByUID(ctx context.Context, c client.Client, uid types.UID, namespace string) (*v1.Pod, error) {
	podList := &v1.PodList{}
	if err := c.List(ctx, podList, &client.ListOptions{
		Namespace: namespace,
	}); err != nil {
		return nil, fmt.Errorf("failed to list pods in namespace %s: %v", namespace, err)
	}

	for _, pod := range podList.Items {
		if pod.UID == uid {
			return &pod, nil
		}
	}

	return nil, fmt.Errorf("pod with UID %s not found in namespace %s", uid, namespace)
}

func GetServicesUIDByPodUID(ctx context.Context, c client.Client, podUID types.UID, namespace string) ([]string, error) {
	// One pod can have multiple services, so we need to find all services associated with the pod
	serviceList := &v1.ServiceList{}
	if err := c.List(ctx, serviceList, &client.ListOptions{
		Namespace: namespace,
	}); err != nil {
		return nil, fmt.Errorf("failed to list services in namespace %s: %v", namespace, err)
	}

	var serviceUIDs []string
	for _, svc := range serviceList.Items {
		// Get the endpoint object associated with the service
		endpoints := &v1.Endpoints{}
		err := c.Get(ctx, types.NamespacedName{Name: svc.Name, Namespace: namespace}, endpoints)
		if err != nil {
			log.Error(err, "Failed to get Endpoints", "Name", svc.Name, "Namespace", namespace)
			continue
		}

		// Check if the pod UID is part of the endpoints
		for _, subset := range endpoints.Subsets {
			for _, address := range subset.Addresses {
				if address.TargetRef != nil && address.TargetRef.UID == podUID {
					serviceUIDs = append(serviceUIDs, string(svc.UID))
					break
				}
			}
		}
	}

	if len(serviceUIDs) == 0 {
		return nil, fmt.Errorf("no services found for pod UID %s in namespace %s", podUID, namespace)
	}

	return serviceUIDs, nil
}
