# AITrigram

A Kubernetes operator for deploying, serving, and continuously improving LLM inference engines.

## What It Does

AITrigram manages the full lifecycle of self-hosted LLMs on Kubernetes:

- **Model management** — Download and version models from HuggingFace or Ollama with automatic storage provisioning
- **Inference serving** — Deploy Ollama or vLLM engines with auto-recovery, GPU support, and multi-model serving
- **Fine-tuning loop** — Run fine-tuning jobs that produce LoRA adapters, then load them back into serving engines so models continuously improve

Remote clients (e.g. LangChain) connect to the deployed engines via standard APIs (Ollama API, OpenAI-compatible API).

## Custom Resources

| Resource | Scope | Purpose |
|---|---|---|
| `ModelRepository` | Cluster | Manages model downloads, storage, and versioning |
| `LLMEngine` | Namespace | Deploys inference engines referencing one or more models |

## Installation

Install with a single YAML file:

```bash
kubectl apply -f https://github.com/cliver-project/AITrigram/releases/latest/download/install.yaml
```

Or build from source:

```bash
make build-installer IMG=ghcr.io/cliver-project/aitrigram-controller:latest
kubectl apply -f dist/install.yaml
```

## Quick Start

```bash
# Create a model repository (downloads the model)
kubectl apply -f config/samples/aitrigram_v1_modelrepository.yaml

# Deploy an inference engine
kubectl apply -f config/samples/aitrigram_v1_llmengine.yaml
```

## Development

```bash
make build          # Build binary
make test           # Run unit tests
make test-e2e       # Run e2e tests (requires Minikube)
make lint           # Lint
make run            # Run controller locally
```

## Requirements

- Kubernetes 1.28+
- Go 1.25+ (for building)

## License

Apache License 2.0 — see [LICENSE](LICENSE).
