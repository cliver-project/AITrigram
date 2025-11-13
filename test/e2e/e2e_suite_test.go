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
	"os/exec"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/cliver-project/AITrigram/test/utils"
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
	cmd = exec.Command("kubectl", "get", "deployment", "controller-manager", "-n", "aitrigram-system")
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
		cmd := exec.Command("kubectl", "get", "deployment", "controller-manager", "-n", "aitrigram-system",
			"-o", "jsonpath={.status.readyReplicas}")
		output, err := utils.Run(cmd)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to get controller deployment status")
		g.Expect(output).To(Equal("1"), "Controller should have 1 ready replica")
		_, _ = fmt.Fprintf(GinkgoWriter, "Controller ready replicas: %s\n", output)
	}).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
})

var _ = AfterSuite(func() {
	// Note: We intentionally do NOT cleanup controller/CRDs in AfterSuite
	// This allows tests to run multiple times against the same cluster
	// and supports production-grade clusters where the controller is managed separately
	// Users can manually run 'make undeploy' and 'make uninstall' if needed
	_, _ = fmt.Fprintf(GinkgoWriter, "Test suite completed. Controller and CRDs remain deployed for reuse.\n")
	_, _ = fmt.Fprintf(GinkgoWriter, "To cleanup, run: make undeploy && make uninstall\n")
})
