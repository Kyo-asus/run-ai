/*
 * SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 */

package helm_deployer

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Downloader control-plane chart branch", func() {
	Describe("downloadControlPlaneChart", func() {
		It("fails when the chart auth client is not configured", func() {
			d := &Downloader{
				ChartUrl:                 "https://cp.example.com/api/v1/ai-applications/charts/abc/content",
				RequiresControlPlaneAuth: true,
				ChartAuthClient:          nil,
			}

			_, err := d.Download()

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("authentication is not configured"))
		})

		It("rejects a non-https chart url", func() {
			d := &Downloader{
				ChartUrl:                 "http://cp.example.com/api/v1/ai-applications/charts/abc/content",
				RequiresControlPlaneAuth: true,
				ChartAuthClient:          http.DefaultClient,
			}

			_, err := d.Download()

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("requires https"))
		})

		It("downloads the chart body into a tgz file", func() {
			chartBytes := []byte("fake-chart-tgz-payload")
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(chartBytes)
			}))
			defer server.Close()

			d := &Downloader{
				ChartUrl:                 server.URL + "/chart.tgz",
				RequiresControlPlaneAuth: true,
				ChartAuthClient:          server.Client(),
			}

			path, err := d.Download()

			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(d.Cleanup)
			Expect(path).To(HaveSuffix(".tgz"))
			content, readErr := os.ReadFile(path)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(content).To(Equal(chartBytes))

			destDir := filepath.Dir(path)
			Expect(filepath.Base(destDir)).To(HavePrefix(ChartsDirName + "-"))
			dirInfo, statErr := os.Stat(destDir)
			Expect(statErr).NotTo(HaveOccurred())
			Expect(dirInfo.Mode().Perm()).To(Equal(os.FileMode(0o700)))

			Expect(d.Cleanup()).To(Succeed())
			Expect(destDir).NotTo(BeADirectory())
		})

		It("removes the temp dir when the chart body is cut short", func() {
			tempRoot := GinkgoT().TempDir()
			GinkgoT().Setenv("TMPDIR", tempRoot)

			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "100")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("partial"))
			}))
			defer server.Close()

			d := &Downloader{
				ChartUrl:                 server.URL + "/chart.tgz",
				RequiresControlPlaneAuth: true,
				ChartAuthClient:          server.Client(),
			}

			_, err := d.Download()

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("write control-plane chart temp file"))
			Expect(os.ReadDir(tempRoot)).To(BeEmpty())
		})

		It("fails on a non-200 response", func() {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()

			d := &Downloader{
				ChartUrl:                 server.URL + "/chart.tgz",
				RequiresControlPlaneAuth: true,
				ChartAuthClient:          server.Client(),
			}

			_, err := d.Download()

			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError("download control-plane chart: unexpected status 403 Forbidden"))
			Expect(d.tempDir).To(BeEmpty())
		})

		It("preserves the supplied client's redirect policy and error cause", func() {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/redirected-chart.tgz", http.StatusFound)
			}))
			defer server.Close()
			client := server.Client()
			redirectErr := errors.New("redirect rejected by caller")
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return redirectErr }
			d := &Downloader{
				ChartUrl:                 server.URL + "/chart.tgz",
				RequiresControlPlaneAuth: true,
				ChartAuthClient:          client,
			}

			_, err := d.Download()

			Expect(errors.Is(err, redirectErr)).To(BeTrue())
			Expect(err.Error()).To(HavePrefix("download control-plane chart: "))
			Expect(d.tempDir).To(BeEmpty())
		})
	})
})
