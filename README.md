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

```yaml
apiVersion: aitrigram.cliver-project.github.io/v1
kind: LLMEngine
metadata:
  name: ollama
  namespace: default
spec:
  engineType: "ollama"
  port: 11434
  servicePort: 8080
---
apiVersion: aitrigram.cliver-project.github.io/v1
kind: ModelRepository
metadata:
  name: llama3
spec:
  modelName: "llama3"
  source:
    type: ollama
  storage:
    type: emptyDir
  supportedEngines:
    - ollama
  autoDownload: true
```
Then, you have 2 replicas of Ollama servers which has the `llama3.2:latest` ready for you to access, and it has a service published too at: `ollama-llama3.default.svc.cluster.local:8080` inside the cluster.

If you want to access it from outside of the cluster, create ingress or route according to your cluster type:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ollama-engine
  annotations:
    # path-rewrite removes the '/ollama' in the path to pass to backend ollama servers.
     haproxy.org/path-rewrite: /ollama/(.*) /\1
  labels:
    name: ollama-engine
spec:
  rules:
  - host: k8s-worker
    http:
      paths:
      - pathType: Prefix
        path: /ollama
        backend:
          service:
            name: ollama-llama3
            port:
              number: 8080
```

Or on openshift:

```yaml
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: ollama-llama3
  namespace: default
spec:
  port:
    targetPort: 8080
  to:
    kind: Service
    name: ollama-llama3

```

#### To Debug

Create a `.vscode/launch.json` file with a configuration to debug:
```json
    "configurations": [
        {
            "name": "Launch Go",
            "type": "go",
            "request": "launch",
            "mode": "debug",
            "console": "integratedTerminal",
            "program": "${file}",
            "args": ["run"]
        }
    ]
```
Debug on the `main.go` to start the controller in you host

Or you can run the main.go directly: `go run cmd/main.go`
