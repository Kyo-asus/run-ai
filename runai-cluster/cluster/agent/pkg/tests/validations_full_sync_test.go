/*
 * SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 */

package test

import (
	"context"
	"net/http"
	"strconv"

	"github.com/dsx-ai-factory/run-ai/runai-application-packages/convutils"
	assets_openapi "github.com/dsx-ai-factory/run-ai/runai-clients/assets-service-client/generated"
	agentHandlers "github.com/dsx-ai-factory/run-ai/runai-cluster/cluster/agent/pkg/handlers"
	clusterCommonConstants "github.com/dsx-ai-factory/run-ai/runai-cluster/cluster/common/constants"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// counterOfObjectChangedAfterBackup is the update counter of a cluster object that was changed after the backup
// the control plane database was restored from; it is higher than any update the restored control plane sends.
const counterOfObjectChangedAfterBackup = "100"

// valueOfSecretChangedAfterBackup is the secret data of a cluster secret that was changed after the backup.
const valueOfSecretChangedAfterBackup = "changed-after-backup"

// changeSecretAfterBackup stamps an existing cluster secret as changed after the backup.
func changeSecretAfterBackup(name, namespace string, k8sClient client.Client) {
	secret := &corev1.Secret{}
	ExpectWithOffset(1, k8sClient.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, secret)).To(Succeed())

	secret.Annotations[agentHandlers.UpdateCountAnnotation] = counterOfObjectChangedAfterBackup
	secret.Data = map[string][]byte{"key": []byte(valueOfSecretChangedAfterBackup)}
	ExpectWithOffset(1, k8sClient.Update(context.Background(), secret)).To(Succeed())
}

// createSecretChangedAfterBackup creates a cluster secret that the control plane will send as a new credential,
// as if it was created and then changed after the backup.
func createSecretChangedAfterBackup(name, namespace string, k8sClient client.Client) {
	secret := getBasicSecretObject(name, namespace, nil, assets_openapi.SCOPETYPE_PROJECT, Proj1IdStr, "password", nil)
	secret.Annotations = map[string]string{agentHandlers.UpdateCountAnnotation: counterOfObjectChangedAfterBackup}
	secret.Data = map[string][]byte{"key": []byte(valueOfSecretChangedAfterBackup)}
	ExpectWithOffset(1, k8sClient.Create(context.Background(), secret)).To(Succeed())
}

func expectSecretChangedAfterBackupIsKept(name, namespace string, k8sClient client.Client) {
	secret := getSecretFromClient(name, namespace, k8sClient)
	ExpectWithOffset(1, secret).ToNot(BeNil())
	ExpectWithOffset(1, secret.Annotations[agentHandlers.UpdateCountAnnotation]).To(Equal(counterOfObjectChangedAfterBackup))
	ExpectWithOffset(1, string(secret.Data["key"])).To(Equal(valueOfSecretChangedAfterBackup))
}

// expectSecretChangedAfterBackupIsOverridden checks that the secret carries the control plane update: its counter,
// and the data key the update sets (secret updates merge the data, so the key from after the backup stays).
func expectSecretChangedAfterBackupIsOverridden(name, namespace string, update assets_openapi.CredentialsUpdate, dataKey string, k8sClient client.Client) {
	secret := getSecretFromClient(name, namespace, k8sClient)
	ExpectWithOffset(1, secret).ToNot(BeNil())
	ExpectWithOffset(1, secret.Annotations[agentHandlers.UpdateCountAnnotation]).To(Equal(strconv.FormatInt(update.UpdateCount, 10)))
	ExpectWithOffset(1, secret.Data).To(HaveKey(dataKey))
}

// createWorkloadV2ChangedAfterBackup creates a cluster workload runner that the control plane will send as a new
// workload, as if it was created and then changed after the backup.
func createWorkloadV2ChangedAfterBackup(name, namespace, app string, k8sClient client.Client) {
	runner := getBasicWorkloadV2(name, namespace, 0, generateSampleDeployment(app))
	runner.SetAnnotations(map[string]string{clusterCommonConstants.AnnotationsBackendWorkloadCounter: counterOfObjectChangedAfterBackup})
	ExpectWithOffset(1, k8sClient.Create(context.Background(), runner)).To(Succeed())
}

func expectWorkloadV2(name, namespace string, k8sClient client.Client, counter, app string) {
	runner := getWorkloadV2FromClient(name, namespace, createdWorkloadC.GetObjectKind().GroupVersionKind(), k8sClient)
	ExpectWithOffset(1, runner).ToNot(BeNil())
	ExpectWithOffset(1, runner.GetAnnotations()[clusterCommonConstants.AnnotationsBackendWorkloadCounter]).To(Equal(counter))

	appLabel, _, err := unstructured.NestedString(runner.Object, "spec", "template", "spec", "selector", "matchLabels", "app")
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, appLabel).To(Equal(app))
}

// credentialsUpdatesOneAtATime serves the credentials sync like the default mock, but returns a single update per
// request, so the agent goes through several batches.
func credentialsUpdatesOneAtATime(writer http.ResponseWriter, request *http.Request) {
	after := int64(getIntFromQuery(request.URL.Query(), "after"))
	resObj := &assets_openapi.CredentialsUpdatesResp{
		RequestInfo:  &assets_openapi.RequestInfo{AfterUpdate: after},
		ResponseInfo: &assets_openapi.ResponseInfo{},
		Assets:       []assets_openapi.CredentialsUpdate{},
	}

	var next *assets_openapi.CredentialsUpdate
	for i := range allCredentialsUpdates {
		update := &allCredentialsUpdates[i]
		if update.UpdateCount > after && (next == nil || update.UpdateCount < next.UpdateCount) {
			next = update
		}
	}
	if next != nil {
		resObj.ResponseInfo.LowestUpdate = convutils.Ref(next.UpdateCount)
		resObj.ResponseInfo.HighestUpdate = convutils.Ref(next.UpdateCount)
		resObj.Assets = append(resObj.Assets, *next)
	}

	ghttp.RespondWithJSONEncoded(http.StatusOK, resObj)(writer, request)
}
