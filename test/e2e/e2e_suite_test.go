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
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/cliver-project/AITrigram/test/utils"
)

const defaultControllerIMG = "ghcr.io/cliver-project/aitrigram-controller:latest"

var (
	// controllerIMG is the image used for the controller deployment.
	// Override via E2E_CONTROLLER_IMG env var.
	controllerIMG string

	// Track if any test failed during the suite
	suiteHadFailures = false
)

// TestE2E runs the end-to-end (e2e) test suite for the project. These tests execute in an isolated,
// temporary environment to validate project changes with the purpose to be used in CI jobs.
// The setup requires Minikube and:
// 1. Builds the controller binary
// 2. Builds the Docker image locally
// 3. Loads the image into minikube (avoiding remote registry pulls)
// 4. Installs CRDs
// 5. Deploys the controller using the locally built image
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting aitrigram integration test suite\n")
	RunSpecs(t, "e2e suite")
}

// Dump cluster state immediately after a test fails, before AfterEach cleanup
// runs. JustAfterEach executes after It but before AfterEach, so all
// resources are still present for inspection.
var _ = JustAfterEach(func() {
	report := CurrentSpecReport()
	if report.Failed() {
		suiteHadFailures = true
		dumpClusterState(report)
	}
})

// dumpClusterState collects a comprehensive snapshot of all CRs, workloads, and
// pod logs when a test fails. This runs before AfterEach cleanup, so resources
// are still present.
func dumpClusterState(report SpecReport) {
	namespacesToDump := []string{"aitrigram-system", "default"}

	_, _ = fmt.Fprintf(GinkgoWriter, "\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "============================================================\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "  CLUSTER STATE DUMP — Test Failed: %s\n", report.FullText())
	_, _ = fmt.Fprintf(GinkgoWriter, "============================================================\n\n")

	// --- Custom Resources ---
	runDumpCmd("ModelRepositories", "kubectl", "get", "modelrepositories", "-o", "yaml")
	runDumpCmd("LLMEngines (all namespaces)", "kubectl", "get", "llmengines", "--all-namespaces", "-o", "yaml")

	for _, ns := range namespacesToDump {
		prefix := fmt.Sprintf("[ns=%s]", ns)

		// --- Workloads ---
		runDumpCmd(prefix+" Deployments", "kubectl", "get", "deployments", "-n", ns, "-o", "wide")
		runDumpCmd(prefix+" LLMEngine Deployments (yaml)", "kubectl", "get", "deployments", "-n", ns,
			"-l", "engine-type", "-o", "yaml")
		runDumpCmd(prefix+" Pods", "kubectl", "get", "pods", "-n", ns, "-o", "wide")
		runDumpCmd(prefix+" Jobs", "kubectl", "get", "jobs", "-n", ns, "-o", "wide")
		runDumpCmd(prefix+" Services", "kubectl", "get", "services", "-n", ns, "-o", "wide")
		runDumpCmd(prefix+" PVCs", "kubectl", "get", "pvc", "-n", ns, "-o", "wide")

		// --- Pod logs (last 100 lines per container) ---
		dumpPodLogs(ns)

		// --- Describe non-Running pods ---
		dumpNonRunningPodDescriptions(ns)

		// --- Events (last 50) ---
		runDumpCmd(prefix+" Events (recent)", "kubectl", "get", "events", "-n", ns,
			"--sort-by=.lastTimestamp", "--field-selector=type!=Normal")
	}

	// --- Controller deployment and logs (always useful) ---
	runDumpCmd("Controller Manager Deployment",
		"kubectl", "get", "deployment", "aitrigram-controller-manager",
		"-n", "aitrigram-system", "-o", "yaml")
	runDumpCmd("Controller Manager Logs (last 200)",
		"kubectl", "logs", "deployment/aitrigram-controller-manager",
		"-n", "aitrigram-system", "--tail=200")

	_, _ = fmt.Fprintf(GinkgoWriter, "\n============================================================\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "  END CLUSTER STATE DUMP\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "============================================================\n\n")
}

// runDumpCmd runs a kubectl command and prints the output under a labeled header.
func runDumpCmd(label string, args ...string) {
	cmd := exec.Command(args[0], args[1:]...)
	output, err := utils.Run(cmd)
	_, _ = fmt.Fprintf(GinkgoWriter, "\n--- %s ---\n", label)
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "(error: %v)\n", err)
	} else if strings.TrimSpace(output) == "" {
		_, _ = fmt.Fprintf(GinkgoWriter, "(empty)\n")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "%s\n", output)
	}
}

