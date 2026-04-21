package k8sclient

import (
	"context"
	"fmt"

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"xmanager_cloud/logging/logging"
)

// checkNamespaceExists verifies if a given namespace already exists in the cluster.
// It returns true if the namespace exists, false if it doesn't, and an error
// for any other issues.
func (c *Client) checkNamespaceExists(ctx context.Context, namespace string) (bool, error) {
	_, err := c.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get namespace %q: %w", namespace, err)
	}
	return true, nil
}

// createNamespace creates a new namespace with standardized labels.
func (c *Client) createNamespace(ctx context.Context, namespace string) error {
	namespaceObj := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				"managed-by": "xmc-actuator",
			},
		},
	}
	_, err := c.clientset.CoreV1().Namespaces().Create(ctx, namespaceObj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create namespace %q: %w", namespace, err)
	}
	return nil
}

// ensureNamespaceExists is the main orchestrator function. It checks for a
// namespace and creates it if it's missing, logging each step of the process.
func (c *Client) ensureNamespaceExists(ctx context.Context, namespace string) error {
	logging.Info(ctx, "Checking if namespace exists...", "namespace", namespace)

	exists, err := c.checkNamespaceExists(ctx, namespace)
	if err != nil {
		logging.Error(ctx, "Could not check for namespace", "namespace", namespace, "error", err)
		return err // Return the wrapped error from the check.
	}
	if exists {
		logging.Info(ctx, "Namespace already exists.", "namespace", namespace)
		return nil
	}

	if !c.RuntimeConfig.CreateNewNamespaces {
		logging.Error(ctx, "Namespace does not exist and namespace creation is disabled.", "namespace", namespace)
		return fmt.Errorf("namespace %q does not exist and namespace creation is disabled", namespace)
	}

	logging.Info(ctx, "Namespace does not exist. Creating it now.", "namespace", namespace)
	if err := c.createNamespace(ctx, namespace); err != nil {
		if apierrors.IsAlreadyExists(err) {
			logging.Warn(ctx, "Namespace was created by another process concurrently. Continuing.", "namespace", namespace)
			return nil
		}
		logging.Error(ctx, "Failed to create namespace", "namespace", namespace, "error", err)
		return err
	}

	logging.Info(ctx, "Successfully created namespace.", "namespace", namespace)
	return nil
}
