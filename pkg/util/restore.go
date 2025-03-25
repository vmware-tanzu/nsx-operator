package util

import (
	"context"
	"fmt"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	commonctl "github.com/vmware-tanzu/nsx-operator/pkg/controllers/common"
	"github.com/vmware-tanzu/nsx-operator/pkg/nsx"
)

var (
	NSXRestoreStatus         = "nsx-restore-status"
	AnnotationRestoreEndTime = "operator_restore_end_time"
	AnnotationForceRestore   = "force_restore"
	RestoreStatusInitial     = "INITIAL"
	RestoreStatusSuccess     = "SUCCESS"
)

func CompareNSXRestore(k8sClient client.Client, nsxClient *nsx.Client) (bool, error) {
	ctx := context.TODO()
	gvk := schema.GroupVersionKind{
		Group:   "nsx.vmware.com",
		Version: "v1",
		Kind:    "NCPConfig",
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: NSXRestoreStatus}, obj); err != nil {
		if apierrors.IsNotFound(err) {
			obj.SetName(NSXRestoreStatus)
			obj.SetAnnotations(map[string]string{AnnotationRestoreEndTime: "-1"})
			log.Info("ncpconfig not exists, create ncpconfig for restore", "ncpconfig", obj)
			// create may fail due to concurrent create with ncp, restart in this case
			if err = k8sClient.Create(ctx, obj); err != nil {
				return false, fmt.Errorf("failed to create %s: %w", NSXRestoreStatus, err)
			}
		} else {
			return false, fmt.Errorf("failed to get %s: %w", NSXRestoreStatus, err)
		}
	}
	annotations := obj.GetAnnotations()

	forceRestoreStr, ok := annotations[AnnotationForceRestore]
	if ok {
		forceRestore, err := strconv.ParseBool(forceRestoreStr)
		if err != nil {
			log.Error(err, "Failed to parse force restore from ncpconfig", "forceRestore", forceRestoreStr)
		} else if forceRestore {
			log.Info("Force restore trigger for testing case")
			return true, nil
		}
	}

	lastEndTimeStr, ok := annotations[AnnotationRestoreEndTime]
	if !ok {
		lastEndTimeStr = "-1"
		annotations[AnnotationRestoreEndTime] = lastEndTimeStr
		obj.SetAnnotations(annotations)
		log.Info("Operator restore timestamp not exists, update ncpconfig for operator", "ncpconfig", obj)
		if err := k8sClient.Update(ctx, obj); err != nil {
			return false, err
		}
	}
	lastEndTime, err := strconv.ParseInt(lastEndTimeStr, 10, 64)
	if err != nil {
		return false, fmt.Errorf("failed to parse last end time from %s: %w", NSXRestoreStatus, err)
	}

	restoreStatus, err := nsxClient.StatusClient.Get(nil)
	if err != nil {
		return false, fmt.Errorf("failed to get NSX restore status: %w", err)
	}
	log.Info("Get restore status from NSX", "restoreStatus", restoreStatus)
	if *restoreStatus.Status.Value == RestoreStatusInitial {
		return false, nil
	} else if *restoreStatus.Status.Value != RestoreStatusSuccess {
		return false, fmt.Errorf("NSX restore not succeeds with status %s", *restoreStatus.Status.Value)
	}
	endTime := *restoreStatus.RestoreEndTime
	if lastEndTime < endTime {
		return true, nil
	}
	return false, nil
}

func updateRestoreEndTime(k8sClient client.Client) error {
	ctx := context.TODO()
	gvk := schema.GroupVersionKind{
		Group:   "nsx.vmware.com",
		Version: "v1",
		Kind:    "NCPConfig",
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)

	return retry.OnError(retry.DefaultBackoff, func(err error) bool {
		return err != nil
	}, func() error {
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: NSXRestoreStatus}, obj); err != nil {
			return err
		}
		annotations := obj.GetAnnotations()
		annotations[AnnotationRestoreEndTime] = strconv.Itoa(int(time.Now().UnixMilli()))
		obj.SetAnnotations(annotations)
		return k8sClient.Update(ctx, obj)
	})
}

func ProcessRestore(reconcilerList []commonctl.ReconcilerProvider, client client.Client) error {
	log.Info("Enter restore mode")
	var errList []error
	// Collect Garbage from overlay to underlay
	for i := len(reconcilerList) - 1; i >= 0; i-- {
		if reconcilerList[i] != nil {
			if err := reconcilerList[i].CollectGarbage(context.TODO()); err != nil {
				errList = append(errList, err)
			}
		}
	}
	if len(errList) > 0 {
		return fmt.Errorf("failed to collect garbage: %v", errList)
	}
	log.Info("Garbage collection succeeds in restore mode")
	// Restore resource from underlay to overlay
	for _, reconciler := range reconcilerList {
		if reconciler != nil {
			if err := reconciler.RestoreReconcile(); err != nil {
				errList = append(errList, err)
			}
		}
	}
	if len(errList) > 0 {
		return fmt.Errorf("failed to restore resources: %v", errList)
	}
	log.Info("Restore reconcile succeeds in restore mode")

	if err := updateRestoreEndTime(client); err != nil {
		return fmt.Errorf("failed to update restore end time: %w", err)
	}
	return nil
}
