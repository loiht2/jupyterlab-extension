package switcher

import (
	nbpods "backend-handler/get-nbpods-name"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Switcher clones a Kubeflow Notebook <podName> in <podNamespace> into <podName>-gpu,
// and injects GPU resources. The GPU resource key is loaded from a ConfigMap
// so you can switch types later without changing code.
//   - ConfigMap name: "gpu-switcher-config" (in the same namespace)
//   - Key: "gpuResourceKey"
//   - Default if missing: "nvidia.com/gpu"
//
// GPU count is set to 1 by default (requests = limits = 1).
func SwitcherToGPU(notebookName, notebookNamespace string) (string, error) {
	apiCtx, apiCancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer apiCancel()

	cfg, err := buildConfig()
	if err != nil {
		return "", fmt.Errorf("build kube config: %w", err)
	}

	// Clients
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("dynamic client: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("k8s clientset: %w", err)
	}

	// 1) Get GPU resource key from ConfigMap
	gpuKey := "nvidia.com/gpu"
	gpuNumstr := "1"
	if cmKey01, cmKey02, err := loadGPUResourceKey(apiCtx, cs, notebookNamespace); err == nil && cmKey01 != "" && cmKey02 != "" {
		gpuKey = cmKey01
		gpuNumstr = cmKey02
	}
	gpuNum, err := strconv.Atoi(gpuNumstr)
	fmt.Printf("gpuNum Type: %T", gpuNum)
	if err != nil {
		fmt.Printf("cannot convert from string to int: %v", err)
	}
	// 2) Get source Notebook
	gvr := schema.GroupVersionResource{Group: "kubeflow.org", Version: "v1", Resource: "notebooks"}
	src, err := dc.Resource(gvr).Namespace(notebookNamespace).Get(apiCtx, notebookName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get source notebook %q: %w", notebookName, err)
	}

	// 3) Build clone object
	dst := src.DeepCopy()
	dstName := setNameGPU(notebookName)
	// dstName := notebookName + "-gpu"
	if err := cleanupMetadata(dst, dstName); err != nil {
		return "", fmt.Errorf("cleanup metadata: %w", err)
	}
	removeStatus(dst)

	// 4) Inject GPU (1 GPU) using key from ConfigMap
	if err := ensureGPUResourcesWithKey(dst, gpuKey, 1); err != nil {
		return "", fmt.Errorf("inject gpu resources: %w", err)
	}

	// 5) Create the new Notebook
	if _, err := dc.Resource(gvr).Namespace(notebookNamespace).Create(apiCtx, dst, metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("create notebook %q and error: %w", dstName, err)
	}

	// 6) Handle new notebook pod
	// Create its own ctx which lasts 5 minutes for waiting
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer waitCancel()
	NewNotebookPodName, err := nbpods.FindFirstPodNameByNotebookName(waitCtx, cs, dstName, notebookNamespace)
	if err != nil {
		fmt.Printf("%v", err)
	}
	fmt.Printf("New notebook pod name: %v is created\n", NewNotebookPodName)

	if err := nbpods.WaitPodReady(waitCtx, cs, notebookNamespace, NewNotebookPodName, 5*time.Minute); err != nil {
		// return pod name and error
		return NewNotebookPodName, err
	}
	fmt.Printf("New notebook pod %v is Ready now!\n", NewNotebookPodName)

	// 7) Waits for 15 mintues before deleting old notebook pod
	time.Sleep(15 * time.Second)
	delCtx, delCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer delCancel()

	// PropagationBackground for quick delete, immediate returns result, related resources when will be deleted in background
	// PropagationForeground for normal delete, wil wait for successful deletion
	policy := metav1.DeletePropagationForeground
	if err := dc.Resource(gvr).Namespace(notebookNamespace).Delete(
		delCtx,
		notebookName,
		metav1.DeleteOptions{PropagationPolicy: &policy},
	); err != nil {
		return NewNotebookPodName, fmt.Errorf("delete old notebook %q: %w", notebookName, err)
	}
	fmt.Printf("Requested deletion of old notebook %s/%s (background propagation)\n", notebookNamespace, notebookName)

	return NewNotebookPodName, nil
}

func SwitcherToCPU(notebookName, notebookNamespace string) (string, error) {
	apiCtx, apiCancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer apiCancel()

	cfg, err := buildConfig()
	if err != nil {
		return "", fmt.Errorf("build kube config: %w", err)
	}

	// Clients
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("dynamic client: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("k8s clientset: %w", err)
	}

	// 1) Get GPU resource key from ConfigMap
	gpuKey := "nvidia.com/gpu"
	if cmKey01, cmKey02, err := loadGPUResourceKey(apiCtx, cs, notebookNamespace); err == nil && cmKey01 != "" && cmKey02 != "" {
		gpuKey = cmKey01
	}

	// 2) Get source Notebook
	gvr := schema.GroupVersionResource{Group: "kubeflow.org", Version: "v1", Resource: "notebooks"}
	src, err := dc.Resource(gvr).Namespace(notebookNamespace).Get(apiCtx, notebookName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get source notebook %q: %w", notebookName, err)
	}

	// 3) Build clone object
	dst := src.DeepCopy()
	dstName := setNameCPU(notebookName)
	// dstName := notebookName + "-gpu"
	if err := cleanupMetadata(dst, dstName); err != nil {
		return "", fmt.Errorf("cleanup metadata: %w", err)
	}
	removeStatus(dst)

	// 4) Delete GPU resources from configuration using key from ConfigMap
	if err := removeGPUResourcesWithKey(dst, gpuKey); err != nil {
		return "", fmt.Errorf("remove gpu resources: %w", err)
	}

	// 5) Create the new Notebook
	if _, err := dc.Resource(gvr).Namespace(notebookNamespace).Create(apiCtx, dst, metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("create notebook %q and error: %w", dstName, err)
	}

	// 6) Handle new notebook pod
	// Create its own ctx which lasts 5 minutes for waiting
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer waitCancel()
	NewNotebookPodName, err := nbpods.FindFirstPodNameByNotebookName(waitCtx, cs, dstName, notebookNamespace)
	if err != nil {
		fmt.Printf("%v", err)
	}
	fmt.Printf("New notebook pod name: %v is created\n", NewNotebookPodName)

	if err := nbpods.WaitPodReady(waitCtx, cs, notebookNamespace, NewNotebookPodName, 5*time.Minute); err != nil {
		// return pod name and error
		return NewNotebookPodName, err
	}
	fmt.Printf("New notebook pod %v is Ready now!\n", NewNotebookPodName)

	// 7) Waits for 15 mintues before deleting old notebook pod
	time.Sleep(15 * time.Second)
	delCtx, delCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer delCancel()

	// PropagationBackground for quick delete, immediate returns result, related resources when will be deleted in background
	// PropagationForeground for normal delete, wil wait for successful deletion
	policy := metav1.DeletePropagationForeground
	if err := dc.Resource(gvr).Namespace(notebookNamespace).Delete(
		delCtx,
		notebookName,
		metav1.DeleteOptions{PropagationPolicy: &policy},
	); err != nil {
		return NewNotebookPodName, fmt.Errorf("delete old notebook %q: %w", notebookName, err)
	}
	fmt.Printf("Requested deletion of old notebook %s/%s (background propagation)\n", notebookNamespace, notebookName)

	return NewNotebookPodName, nil
}

func buildConfig() (*rest.Config, error) {
	// Prefer in-cluster when running inside a Pod
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	// Fallback to KUBECONFIG for local/dev
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return clientcmd.BuildConfigFromFlags("", v)
	}
	home, _ := os.UserHomeDir()
	return clientcmd.BuildConfigFromFlags("", filepath.Join(home, ".kube", "config"))
}

