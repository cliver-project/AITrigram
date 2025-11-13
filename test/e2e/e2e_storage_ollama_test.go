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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/cliver-project/AITrigram/test/utils"
)

var _ = Describe("ModelRepository Storage Tests for Ollama", Ordered, func() {
	// Use the same namespace as the controller (aitrigram-system)
	// Jobs and PVCs are created in the operator namespace
	const testNamespace = namespace // Reuse aitrigram-system from e2e_test.go

	// Setup NFS server and provisioner for testing
	BeforeAll(func() {
		By("deploying NFS server ServiceAccount (for OpenShift SCC)")
		cmd := exec.Command("kubectl", "apply", "-f", "test/e2e/storage/nfs-server-serviceaccount.yaml")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy NFS server ServiceAccount")

		By("deploying NFS server for testing")
		cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/storage/nfs-server-deployment.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy NFS server")

		By("waiting for NFS server to be ready")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "deployment", "nfs-server", "-n", testNamespace,
				"-o", "jsonpath={.status.readyReplicas}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to get NFS server deployment status")
			g.Expect(output).To(Equal("1"), "NFS server should have 1 ready replica")
			_, _ = fmt.Fprintf(GinkgoWriter, "NFS server ready replicas: %s\n", output)
		}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("waiting for NFS server pod to be running")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "pods", "-n", testNamespace, "-l", "app=nfs-server",
				"-o", "jsonpath={.items[0].status.phase}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to get NFS server pod status")
			g.Expect(output).To(Equal("Running"), "NFS server pod should be running")
			_, _ = fmt.Fprintf(GinkgoWriter, "NFS server pod status: %s\n", output)
		}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("waiting additional time for NFS server to be fully ready to accept connections")
		time.Sleep(10 * time.Second)

		// Check if nfs-client storage class already exists
		By("checking if nfs-client storage class already exists")
		cmd = exec.Command("kubectl", "get", "storageclass", "nfs-client")
		_, err = utils.Run(cmd)
		nfsClientExists := (err == nil)
		if nfsClientExists {
			_, _ = fmt.Fprintf(GinkgoWriter, "nfs-client storage class already exists, skipping NFS provisioner installation\n")
		} else {
			By("deploying NFS provisioner for RWX PVC tests")
			cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/storage/nfs-provisioner.yaml")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to deploy NFS provisioner")

			By("waiting for NFS provisioner to be ready (may skip if NFS client support unavailable)")
			// In CI environments like GitHub Actions, Minikube may not have NFS client support
			// If the provisioner fails to start, log the issue but don't fail the entire test suite
			provisionerStarted := false
			for i := 0; i < 36; i++ { // 3 minutes with 5 second intervals
				cmd = exec.Command("kubectl", "get", "deployment", "nfs-client-provisioner", "-n", testNamespace,
					"-o", "jsonpath={.status.readyReplicas}")
				output, err := utils.Run(cmd)
				if err == nil && output == "1" {
					_, _ = fmt.Fprintf(GinkgoWriter, "NFS provisioner ready replicas: %s\n", output)
					provisionerStarted = true
					break
				}

				// Check if pod is in error state
				cmd = exec.Command("kubectl", "get", "pods", "-n", testNamespace,
					"-l", "app=nfs-client-provisioner",
					"-o", "jsonpath={.items[0].status.containerStatuses[0].state.waiting.message}")
				waitingMsg, _ := utils.Run(cmd)
				if waitingMsg != "" && (i%6 == 0) { // Log every 30 seconds
					_, _ = fmt.Fprintf(GinkgoWriter, "NFS provisioner pod waiting: %s\n", waitingMsg)
				}

				time.Sleep(5 * time.Second)
			}

			if !provisionerStarted {
				_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: NFS provisioner failed to start after 3 minutes.\n")
				_, _ = fmt.Fprintf(GinkgoWriter, "This is expected in environments without NFS client support (e.g., GitHub Actions).\n")
				_, _ = fmt.Fprintf(GinkgoWriter, "RWX PVC tests will be skipped automatically.\n")

				// Get pod description for debugging
				cmd = exec.Command("kubectl", "describe", "pod", "-n", testNamespace,
					"-l", "app=nfs-client-provisioner")
				podDesc, _ := utils.Run(cmd)
				_, _ = fmt.Fprintf(GinkgoWriter, "Pod description:\n%s\n", podDesc)
			}
		}
	})

	// Cleanup NFS server and provisioner
	AfterAll(func() {
		By("cleaning up NFS provisioner")
		cmd := exec.Command("kubectl", "delete", "-f", "test/e2e/storage/nfs-provisioner.yaml", "--wait=false")
		_, _ = utils.Run(cmd)

		By("cleaning up NFS server deployment")
		cmd = exec.Command("kubectl", "delete", "-f", "test/e2e/storage/nfs-server-deployment.yaml", "--wait=false")
		_, _ = utils.Run(cmd)

		By("cleaning up NFS server ServiceAccount")
		cmd = exec.Command("kubectl", "delete", "-f", "test/e2e/storage/nfs-server-serviceaccount.yaml", "--wait=false")
		_, _ = utils.Run(cmd)
	})

	// Set test timeouts
	SetDefaultEventuallyTimeout(30 * time.Minute)
	SetDefaultEventuallyPollingInterval(30 * time.Second)

	Context("HostPath Storage", func() {
		const modelRepoName = "ollama-hostpath-test"

		It("should download Ollama model successfully with HostPath storage", func() {
			By("applying ModelRepository with HostPath storage")
			cmd := exec.Command("kubectl", "apply", "-f", "test/e2e/storage/e2e-02-ollama-hostpath.yaml")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply ModelRepository with HostPath storage")
			_, _ = fmt.Fprintf(GinkgoWriter, "Applied ModelRepository: %s\n", output)

			By("waiting for download job to be created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", "-n", testNamespace, "-l",
					"modelrepository.aitrigram.cliver-project.github.io/name="+modelRepoName,
					"-o", "name")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get download job")
				jobs := utils.GetNonEmptyLines(output)
				g.Expect(len(jobs)).To(BeNumerically(">", 0), "Expected at least 1 download job")
				_, _ = fmt.Fprintf(GinkgoWriter, "Found download job: %v\n", jobs)
			}).Should(Succeed())

			By("waiting for download job to complete successfully")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", "-n", testNamespace, "-l",
					"modelrepository.aitrigram.cliver-project.github.io/name="+modelRepoName,
					"-o", "jsonpath={.items[0].status.succeeded}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get job status")
				g.Expect(output).To(Equal("1"), "Download job should succeed")
				_, _ = fmt.Fprintf(GinkgoWriter, "Job succeeded count: %s\n", output)
			}).Should(Succeed())

			By("verifying ModelRepository status is 'downloaded'")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "modelrepository", modelRepoName,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get ModelRepository status")
				g.Expect(strings.ToLower(output)).To(Equal("downloaded"), "ModelRepository should be in downloaded phase")
				_, _ = fmt.Fprintf(GinkgoWriter, "ModelRepository phase: %s\n", output)
			}).Should(Succeed())

			By("verifying BoundNodeName is set in status")
			cmd = exec.Command("kubectl", "get", "modelrepository", modelRepoName,
				"-o", "jsonpath={.status.boundNodeName}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to get boundNodeName")
			Expect(output).NotTo(BeEmpty(), "BoundNodeName should be set for HostPath storage")
			_, _ = fmt.Fprintf(GinkgoWriter, "Model bound to node: %s\n", output)

			By("cleaning up ModelRepository")
			cmd = exec.Command("kubectl", "delete", "modelrepository", modelRepoName, "--wait=true")
			output, _ = utils.Run(cmd)
			_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup result: %s\n", output)
		})
	})

	Context("PVC ReadWriteMany (RWX) Storage", func() {
		const modelRepoName = "ollama-pvc-rwx-test"
		const pvcName = "model-storage-rwx-test"

		It("should download Ollama model successfully with RWX PVC storage", func() {
			By("checking if a ReadWriteMany storage class is available")
			cmd := exec.Command("kubectl", "get", "storageclass",
				"-o", "jsonpath={.items[?(@.metadata.annotations.storageclass\\.kubernetes\\.io/is-default-class=='true')].metadata.name}")
			defaultSC, err := utils.Run(cmd)
			if err != nil || defaultSC == "" {
				Skip("No default storage class found - skipping RWX PVC test")
			}
			_, _ = fmt.Fprintf(GinkgoWriter, "Using default storage class: %s\n", defaultSC)

			By("applying ModelRepository with RWX PVC storage")
			cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/storage/e2e-03-ollama-pvc-rwx.yaml")
			output, err := utils.Run(cmd)
			if err != nil {
				// If RWX is not supported, skip gracefully
				if strings.Contains(err.Error(), "ReadWriteMany") || strings.Contains(err.Error(), "access mode") {
					Skip("ReadWriteMany not supported by storage class - skipping RWX test")
				}
				Expect(err).NotTo(HaveOccurred(), "Failed to apply ModelRepository with RWX PVC storage")
			}
			_, _ = fmt.Fprintf(GinkgoWriter, "Applied ModelRepository: %s\n", output)

			By("waiting for PVC to be auto-created by controller")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pvc", "-n", testNamespace, "-l",
					"aitrigram.io/model-repo="+modelRepoName,
					"-o", "jsonpath={.items[0].status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get PVC")
				g.Expect(output).To(Or(Equal("Bound"), Equal("Pending")), "PVC should be created by controller")
				_, _ = fmt.Fprintf(GinkgoWriter, "Auto-created PVC status: %s\n", output)
			}).Should(Succeed())

			By("waiting for download job to complete successfully")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", "-n", testNamespace, "-l",
					"modelrepository.aitrigram.cliver-project.github.io/name="+modelRepoName,
					"-o", "jsonpath={.items[0].status.succeeded}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get job status")
				g.Expect(output).To(Equal("1"), "Download job should succeed")
				_, _ = fmt.Fprintf(GinkgoWriter, "Job succeeded count: %s\n", output)
			}).Should(Succeed())

			By("verifying ModelRepository status is 'downloaded'")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "modelrepository", modelRepoName,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get ModelRepository status")
				g.Expect(strings.ToLower(output)).To(Equal("downloaded"), "ModelRepository should be in downloaded phase")
				_, _ = fmt.Fprintf(GinkgoWriter, "ModelRepository phase: %s\n", output)
			}).Should(Succeed())

			By("cleaning up ModelRepository (controller will auto-cleanup PVC)")
			cmd = exec.Command("kubectl", "delete", "modelrepository", modelRepoName, "--wait=true")
			cleanupOutput, _ := utils.Run(cmd)
			_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup ModelRepository result: %s\n", cleanupOutput)
		})
	})

	Context("PVC ReadWriteOnce (RWO) Storage", func() {
		const modelRepoName = "ollama-pvc-rwo-test"
		const pvcName = "model-storage-rwo-test"

		It("should download Ollama model successfully with RWO PVC storage", func() {
			By("checking if a storage class is available")
			cmd := exec.Command("kubectl", "get", "storageclass")
			output, err := utils.Run(cmd)
			if err != nil {
				Skip("No storage class available - skipping RWO PVC test")
			}
			_, _ = fmt.Fprintf(GinkgoWriter, "Storage classes available\n")

			By("applying ModelRepository with RWO PVC storage")
			cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/storage/e2e-04-ollama-pvc-rwo.yaml")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply ModelRepository with RWO PVC storage")
			_, _ = fmt.Fprintf(GinkgoWriter, "Applied ModelRepository: %s\n", output)

			By("waiting for PVC to be auto-created by controller")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pvc", "-n", testNamespace, "-l",
					"aitrigram.io/model-repo="+modelRepoName,
					"-o", "jsonpath={.items[0].status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get PVC")
				g.Expect(output).To(Or(Equal("Bound"), Equal("Pending")), "PVC should be created by controller")
				_, _ = fmt.Fprintf(GinkgoWriter, "Auto-created PVC status: %s\n", output)
			}).Should(Succeed())

			By("waiting for download job to complete successfully")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", "-n", testNamespace, "-l",
					"modelrepository.aitrigram.cliver-project.github.io/name="+modelRepoName,
					"-o", "jsonpath={.items[0].status.succeeded}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get job status")
				g.Expect(output).To(Equal("1"), "Download job should succeed")
				_, _ = fmt.Fprintf(GinkgoWriter, "Job succeeded count: %s\n", output)
			}).Should(Succeed())

			By("verifying ModelRepository status is 'downloaded'")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "modelrepository", modelRepoName,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get ModelRepository status")
				g.Expect(strings.ToLower(output)).To(Equal("downloaded"), "ModelRepository should be in downloaded phase")
				_, _ = fmt.Fprintf(GinkgoWriter, "ModelRepository phase: %s\n", output)
			}).Should(Succeed())

			By("verifying BoundNodeName is set in status for RWO storage")
			cmd = exec.Command("kubectl", "get", "modelrepository", modelRepoName,
				"-o", "jsonpath={.status.boundNodeName}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to get boundNodeName")
			Expect(output).NotTo(BeEmpty(), "BoundNodeName should be set for RWO storage")
			_, _ = fmt.Fprintf(GinkgoWriter, "Model bound to node: %s\n", output)

			By("cleaning up ModelRepository (controller will auto-cleanup PVC)")
			cmd = exec.Command("kubectl", "delete", "modelrepository", modelRepoName, "--wait=true")
			cleanupOutput, _ := utils.Run(cmd)
			_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup ModelRepository result: %s\n", cleanupOutput)
		})
	})

	Context("NFS Storage", func() {
		const modelRepoName = "ollama-nfs-test"

		It("should download Ollama model successfully with NFS storage", func() {
			By("applying ModelRepository with NFS storage")
			cmd := exec.Command("kubectl", "apply", "-f", "test/e2e/storage/e2e-01-ollama-nfs.yaml")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply ModelRepository with NFS storage")
			_, _ = fmt.Fprintf(GinkgoWriter, "Applied ModelRepository: %s\n", output)

			By("waiting for download job to complete successfully")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", "-n", testNamespace, "-l",
					"modelrepository.aitrigram.cliver-project.github.io/name="+modelRepoName,
					"-o", "jsonpath={.items[0].status.succeeded}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get job status")
				g.Expect(output).To(Equal("1"), "Download job should succeed")
				_, _ = fmt.Fprintf(GinkgoWriter, "Job succeeded count: %s\n", output)
			}).Should(Succeed())

			By("verifying ModelRepository status is 'downloaded'")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "modelrepository", modelRepoName,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get ModelRepository status")
				g.Expect(strings.ToLower(output)).To(Equal("downloaded"), "ModelRepository should be in downloaded phase")
				_, _ = fmt.Fprintf(GinkgoWriter, "ModelRepository phase: %s\n", output)
			}).Should(Succeed())

			By("cleaning up ModelRepository")
			cmd = exec.Command("kubectl", "delete", "modelrepository", modelRepoName, "--wait=true")
			output, _ = utils.Run(cmd)
			_, _ = fmt.Fprintf(GinkgoWriter, "Cleanup result: %s\n", output)
		})
	})
})
