/*
Copyright 2025 Lin Gao.

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
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/cliver-project/AITrigram/test/utils"
)

// namespace where the project is deployed in
const namespace = "aitrigram-system"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("undeploying the controller-manager")
		cmd := exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})
	})

	Context("Ollama LLMEngine E2E Test", func() {
		const testNamespace = "ollama-e2e-test"
		var serviceName string
		var serviceIP string
		var servicePort string

		It("should deploy Ollama LLMEngine and handle inference requests", func() {
			By("creating test namespace")
			cmd := exec.Command("kubectl", "create", "ns", testNamespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create test namespace")

			By("deploying the Ollama LLMEngine sample")
			cmd = exec.Command("kubectl", "apply", "-f", "config/samples/e2e-ollama-simple.yaml", "-n", testNamespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to deploy Ollama LLMEngine sample")

			By("waiting for deployment to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "deployment", "-n", testNamespace, "-o", "name")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get deployments")
				deployments := utils.GetNonEmptyLines(output)
				g.Expect(len(deployments)).To(BeNumerically(">", 0), "Expected at least 1 deployment")

				// Check if deployment is ready
				for _, deployment := range deployments {
					deploymentName := strings.TrimPrefix(deployment, "deployment.apps/")
					cmd = exec.Command("kubectl", "get", "deployment", deploymentName, "-n", testNamespace,
						"-o", "jsonpath={.status.readyReplicas}")
					readyReplicas, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(readyReplicas).To(Equal("1"), "Deployment should have 1 ready replica")
				}
			}).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("waiting for service to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "service", "-n", testNamespace,
					"-o", "go-template={{ range .items }}{{ .metadata.name }}{{ \"\\n\" }}{{ end }}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get services")
				services := utils.GetNonEmptyLines(output)
				g.Expect(len(services)).To(BeNumerically(">", 0), "Expected at least 1 service")
				serviceName = services[0]
			}).Should(Succeed())

			By("getting service IP and port")
			cmd = exec.Command("kubectl", "get", "service", serviceName, "-n", testNamespace,
				"-o", "jsonpath={.spec.clusterIP}")
			serviceIP, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to get service IP")
			Expect(serviceIP).NotTo(BeEmpty(), "Service IP should not be empty")

			cmd = exec.Command("kubectl", "get", "service", serviceName, "-n", testNamespace,
				"-o", "jsonpath={.spec.ports[0].port}")
			servicePort, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to get service port")
			Expect(servicePort).NotTo(BeEmpty(), "Service port should not be empty")

			_, _ = fmt.Fprintf(GinkgoWriter, "Service endpoint: %s:%s\n", serviceIP, servicePort)

			By("deploying busybox pod for testing")
			busyboxYaml := fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: busybox-client
  namespace: %s
spec:
  containers:
  - name: busybox
    image: busybox:latest
    command: ["sh", "-c", "sleep 3600"]
`, testNamespace)

			cmd = exec.Command("sh", "-c", fmt.Sprintf("echo '%s' | kubectl apply -f -", busyboxYaml))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to deploy busybox pod")

			By("waiting for busybox pod to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", "busybox-client", "-n", testNamespace,
					"-o", "jsonpath={.status.phase}")
				phase, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(phase).To(Equal("Running"), "Busybox pod should be running")
			}).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			By("performing LLM inference request via busybox")
			curlCommand := fmt.Sprintf(`curl -s http://%s:%s/api/generate -d '{"model": "llama3.2:latest", "prompt": "Hello, Please say Hello to me!", "stream": false}'`,
				serviceIP, servicePort)

			var inferenceResponse string
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "exec", "busybox-client", "-n", testNamespace, "--",
					"sh", "-c", curlCommand)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to execute curl command in busybox")
				inferenceResponse = output
				g.Expect(inferenceResponse).NotTo(BeEmpty(), "Inference response should not be empty")
			}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			_, _ = fmt.Fprintf(GinkgoWriter, "Inference response: %s\n", inferenceResponse)

			By("verifying inference response contains 'Hello'")
			Expect(strings.Contains(strings.ToLower(inferenceResponse), "hello")).To(BeTrue(),
				"Response should contain 'Hello' (case-insensitive)")

			By("cleaning up test resources")
			cmd = exec.Command("kubectl", "delete", "namespace", testNamespace, "--wait=false")
			_, _ = utils.Run(cmd)
		})
	})
})
