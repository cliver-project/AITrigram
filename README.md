# AITrigram
A kubernetes operator on LLMs serving.

## Description
With the operator, users can define their own `LLMEngine` `ModelRepository` to k8s cluster to serve the LLM.

## Getting Started

### Prerequisites
- go version v1.23.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

## To Deploy on the cluster

**Using the installer**

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/cliver-project/AITrigram/main/dist/install.yaml
```

### Deploy LLM Servings

Here's a minimal example to deploy DeepSeek-R1 with vLLM:

```yaml
---
# Step 1: Create the ModelRepository
apiVersion: aitrigram.cliver-project.github.io/v1
kind: ModelRepository
metadata:
  name: deepseek-r1-7b
spec:
  source:
    origin: huggingface
    modelId: deepseek-ai/DeepSeek-R1-Distill-Qwen-7B
  storage:
    path: /data/models
    emptyDir: {}
  autoDownload: true

---
# Step 2: Create the LLMEngine to serve the model
apiVersion: aitrigram.cliver-project.github.io/v1
kind: LLMEngine
metadata:
  name: deepseek-r1-engine
spec:
  engineType: vllm
  modelRefs:
    - deepseek-r1-7b
  replicas: 1
```

This will create a vLLM deployment serving the DeepSeek-R1 model. The model will be automatically downloaded from HuggingFace and served at `deepseek-r1-engine-deepseek-r1-7b.default.svc.cluster.local:8000` inside the cluster.

For more examples including Ollama, GPU configurations, and advanced settings, check the [config/samples/](config/samples/) directory.

