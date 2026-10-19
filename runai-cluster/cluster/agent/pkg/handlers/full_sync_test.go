/*
 * SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 */

package handlers

import (
	"context"

	_ "github.com/dsx-ai-factory/run-ai/runai-cluster/cluster/agent/pkg/tests/helper"

	assetsApi "github.com/dsx-ai-factory/run-ai/runai-clients/assets-service-client/generated"
	runaiBackendClient "github.com/dsx-ai-factory/run-ai/runai-cluster/cluster/agent/pkg/backend-client"
	configmaps "github.com/dsx-ai-factory/run-ai/runai-cluster/cluster/agent/pkg/config-maps"
	clusterCommonConstants "github.com/dsx-ai-factory/run-ai/runai-cluster/cluster/common/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("Full sync", func() {
	Context("applying a credential that the cluster has with a newer update counter", func() {
		var (
			fakeClient client.Client
			handler    *CommonHandler
		)

		secret := func(counter, value string) *corev1.Secret {
			return &corev1.Secret{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
				ObjectMeta: metav1.ObjectMeta{
					Name:        "dockerregistry-cred",
					Namespace:   "runai",
					Annotations: map[string]string{UpdateCountAnnotation: counter},
				},
				Data: map[string][]byte{"key": []byte(value)},
			}
		}

		clusterSecret := func() *corev1.Secret {
			s := &corev1.Secret{}
			Expect(fakeClient.Get(context.Background(), types.NamespacedName{Namespace: "runai", Name: "dockerregistry-cred"}, s)).To(Succeed())
			return s
		}

		BeforeEach(func() {
			// the secret was updated on the cluster after the backup the control plane database was restored from.
			// the interceptor sets the GVK on fetched objects, as the agent's cached client does.
			fakeClient = fake.NewClientBuilder().
				WithObjects(secret("70", "changed-after-backup")).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if err := c.Get(ctx, key, obj, opts...); err != nil {
							return err
						}
						gvk, err := apiutil.GVKForObject(obj, c.Scheme())
						if err != nil {
							return err
						}
						obj.GetObjectKind().SetGroupVersionKind(gvk)
						return nil
					},
				}).
				Build()

			var err error
			handler, err = NewCommonHandler(&runaiBackendClient.BackendClient{}, fakeClient, fakeClient, &corev1.Secret{},
				AssetsObjectInspector(false), HandlingOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		It("keeps the cluster object outside a full sync", func() {
			Expect(handler.CreateOrUpdateObject(secret("62", "from-backup"), "")).To(Succeed())

			Expect(clusterSecret().Annotations[UpdateCountAnnotation]).To(Equal("70"))
			Expect(string(clusterSecret().Data["key"])).To(Equal("changed-after-backup"))
		})

		It("applies the control plane version and its counter during a full sync", func() {
			handler.fullSync = true

			Expect(handler.CreateOrUpdateObject(secret("62", "from-backup"), "")).To(Succeed())

			Expect(clusterSecret().Annotations[UpdateCountAnnotation]).To(Equal("62"))
			Expect(string(clusterSecret().Data["key"])).To(Equal("from-backup"))
		})

		It("applies an update with the same counter during a full sync", func() {
			handler.fullSync = true

			Expect(handler.CreateOrUpdateObject(secret("70", "same-counter"), "")).To(Succeed())

			Expect(clusterSecret().Annotations[UpdateCountAnnotation]).To(Equal("70"))
			Expect(string(clusterSecret().Data["key"])).To(Equal("same-counter"))
		})
	})

	Context("starting the credentials handler", func() {
		newHandler := func(data map[string]string) *AssetsHandler {
			agentConfig := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: configmaps.AgentConfigName, Namespace: clusterCommonConstants.NamespaceRunAi},
				Data:       data,
			}
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(agentConfig).Build()

			handler, err := NewCredentialAssetsHandler(&runaiBackendClient.BackendClient{}, fakeClient, fakeClient)
			Expect(err).NotTo(HaveOccurred())
			return handler.(*AssetsHandler)
		}

		It("starts a full sync when agent-config sets its counter to -1", func() {
			handler := newHandler(map[string]string{configmaps.CredentialsSyncCounter: "-1"})

			Expect(handler.syncCounter).To(Equal(FullSyncCounter))
		})

		It("resumes from the stored counter otherwise", func() {
			handler := newHandler(map[string]string{configmaps.CredentialsSyncCounter: "68"})

			Expect(handler.syncCounter).To(Equal(int64(68)))
		})

		It("keeps the first-time sync when agent-config has no counter for it", func() {
			handler := newHandler(map[string]string{configmaps.DatasourcesSyncCounter: "5"})

			Expect(handler.syncCounter).To(Equal(int64(0)))
		})
	})

	It("k8s-objects handler marks the batch of every kind", func() {
		handler := &K8sObjectsHandler{kindHandlers: map[assetsApi.AssetKind]*CommonHandler{
			assetsApi.ASSETKIND_CONFIG_MAP:              {},
			assetsApi.ASSETKIND_RESOURCE_CLAIM_TEMPLATE: {},
		}}

		handler.setFullSync(true)
		for _, kindHandler := range handler.kindHandlers {
			Expect(kindHandler.fullSync).To(BeTrue())
		}

		handler.setFullSync(false)
		for _, kindHandler := range handler.kindHandlers {
			Expect(kindHandler.fullSync).To(BeFalse())
		}
	})
})
