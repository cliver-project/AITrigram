/*
Copyright 2025 Red Hat, Inc.

Authors: Lin Gao <lgao@redhat.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/cliver-project/AITrigram/test/utils"
)

var _ = Describe("LLMEngine Inference Tests with HostPath Storage", Ordered, func() {
	// Set test timeouts - inference can take longer
	SetDefaultEventuallyTimeout(40 * time.Minute)
	SetDefaultEventuallyPollingInterval(30 * time.Second)

	Context("Ollama with HostPath Storage - Full Inference Test", func() {
		const modelRepoName = "ollama-hostpath-inference-test"
		const engineName = "ollama-hostpath-inference-test"
		const engineNamespace = "default"

		AfterEach(func() {
			cmd := exec.Command("kubectl", "delete", "llmengine", engineName, "-n", engineNamespace, "--ignore-not-found", "--wait=true", "--timeout=60s")
			output, _ := utils.Run(cmd)
			_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup LLMEngine: %s\n", output)

			cmd = exec.Command("kubectl", "delete", "modelrepository", modelRepoName, "--ignore-not-found", "--wait=true", "--timeout=60s")
			output, _ = utils.Run(cmd)
			_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup ModelRepository: %s\n", output)

			cmd = exec.Command("kubectl", "delete", "pod", "inference-test-client", "-n", engineNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should deploy Ollama model and engine, then handle inference requests", func() {
			By("applying ModelRepository and LLMEngine manifests")
			cmd := exec.Command("kubectl", "apply", "-f", "test/e2e/storage/e2e-06-ollama-hostpath-inference.yaml")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply Ollama inference test manifests")
			_, _ = fmt.Fprintf(GinkgoWriter, "Applied manifests: %s\n", output)

			By("waiting for ModelRepository download to complete")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "modelrepository", modelRepoName,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get ModelRepository status")
				g.Expect(output).To(Equal("Ready"), "ModelRepository should be in Ready phase")
				_, _ = fmt.Fprintf(GinkgoWriter, "ModelRepository phase: %s\n", output)
			}).Should(Succeed())

			By("waiting for LLMEngine deployment to be created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deployment", "-n", engineNamespace, "-l",
					"app="+engineName+",model="+modelRepoName,
					"-o", "name")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get LLMEngine deployment")
				deployments := utils.GetNonEmptyLines(output)
				g.Expect(deployments).ToNot(BeEmpty(), "Expected at least 1 deployment")
				_, _ = fmt.Fprintf(GinkgoWriter, "Found deployment: %v\n", deployments)
			}).Should(Succeed())

			By("waiting for LLMEngine deployment to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deployment", "-n", engineNamespace, "-l",
					"app="+engineName+",model="+modelRepoName,
					"-o", "jsonpath={.items[0].status.readyReplicas}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get deployment ready replicas")
				g.Expect(output).To(Equal("1"), "Deployment should have 1 ready replica")
				_, _ = fmt.Fprintf(GinkgoWriter, "Ready replicas: %s\n", output)
			}).Should(Succeed())

			By("waiting for service to be created")
			var serviceName string
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "service", "-n", engineNamespace, "-l",
					"app="+engineName+",model="+modelRepoName,
					"-o", "jsonpath={.items[0].metadata.name}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get service")
				g.Expect(output).NotTo(BeEmpty(), "Service name should not be empty")
				serviceName = output
				_, _ = fmt.Fprintf(GinkgoWriter, "Found service: %s\n", serviceName)
			}).Should(Succeed())

			By("getting service ClusterIP and port")
			cmd = exec.Command("kubectl", "get", "service", serviceName, "-n", engineNamespace,
				"-o", "jsonpath={.spec.clusterIP}")
			serviceIP, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to get service IP")
			Expect(serviceIP).NotTo(BeEmpty(), "Service IP should not be empty")

			cmd = exec.Command("kubectl", "get", "service", serviceName, "-n", engineNamespace,
				"-o", "jsonpath={.spec.ports[0].port}")
			servicePort, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to get service port")
			Expect(servicePort).NotTo(BeEmpty(), "Service port should not be empty")

			_, _ = fmt.Fprintf(GinkgoWriter, "Service endpoint: %s:%s\n", serviceIP, servicePort)

			By("creating a test pod for making HTTP requests")
			testPodYaml := fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: inference-test-client
  namespace: %s
spec:
  containers:
  - name: curl
    image: curlimages/curl:latest
    command: ["sh", "-c", "sleep 3600"]
  restartPolicy: Never
`, engineNamespace)

			cmd = exec.Command("sh", "-c", fmt.Sprintf("echo '%s' | kubectl apply -f -", testPodYaml))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create test client pod")

			By("waiting for test client pod to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", "inference-test-client", "-n", engineNamespace,
					"-o", "jsonpath={.status.phase}")
				phase, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(phase).To(Equal("Running"), "Test client pod should be running")
			}).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			By("sending inference request to Ollama service")
			// Ollama API format: POST /api/generate
			// The model name is "qwen2.5:0.5b" as downloaded by ollama pull
			curlCommand := fmt.Sprintf(
				`curl -s -m 120 http://%s:%s/api/generate -d '{"model": "qwen2.5:0.5b", "prompt": "Say hello world", "stream": false}'`,
				serviceIP, servicePort)

			var inferenceResponse string
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "exec", "inference-test-client", "-n", engineNamespace, "--",
					"sh", "-c", curlCommand)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to execute inference request")
				inferenceResponse = output
				g.Expect(inferenceResponse).NotTo(BeEmpty(), "Inference response should not be empty")
				_, _ = fmt.Fprintf(GinkgoWriter, "Raw response: %s\n", inferenceResponse)

				// Parse JSON response
				var result map[string]interface{}
				err = json.Unmarshal([]byte(inferenceResponse), &result)
				g.Expect(err).NotTo(HaveOccurred(), "Response should be valid JSON")

				// Check for response field
				response, ok := result["response"]
				g.Expect(ok).To(BeTrue(), "Response should contain 'response' field")
				g.Expect(response).NotTo(BeNil(), "Response field should not be nil")
			}).WithTimeout(10 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())

			By("verifying inference response contains expected text")
			// The response should contain some greeting text
			lowerResponse := strings.ToLower(inferenceResponse)
			Expect(lowerResponse).To(ContainSubstring("hello"), "Response should contain 'hello'")
			_, _ = fmt.Fprintf(GinkgoWriter, "✓ Inference test passed!\n")

		})
	})

	Context("HuggingFace (vLLM) with HostPath Storage - Full Inference Test", func() {
		const modelRepoName = "hf-hostpath-inference-test"
		const engineName = "hf-hostpath-inference-test"
		const engineNamespace = "aitrigram-system"

		AfterEach(func() {
			cmd := exec.Command("kubectl", "delete", "llmengine", engineName, "-n", engineNamespace, "--ignore-not-found", "--wait=true", "--timeout=60s")
			output, _ := utils.Run(cmd)
			_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup LLMEngine: %s\n", output)

			cmd = exec.Command("kubectl", "delete", "modelrepository", modelRepoName, "--ignore-not-found", "--wait=true", "--timeout=60s")
			output, _ = utils.Run(cmd)
			_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup ModelRepository: %s\n", output)
		})

		It("should deploy HuggingFace model and vLLM engine, then handle inference requests", func() {
			By("checking if cluster has GPU support")
			cmd := exec.Command("kubectl", "get", "nodes", "-o", "jsonpath={.items[*].status.capacity.nvidia\\.com/gpu}")
			gpuOutput, _ := utils.Run(cmd)
			if gpuOutput == "" {
				Skip("No GPU detected - skipping HuggingFace/vLLM inference test (vLLM requires GPU or CPU fallback)")
			}

			By("applying ModelRepository and LLMEngine manifests")
			cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/storage/e2e-07-huggingface-hostpath-inference.yaml")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply HuggingFace inference test manifests")
			_, _ = fmt.Fprintf(GinkgoWriter, "Applied manifests: %s\n", output)

			By("waiting for ModelRepository download to complete")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "modelrepository", modelRepoName,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get ModelRepository status")
				g.Expect(output).To(Equal("Ready"), "ModelRepository should be in Ready phase")
				_, _ = fmt.Fprintf(GinkgoWriter, "ModelRepository phase: %s\n", output)
			}).Should(Succeed())

			By("waiting for LLMEngine deployment to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deployment", "-n", engineNamespace, "-l",
					"app="+engineName+",model="+modelRepoName,
					"-o", "jsonpath={.items[0].status.readyReplicas}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get deployment ready replicas")
				g.Expect(output).To(Equal("1"), "Deployment should have 1 ready replica")
			}).Should(Succeed())

			By("getting service endpoint")
			cmd = exec.Command("kubectl", "get", "service", "-n", engineNamespace, "-l",
				"app="+engineName+",model="+modelRepoName,
				"-o", "jsonpath={.items[0].metadata.name}")
			serviceName, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(serviceName).NotTo(BeEmpty())

			cmd = exec.Command("kubectl", "get", "service", serviceName, "-n", engineNamespace,
				"-o", "jsonpath={.spec.clusterIP}:{.spec.ports[0].port}")
			serviceEndpoint, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprintf(GinkgoWriter, "Service endpoint: %s\n", serviceEndpoint)

			By("creating test client pod if not exists")
			cmd = exec.Command("kubectl", "get", "pod", "inference-test-client", "-n", engineNamespace)
			_, err = utils.Run(cmd)
			if err != nil {
				testPodYaml := fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: inference-test-client
  namespace: %s
spec:
  containers:
  - name: curl
    image: curlimages/curl:latest
    command: ["sh", "-c", "sleep 3600"]
  restartPolicy: Never
`, engineNamespace)
				cmd = exec.Command("sh", "-c", fmt.Sprintf("echo '%s' | kubectl apply -f -", testPodYaml))
				_, err = utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func(g Gomega) {
					cmd := exec.Command("kubectl", "get", "pod", "inference-test-client", "-n", engineNamespace,
						"-o", "jsonpath={.status.phase}")
					phase, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(phase).To(Equal("Running"))
				}).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
			}

			By("sending inference request to vLLM service")
			curlCommand := fmt.Sprintf(
				`curl -s -m 120 http://%s/v1/completions -H "Content-Type: application/json" -d '{"model": "hf-internal-testing/tiny-random-gpt2", "prompt": "Hello world", "max_tokens": 20}'`,
				serviceEndpoint)

			var inferenceResponse string
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "exec", "inference-test-client", "-n", engineNamespace, "--",
					"sh", "-c", curlCommand)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to execute inference request")
				inferenceResponse = output
				g.Expect(inferenceResponse).NotTo(BeEmpty(), "Response should not be empty")

				var result map[string]interface{}
				err = json.Unmarshal([]byte(inferenceResponse), &result)
				g.Expect(err).NotTo(HaveOccurred(), "Response should be valid JSON")
			}).WithTimeout(10 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())

			_, _ = fmt.Fprintf(GinkgoWriter, "Inference response: %s\n", inferenceResponse)
			_, _ = fmt.Fprintf(GinkgoWriter, "✓ HuggingFace inference test passed!\n")

		})
	})
})
