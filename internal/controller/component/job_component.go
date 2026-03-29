// Package component provides a framework for managing ModelRepository job resources
// using the same pattern as LLMEngine components.
package component

import (
	"context"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/cliver-project/AITrigram/internal/controller/storage"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ModelRepositoryJobComponent is the main interface for ModelRepository job components.
// Each component manages a specific job type (download or cleanup) for a ModelRepository.
type ModelRepositoryJobComponent interface {
	// Name returns the component name (typically the job name)
	Name() string

	// Reconcile ensures the component's resources match the desired state
	Reconcile(ctx ModelRepositoryJobContext) error
}

// ModelRepositoryJobContext provides context for job component reconciliation.
// This context is used both for the main reconciliation logic and for
// adapter/predicate functions.
type ModelRepositoryJobContext struct {
	context.Context

	// Client for creating/updating resources
	Client client.Client

	// Scheme for setting owner references
	Scheme *runtime.Scheme

	// ModelRepository resource being reconciled
	ModelRepo *aitrigramv1.ModelRepository

	// Storage provider for the ModelRepository
	Provider *storage.StorageClassProvider

	// RevisionRef if dealing with a specific revision (optional)
	RevisionRef *aitrigramv1.RevisionReference

	// JobName for the job being created/updated
	JobName string

	// Namespace where resources are created
	Namespace string

	// CustomImage to override the default job image (optional)
	CustomImage string

	// CustomScript to override the default job script (optional)
	CustomScript string
}

// JobType represents the type of job (download or cleanup).
type JobType string

const (
	// JobTypeDownload represents a download job
	JobTypeDownload JobType = "download"
	// JobTypeCleanup represents a cleanup job
	JobTypeCleanup JobType = "cleanup"
)
