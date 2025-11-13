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
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/cliver-project/AITrigram/test/utils"
)

var (
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

// Track test failures
var _ = ReportAfterEach(func(report SpecReport) {
	if report.Failed() {
		suiteHadFailures = true
	}
})

var _ = BeforeSuite(func() {
	// Check if CRDs are already installed
	By("checking if CRDs are already installed")
	cmd := exec.Command("kubectl", "get", "crd", "modelrepositories.aitrigram.cliver-project.github.io")
	_, err := utils.Run(cmd)
	crdsInstalled := (err == nil)
	if crdsInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "CRDs already installed, skipping installation\n")
	} else {
		By("installing CRDs to the cluster")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to install CRDs")
	}

	// Check if controller is already deployed
	By("checking if controller is already deployed")
	cmd = exec.Command("kubectl", "get", "deployment", "aitrigram-controller-manager", "-n", "aitrigram-system")
	_, err = utils.Run(cmd)
	controllerDeployed := (err == nil)
	if controllerDeployed {
		_, _ = fmt.Fprintf(GinkgoWriter, "Controller already deployed, skipping deployment\n")
	} else {
		By("building the controller binary")
		cmd = exec.Command("make", "build")
		_, err = utils.Run(cmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the controller binary")

		By("building the controller docker image")
		cmd = exec.Command("make", "docker-build", "IMG=ghcr.io/cliver-project/aitrigram-controller:latest")
		_, err = utils.Run(cmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the controller docker image")

		By("loading the controller image into minikube")
		cmd = exec.Command("minikube", "image", "load", "ghcr.io/cliver-project/aitrigram-controller:latest")
		_, err = utils.Run(cmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load image into minikube")

		By("deploying the controller to the cluster")
		cmd = exec.Command("make", "deploy", "IMG=ghcr.io/cliver-project/aitrigram-controller:latest")
		_, err = utils.Run(cmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to deploy the controller")
	}

	// Wait for controller to be ready if it was just deployed or already exists
	By("waiting for controller to be ready")
	Eventually(func(g Gomega) {
		cmd := exec.Command("kubectl", "get", "deployment", "aitrigram-controller-manager", "-n", "aitrigram-system",
			"-o", "jsonpath={.status.readyReplicas}")
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to get controller deployment status")
		g.Expect(output).To(Equal("1"), "Controller should have 1 ready replica")
		_, _ = fmt.Fprintf(GinkgoWriter, "Controller ready replicas: %s\n", output)
	}).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
})

var _ = AfterSuite(func() {
	// Dump diagnostic information if any tests failed or if E2E_DUMP_LOGS is set
	dumpLogs := suiteHadFailures || os.Getenv("E2E_DUMP_LOGS") == "true"

	if dumpLogs {
		By("=== DUMPING DIAGNOSTIC INFORMATION FOR DEBUGGING ===")

		// Dump controller logs
		By("Fetching controller-manager logs")
		cmd := exec.Command("kubectl", "logs", "deployment/aitrigram-controller-manager", "-n", "aitrigram-system", "--tail=200")
		if output, err := utils.Run(cmd); err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "\n=== CONTROLLER LOGS (last 200 lines) ===\n%s\n", output)
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get controller logs: %v\n", err)
		}

		// Dump all pods in aitrigram-system
		By("Fetching all pods in aitrigram-system namespace")
		cmd = exec.Command("kubectl", "get", "pods", "-n", "aitrigram-system", "-o", "wide")
		if output, err := utils.Run(cmd); err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "\n=== ALL PODS IN aitrigram-system ===\n%s\n", output)
		}

		// Dump all jobs in aitrigram-system
		By("Fetching all jobs in aitrigram-system namespace")
		cmd = exec.Command("kubectl", "get", "jobs", "-n", "aitrigram-system", "-o", "wide")
		if output, err := utils.Run(cmd); err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "\n=== ALL JOBS IN aitrigram-system ===\n%s\n", output)
		}

		// Dump failed job logs
		By("Fetching logs from failed jobs")
		cmd = exec.Command("kubectl", "get", "jobs", "-n", "aitrigram-system", "-o", "jsonpath={.items[?(@.status.failed>=1)].metadata.name}")
		if failedJobs, err := utils.Run(cmd); err == nil && failedJobs != "" {
			jobs := utils.GetNonEmptyLines(failedJobs)
			for _, jobName := range jobs {
				cmd = exec.Command("kubectl", "logs", "job/"+jobName, "-n", "aitrigram-system", "--tail=100")
				if output, err := utils.Run(cmd); err == nil {
					_, _ = fmt.Fprintf(GinkgoWriter, "\n=== FAILED JOB LOGS: %s ===\n%s\n", jobName, output)
				}
			}
		}

		// Dump all ModelRepositories
		By("Fetching all ModelRepositories")
		cmd = exec.Command("kubectl", "get", "modelrepositories", "-o", "yaml")
		if output, err := utils.Run(cmd); err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "\n=== ALL MODELREPOSITORIES ===\n%s\n", output)
		}

		// Dump all PVCs in aitrigram-system
		By("Fetching all PVCs in aitrigram-system namespace")
		cmd = exec.Command("kubectl", "get", "pvc", "-n", "aitrigram-system", "-o", "wide")
		if output, err := utils.Run(cmd); err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "\n=== ALL PVCs IN aitrigram-system ===\n%s\n", output)
		}

		// Dump events in aitrigram-system (last 50)
		By("Fetching recent events in aitrigram-system namespace")
		cmd = exec.Command("kubectl", "get", "events", "-n", "aitrigram-system", "--sort-by=.lastTimestamp")
		if output, err := utils.Run(cmd); err == nil {
			lines := utils.GetNonEmptyLines(output)
			// Get last 50 events
			startIdx := 0
			if len(lines) > 50 {
				startIdx = len(lines) - 50
			}
			_, _ = fmt.Fprintf(GinkgoWriter, "\n=== RECENT EVENTS IN aitrigram-system (last 50) ===\n")
			for i := startIdx; i < len(lines); i++ {
				_, _ = fmt.Fprintf(GinkgoWriter, "%s\n", lines[i])
			}
		}

		// Dump storage class information
		By("Fetching all storage classes")
		cmd = exec.Command("kubectl", "get", "storageclass")
		if output, err := utils.Run(cmd); err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "\n=== STORAGE CLASSES ===\n%s\n", output)
		}

		// Dump NFS server status if it exists
		By("Fetching NFS server status")
		cmd = exec.Command("kubectl", "get", "deployment", "nfs-server", "-n", "aitrigram-system", "-o", "yaml")
		if output, err := utils.Run(cmd); err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "\n=== NFS SERVER DEPLOYMENT ===\n%s\n", output)
		}

		// Dump NFS provisioner status if it exists
		By("Fetching NFS provisioner status")
		cmd = exec.Command("kubectl", "get", "deployment", "nfs-client-provisioner", "-n", "aitrigram-system", "-o", "yaml")
		if output, err := utils.Run(cmd); err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "\n=== NFS PROVISIONER DEPLOYMENT ===\n%s\n", output)
		}

		By("=== END OF DIAGNOSTIC INFORMATION ===")

	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "\nAll tests passed. Set E2E_DUMP_LOGS=true to always dump diagnostic logs.\n")
	}

	// Note: We intentionally do NOT cleanup controller/CRDs in AfterSuite
	// This allows tests to run multiple times against the same cluster
	// and supports production-grade clusters where the controller is managed separately
	// Users can manually run 'make undeploy' and 'make uninstall' if needed
	_, _ = fmt.Fprintf(GinkgoWriter, "\nTest suite completed. Controller and CRDs remain deployed for reuse.\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "To cleanup, run: make undeploy && make uninstall\n")
})
