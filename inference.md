# LLM Inference setups.

In this project, there will be 2 CRDs:

* `LLMEngine`: a server that accpets client requests and do the LLM inference then returns the response.
   - `Ollama`: A server that easily starts with small LLMs. It has GPU support as well.

* `ModelRepository`: A CRD which represents the LLM model, which engine will be running on, and others.

The followings describe the setups on the scope of the direct `LLMEngine` and `ModelRepository` serving without consideration of autoscalling, node affinity, model downloading, etc.


## LLMEngine with CPU

```mermaid
---
title: LLMEngine with CPU
---
graph LR
    U[User Requests] -- in sequence / in batch --> A[LLMEngine]
    A -- Load LLM --> B[CPU]
```

## LLMEngine with a single GPU

```mermaid
---
title: LLMEngine with single GPU
---
graph LR
    U[User Requests] -- in sequence / in batch --> A[LLMEngine]
    A -- Load LLM --> B[GPU]
```

> NOTE: users can specify which GPU to use in case there are multiple GPUs available: `CUDA_VISIBLE_DEVICES: 0` for the first one, etc. `ROCR_VISIBLE_DEVICES: 0` for AMD GPU cards.

## LLMEngine with multiple GPUs (MP)

```mermaid
---
title: LLMEngine with multiple GPUs (MP)
---
graph LR
    U[User Requests] -- in sequence / in batch --> A[LLMEngine]
    A -- Load LLM --> B1[GPU1]
    A -- Load LLM --> 2[GPU2]
```

> NOTE: by default, each LLMEngine only uses one GPU, the other will be idle. so it needs the LLMEngine to support to use multiple GPUs, typically called: model parallelism (MP).


> NOTE: It woulbe the best pratice to bind each GPU with each `ModelRepository` serving. For large model that one GPU cannot load, uses Model Parallelism (MP) to load it using multiple GPUs, and we can set up a Data Parallelism before it to distribute the requests.

## DP and MP

```mermaid
---
title: Data Parallelism and Model Parallelism
---
graph LR
    U1[User Requests] -- Concurrent --> A[Distributed DP]
    U2[User Requests] -- Concurrent --> A
    A -- data part 1 --> B1[MP For Big Model A]
    A -- data part 2 --> B2[MP For Big Model A]
    B1 -- Model A Part 1 --> C1[GPU 0]
    B1 -- Model A Part 2 --> C2[GPU 1]
    B2 -- Model A Part 1 --> C3[GPU 2]
    B2 -- Model A Part 2 --> C4[GPU 3]
```

> NOTE: in the above setup, the same `Big Model A` gets loaded by 2 GPUs, and there are 2 replicas.
