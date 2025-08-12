package nbpods

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// FindFirstPodNameByNotebookName returns the first pod (according to alphabet) of the notebook.
// Filter by label: notebook-name=<notebookName> inside the namespace.
// Return error if it doesn't find any pod.
// FindFirstPodNameByNotebookName waits until the notebook has at least 1 pod.
// and then return pod (by name sorting). Expired regard ctx.

func FindFirstPodNameByNotebookName(
	ctx context.Context,
	client kubernetes.Interface,
	notebookName, namespace string,
) (string, error) {

	ls := "notebook-name=" + notebookName

	// Default timeout is 3 minutes if "ctx" has no deadline yet.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
	}

	// simple backoff: 200ms -> x2 to maximum 2s
	delay := 200 * time.Millisecond
	const maxDelay = 2 * time.Second

	for {
		podList, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: ls,
		})
		if err != nil {
			// error API (network, RBAC, ...) -> return error
			return "", err
		}

		if len(podList.Items) > 0 {
			// get the first pod according to notebook name and namespace
			sort.Slice(podList.Items, func(i, j int) bool {
				return podList.Items[i].Name < podList.Items[j].Name
			})
			return podList.Items[0].Name, nil
		}

		// If the system has no pod yet -> wait for the next iteration until ctx is cancel/timeout
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
			// increase delay time for the next iteration
			if delay < maxDelay {
				delay *= 2
				if delay > maxDelay {
					delay = maxDelay
				}
			}
		}
	}
}

// WaitPodReady waits for Pod until it becomes Ready.
// Returns "nil" when Pod ready.
// Returns "error" if timeout or pod is in terminal states: Failed/Suceeded or Deleted.
func WaitPodReady(
	ctx context.Context,
	client kubernetes.Interface,
	namespace, podName string,
	timeout time.Duration,
) error {
	// Add default deadline if ctx has no deadline yet.
	if _, ok := ctx.Deadline(); !ok && timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	interval := time.Second

	return wait.PollUntilContextCancel(ctx, interval, true, func(ctx context.Context) (bool, error) {
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			// If pod is newly created or recreated -> continue for waiting
			return false, nil
		}
		if err != nil {
			return false, err
		}

		// If pod is marked for deletion, return as an error
		if pod.DeletionTimestamp != nil {
			return false, fmt.Errorf("pod %q is being deleted", pod.Name)
		}

		// If pod is in terminal phase -> fail
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			return false, fmt.Errorf("pod %q reached terminal phase %s", pod.Name, pod.Status.Phase)
		}

		// If pod is Ready
		if isPodReady(pod) {
			return true, nil
		}
		return false, nil
	})
}

// isPodReady: if Running and PodReady=True
func isPodReady(p *corev1.Pod) bool {
	if p.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
