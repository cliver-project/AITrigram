// Package component provides a framework for managing LLMEngine resources using
// the assets pattern inspired by HyperShift control plane v2.
package component

import (
	"context"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LLMEngineComponent is the main interface for LLMEngine components.
// Each component manages one or more Kubernetes resources for a specific
// LLMEngine+ModelRepository pair.
type LLMEngineComponent interface {
	// Name returns the component name (typically "{engine-name}-{model-name}")
	Name() string

	// Reconcile ensures the component's resources match the desired state
	Reconcile(ctx LLMEngineContext) error
}

// LLMEngineContext provides context for component reconciliation.
// This context is used both for the main reconciliation logic and for
// adapter/predicate functions.
type LLMEngineContext struct {
	context.Context

	// Client for creating/updating resources
	Client client.Client

	// Scheme for setting owner references
	Scheme *runtime.Scheme

	// LLMEngine resource being reconciled
	LLMEngine *aitrigramv1.LLMEngine

	// ModelRepository being served by this component
	ModelRepo *aitrigramv1.ModelRepository

	// Namespace where resources are created (same as LLMEngine namespace)
	Namespace string
}
