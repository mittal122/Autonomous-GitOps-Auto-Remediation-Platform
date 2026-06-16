// Package remediator provides concrete implementations of contracts.RemediationAction.
// Every action commits a YAML change to the GitOps repository; no Kubernetes API calls
// are made. The cluster reconciles via ArgoCD after the commit is pushed.
package remediator

import "github.com/autosre/agent/internal/contracts"

// Compile-time checks: all three actions must satisfy the interface.
var (
	_ contracts.RemediationAction = (*RollbackDeployment)(nil)
	_ contracts.RemediationAction = (*ScaleDeployment)(nil)
	_ contracts.RemediationAction = (*BumpMemoryLimit)(nil)
)