func loadGPUResourceKey(ctx context.Context, cs kubernetes.Interface, ns string) (string, string, error) {
	const (
		cmName  = "gpu-switcher-config"
		cmKey01 = "gpuResourceKey"
		cmKey02 = "numGpuResource"
	)
	cm, err := cs.CoreV1().ConfigMaps(ns).Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	return cm.Data[cmKey01], cm.Data[cmKey02], nil
}

func cleanupMetadata(obj *unstructured.Unstructured, newName string) error {
	obj.SetName(newName)
	obj.SetResourceVersion("")
	obj.SetUID("")
	obj.SetGeneration(0)
	obj.SetManagedFields(nil)

	// scrub nested metadata
	metaAny, ok := obj.Object["metadata"]
	if !ok {
		return nil
	}
	meta, ok := metaAny.(map[string]any)
	if !ok {
		return nil
	}
	delete(meta, "resourceVersion")
	delete(meta, "uid")
	delete(meta, "generation")
	delete(meta, "creationTimestamp")
	delete(meta, "managedFields")
	delete(meta, "ownerReferences")

	// align label app if present
	if labelsAny, ok := meta["labels"]; ok {
		if labels, ok := labelsAny.(map[string]any); ok {
			if _, has := labels["app"]; has {
				labels["app"] = newName
			}
		}
	}
	return nil
}

