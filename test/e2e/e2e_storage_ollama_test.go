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
	const testNamespace = "mr-storage-test"

	// Setup test namespace and NFS server
	BeforeAll(func() {
		By("creating test namespace for ModelRepository storage tests")
		cmd := exec.Command("kubectl", "create", "ns", testNamespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create test namespace")

		By("deploying NFS server for testing")
		cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/storage/nfs-server-deployment.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy NFS server")

		By("waiting for NFS server to be ready")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "deployment", "nfs-server", "-n", "default",
				"-o", "jsonpath={.status.readyReplicas}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to get NFS server deployment status")
			g.Expect(output).To(Equal("1"), "NFS server should have 1 ready replica")
		}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("waiting for NFS server pod to be running")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "pods", "-n", "default", "-l", "app=nfs-server",
				"-o", "jsonpath={.items[0].status.phase}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to get NFS server pod status")
			g.Expect(output).To(Equal("Running"), "NFS server pod should be running")
		}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
	})

	// Cleanup test namespace and NFS server
	AfterAll(func() {
		By("cleaning up NFS server deployment")
		cmd := exec.Command("kubectl", "delete", "-f", "test/e2e/storage/nfs-server-deployment.yaml", "--wait=false")
		_, _ = utils.Run(cmd)

		By("cleaning up test namespace")
		cmd = exec.Command("kubectl", "delete", "ns", testNamespace, "--wait=false")
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
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply ModelRepository with HostPath storage")

			By("waiting for download job to be created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", "-l",
					"app.kubernetes.io/managed-by=modelrepository-controller,modelrepository.aitrigram.cliver-project.github.io/name="+modelRepoName,
					"-o", "name")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get download job")
				jobs := utils.GetNonEmptyLines(output)
				g.Expect(len(jobs)).To(BeNumerically(">", 0), "Expected at least 1 download job")
			}).Should(Succeed())

			By("waiting for download job to complete successfully")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", "-l",
					"app.kubernetes.io/managed-by=modelrepository-controller,modelrepository.aitrigram.cliver-project.github.io/name="+modelRepoName,
					"-o", "jsonpath={.items[0].status.succeeded}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get job status")
				g.Expect(output).To(Equal("1"), "Download job should succeed")
			}).Should(Succeed())

			By("verifying ModelRepository status is 'downloaded'")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "modelrepository", modelRepoName,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get ModelRepository status")
				g.Expect(strings.ToLower(output)).To(Equal("downloaded"), "ModelRepository should be in downloaded phase")
			}).Should(Succeed())

			By("verifying BoundNodeName is set in status")
			cmd = exec.Command("kubectl", "get", "modelrepository", modelRepoName,
				"-o", "jsonpath={.status.boundNodeName}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to get boundNodeName")
			Expect(output).NotTo(BeEmpty(), "BoundNodeName should be set for HostPath storage")
			_, _ = fmt.Fprintf(GinkgoWriter, "Model bound to node: %s\n", output)

			By("cleaning up ModelRepository")
			cmd = exec.Command("kubectl", "delete", "modelrepository", modelRepoName, "--wait=true")
			_, _ = utils.Run(cmd)
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

			By("applying PVC and ModelRepository with RWX PVC storage")
			cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/storage/e2e-03-ollama-pvc-rwx.yaml", "-n", testNamespace)
			_, err = utils.Run(cmd)
			if err != nil {
				// If RWX is not supported, skip gracefully
				if strings.Contains(err.Error(), "ReadWriteMany") || strings.Contains(err.Error(), "access mode") {
					Skip("ReadWriteMany not supported by storage class - skipping RWX test")
				}
				Expect(err).NotTo(HaveOccurred(), "Failed to apply ModelRepository with RWX PVC storage")
			}

			By("waiting for PVC to be created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pvc", pvcName, "-n", testNamespace,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get PVC")
				g.Expect(output).To(Or(Equal("Bound"), Equal("Pending")), "PVC should exist")
			}).Should(Succeed())

			By("waiting for download job to complete successfully")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", "-l",
					"app.kubernetes.io/managed-by=modelrepository-controller,modelrepository.aitrigram.cliver-project.github.io/name="+modelRepoName,
					"-o", "jsonpath={.items[0].status.succeeded}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get job status")
				g.Expect(output).To(Equal("1"), "Download job should succeed")
			}).Should(Succeed())

			By("verifying ModelRepository status is 'downloaded'")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "modelrepository", modelRepoName,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get ModelRepository status")
				g.Expect(strings.ToLower(output)).To(Equal("downloaded"), "ModelRepository should be in downloaded phase")
			}).Should(Succeed())

			By("cleaning up ModelRepository")
			cmd = exec.Command("kubectl", "delete", "modelrepository", modelRepoName, "--wait=true")
			_, _ = utils.Run(cmd)

			By("cleaning up PVC")
			cmd = exec.Command("kubectl", "delete", "pvc", pvcName, "-n", testNamespace, "--wait=false")
			_, _ = utils.Run(cmd)
		})
	})

	Context("PVC ReadWriteOnce (RWO) Storage", func() {
		const modelRepoName = "ollama-pvc-rwo-test"
		const pvcName = "model-storage-rwo-test"

		It("should download Ollama model successfully with RWO PVC storage", func() {
			By("checking if a storage class is available")
			cmd := exec.Command("kubectl", "get", "storageclass")
			_, err := utils.Run(cmd)
			if err != nil {
				Skip("No storage class available - skipping RWO PVC test")
			}

			By("applying PVC and ModelRepository with RWO PVC storage")
			cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/storage/e2e-04-ollama-pvc-rwo.yaml", "-n", testNamespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply ModelRepository with RWO PVC storage")

			By("waiting for PVC to be created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pvc", pvcName, "-n", testNamespace,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get PVC")
				g.Expect(output).To(Or(Equal("Bound"), Equal("Pending")), "PVC should exist")
			}).Should(Succeed())

			By("waiting for download job to complete successfully")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", "-l",
					"app.kubernetes.io/managed-by=modelrepository-controller,modelrepository.aitrigram.cliver-project.github.io/name="+modelRepoName,
					"-o", "jsonpath={.items[0].status.succeeded}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get job status")
				g.Expect(output).To(Equal("1"), "Download job should succeed")
			}).Should(Succeed())

			By("verifying ModelRepository status is 'downloaded'")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "modelrepository", modelRepoName,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get ModelRepository status")
				g.Expect(strings.ToLower(output)).To(Equal("downloaded"), "ModelRepository should be in downloaded phase")
			}).Should(Succeed())

			By("verifying BoundNodeName is set in status for RWO storage")
			cmd = exec.Command("kubectl", "get", "modelrepository", modelRepoName,
				"-o", "jsonpath={.status.boundNodeName}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to get boundNodeName")
			Expect(output).NotTo(BeEmpty(), "BoundNodeName should be set for RWO storage")
			_, _ = fmt.Fprintf(GinkgoWriter, "Model bound to node: %s\n", output)

			By("cleaning up ModelRepository")
			cmd = exec.Command("kubectl", "delete", "modelrepository", modelRepoName, "--wait=true")
			_, _ = utils.Run(cmd)

			By("cleaning up PVC")
			cmd = exec.Command("kubectl", "delete", "pvc", pvcName, "-n", testNamespace, "--wait=false")
			_, _ = utils.Run(cmd)
		})
	})

	Context("NFS Storage", func() {
		const modelRepoName = "ollama-nfs-test"

		It("should download Ollama model successfully with NFS storage", func() {
			By("applying ModelRepository with NFS storage")
			cmd := exec.Command("kubectl", "apply", "-f", "test/e2e/storage/e2e-01-ollama-nfs.yaml")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply ModelRepository with NFS storage")

			By("waiting for download job to complete successfully")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", "-l",
					"app.kubernetes.io/managed-by=modelrepository-controller,modelrepository.aitrigram.cliver-project.github.io/name="+modelRepoName,
					"-o", "jsonpath={.items[0].status.succeeded}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get job status")
				g.Expect(output).To(Equal("1"), "Download job should succeed")
			}).Should(Succeed())

			By("verifying ModelRepository status is 'downloaded'")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "modelrepository", modelRepoName,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get ModelRepository status")
				g.Expect(strings.ToLower(output)).To(Equal("downloaded"), "ModelRepository should be in downloaded phase")
			}).Should(Succeed())

			By("cleaning up ModelRepository")
			cmd = exec.Command("kubectl", "delete", "modelrepository", modelRepoName, "--wait=true")
			_, _ = utils.Run(cmd)
		})
	})
})