// dumpPodLogs prints the last 80 lines of logs for every pod in the namespace.
func dumpPodLogs(ns string) {
	cmd := exec.Command("kubectl", "get", "pods", "-n", ns,
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
	output, err := utils.Run(cmd)
	if err != nil {
		return
	}
	for _, podName := range utils.GetNonEmptyLines(output) {
		runDumpCmd(fmt.Sprintf("[ns=%s] Logs: %s", ns, podName),
			"kubectl", "logs", podName, "-n", ns, "--all-containers", "--tail=80")
	}
}

// dumpNonRunningPodDescriptions describes pods that are not in Running phase.
func dumpNonRunningPodDescriptions(ns string) {
	cmd := exec.Command("kubectl", "get", "pods", "-n", ns,
		"--field-selector=status.phase!=Running,status.phase!=Succeeded",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
	output, err := utils.Run(cmd)
	if err != nil {
		return
	}
	for _, podName := range utils.GetNonEmptyLines(output) {
		runDumpCmd(fmt.Sprintf("[ns=%s] Describe non-running pod: %s", ns, podName),
			"kubectl", "describe", "pod", podName, "-n", ns)
	}
}

var _ = BeforeSuite(func() {
	// Resolve controller image: env var override or default
	controllerIMG = os.Getenv("E2E_CONTROLLER_IMG")
	if controllerIMG == "" {
		controllerIMG = defaultControllerIMG
	}
	_, _ = fmt.Fprintf(GinkgoWriter, "Using controller image: %s\n", controllerIMG)

	// Always install CRDs to ensure they match the current code.
	// Skipping on "already exists" risks running tests against stale schemas.
	By("installing CRDs to the cluster")
	cmd := exec.Command("make", "install")
	_, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to install CRDs")

	// Always build and deploy the controller to ensure the binary matches
	// the current code. A stale controller causes unmarshal errors when
	// Go types and CRD schemas diverge.
	By("building the controller binary and docker image")
	cmd = exec.Command("make", "build", "docker-build", "IMG="+controllerIMG)
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the controller")

	By("loading the controller image into minikube")
	cmd = exec.Command("sh", "-c",
		"$(command -v docker || command -v podman) save "+controllerIMG+" | minikube image load --overwrite=true -")
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load image into minikube")

	// Delete the existing controller deployment so the new image is picked up.
	// We avoid make undeploy because it deletes the namespace, which blocks
	// the subsequent make deploy until the namespace finishes terminating.
	By("removing existing controller deployment")
	cmd = exec.Command("kubectl", "delete", "deployment", "aitrigram-controller-manager",
		"-n", "aitrigram-system", "--ignore-not-found")
	output, _ := utils.Run(cmd)
	_, _ = fmt.Fprintf(GinkgoWriter, "Deployment cleanup: %s\n", output)

	By("deploying the controller to the cluster")
	cmd = exec.Command("make", "deploy", "IMG="+controllerIMG)
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to deploy the controller")

	// Configure namespace PodSecurity settings for E2E tests
	// This allows privileged pods like NFS server to run in the aitrigram-system namespace
	By("configuring namespace PodSecurity settings for E2E tests")
	cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/storage/namespace-config.yaml")
	_, err = utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to configure namespace PodSecurity settings")

	// Wait for controller to be ready
	By("waiting for controller to be ready")
	Eventually(func(g Gomega) {
		cmd := exec.Command("kubectl", "get", "deployment", "aitrigram-controller-manager", "-n", "aitrigram-system",
			"-o", "jsonpath={.status.readyReplicas}")
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to get controller deployment status")
		g.Expect(output).To(Equal("1"), "Controller should have 1 ready replica")
		_, _ = fmt.Fprintf(GinkgoWriter, "Controller ready replicas: %s\n", output)
	}).WithTimeout(5*time.Minute).WithPolling(5*time.Second).Should(Succeed(), func() string {
		// Dump diagnostics on timeout so we can see why the controller isn't ready
		diag := "\n=== Controller startup diagnostics ===\n"
		cmd := exec.Command("kubectl", "get", "deployment", "aitrigram-controller-manager",
			"-n", "aitrigram-system", "-o", "yaml")
		if out, err := utils.Run(cmd); err == nil {
			diag += "--- Deployment YAML ---\n" + out + "\n"
		}
		cmd = exec.Command("kubectl", "get", "pods", "-n", "aitrigram-system", "-o", "wide")
		if out, err := utils.Run(cmd); err == nil {
			diag += "--- Pods ---\n" + out + "\n"
		}
		cmd = exec.Command("kubectl", "describe", "pods", "-n", "aitrigram-system",
			"-l", "control-plane=controller-manager")
		if out, err := utils.Run(cmd); err == nil {
			diag += "--- Pod Describe ---\n" + out + "\n"
		}
		cmd = exec.Command("kubectl", "logs", "-n", "aitrigram-system",
			"-l", "control-plane=controller-manager", "--tail=50")
		if out, err := utils.Run(cmd); err == nil {
			diag += "--- Pod Logs ---\n" + out + "\n"
		}
		cmd = exec.Command("kubectl", "get", "events", "-n", "aitrigram-system",
			"--sort-by=.lastTimestamp")
		if out, err := utils.Run(cmd); err == nil {
			diag += "--- Events ---\n" + out + "\n"
		}
		return diag
	})
})

var _ = AfterSuite(func() {
	if suiteHadFailures {
		_, _ = fmt.Fprintf(GinkgoWriter, "\nSome tests failed. Per-test cluster state dumps were printed above.\n")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "\nAll tests passed.\n")
	}

	// Undeploy the controller first so its container releases the image,
	// then remove the image from minikube to prevent stale cache hits.
	By("undeploying controller")
	cmd := exec.Command("make", "undeploy")
	output, _ := utils.Run(cmd)
	_, _ = fmt.Fprintf(GinkgoWriter, "Undeploy: %s\n", output)

	By("removing controller image from minikube cache")
	cmd = exec.Command("minikube", "image", "rm", controllerIMG)
	output, _ = utils.Run(cmd)
	_, _ = fmt.Fprintf(GinkgoWriter, "Image cleanup: %s\n", output)

	_, _ = fmt.Fprintf(GinkgoWriter, "Test suite completed.\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "To reinstall, run: make deploy\n")
})