func removeStatus(obj *unstructured.Unstructured) {
	delete(obj.Object, "status")
}

func ensureGPUResourcesWithKey(obj *unstructured.Unstructured, gpuKey string, gpuCount int) error {
	containers, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if err != nil || !found || len(containers) == 0 {
		return fmt.Errorf("containers not found in Notebook spec: %v", err)
	}
	gpuStr := strconv.Itoa(gpuCount)

	for i := range containers {
		c, ok := containers[i].(map[string]any)
		if !ok {
			return fmt.Errorf("container[%d] has unexpected type", i)
		}
		resources, _ := c["resources"].(map[string]any)
		if resources == nil {
			resources = map[string]any{}
		}
		limits, _ := resources["limits"].(map[string]any)
		if limits == nil {
			limits = map[string]any{}
		}
		requests, _ := resources["requests"].(map[string]any)
		if requests == nil {
			requests = map[string]any{}
		}

		limits[gpuKey] = gpuStr
		requests[gpuKey] = gpuStr

		resources["limits"] = limits
		resources["requests"] = requests
		c["resources"] = resources
		containers[i] = c
	}
	return unstructured.SetNestedSlice(obj.Object, containers, "spec", "template", "spec", "containers")
}

// Remove GPU resources which has key = gpuKey from every conatiner in Notebook (unstructured).
func removeGPUResourcesWithKey(obj *unstructured.Unstructured, gpuKey string) error {
	containers, found, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if err != nil || !found || len(containers) == 0 {
		return fmt.Errorf("containers not found in Notebook spec: %v", err)
	}

	for i := range containers {
		c, ok := containers[i].(map[string]any)
		if !ok {
			return fmt.Errorf("container[%d] has unexpected type", i)
		}

		resources, _ := c["resources"].(map[string]any)
		if resources == nil {
			// không có resources => không cần xoá
			continue
		}

		// Xoá trong limits
		if limits, _ := resources["limits"].(map[string]any); limits != nil {
			delete(limits, gpuKey)
			if len(limits) == 0 {
				delete(resources, "limits")
			} else {
				resources["limits"] = limits
			}
		}

		// Xoá trong requests
		if requests, _ := resources["requests"].(map[string]any); requests != nil {
			delete(requests, gpuKey)
			if len(requests) == 0 {
				delete(resources, "requests")
			} else {
				resources["requests"] = requests
			}
		}

		// Nếu resources trống thì bỏ hẳn
		if len(resources) == 0 {
			delete(c, "resources")
		} else {
			c["resources"] = resources
		}

		containers[i] = c
	}

	return unstructured.SetNestedSlice(obj.Object, containers, "spec", "template", "spec", "containers")
}

func setNameCPU(s string) string {
	i := strings.LastIndex(s, "-")
	if i <= 0 { // if the name does not have '-' or the 1st character is '-'
		s = s + "-cpu"
		return s
	}
	if s[i+1:] != "gpu" {
		s = s + "-cpu"
		return s
	}
	if s[i+1:] == "gpu" {
		s = s[:i]
		s = s[:i] + "-cpu"
		return s
	}
	return s
}

func setNameGPU(s string) string {
	i := strings.LastIndex(s, "-")
	if i <= 0 { // if the name does not have '-' or the 1st character is '-'
		s = s + "-gpu"
		return s
	}
	if s[i+1:] != "cpu" {
		s = s + "-gpu"
		return s
	}
	if s[i+1:] == "cpu" {
		s = s[:i]
		s = s[:i] + "-gpu"
		return s
	}
	return s
}
