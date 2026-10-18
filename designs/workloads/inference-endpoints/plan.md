# InferenceEndpoint × Run:ai inference types — plan

Companion to the design pages in this folder (`index.html`, `lws.html`, `knative.html`,
`nimservice.html`). Point of view for the design review; phases below are proposals until agreed.

## TL;DR (the whole thing in 3 lines)

- One API call, two CRDs: the existing workload CRD per type stays as is; an `InferenceEndpoint`
  is created beside it and only changes the last hop (which Pod, chosen by the Endpoint Picker).
- Ship now: the side controller + the anyworkload plugin + the LWS adapter. NIM follows once auth
  is re-wired. Knative waits for a decision (Gateway failover hack, upstream change, or no Knative).
- Everything marked 🔜 PLANNED in the pages is deliberately **not** in this PR.

## What this PR contains

| Area | Content | State |
|---|---|---|
| `runai-cluster/cluster/inference-endpoint-controller` | the side controller: `InferenceEndpoint` CRD, renders HTTPRoute → InferencePool → EPP (+ rewrites) | 🆕 NEW |
| `runai-cluster/cluster/anyworkload-controller/plugins/inferenceendpoint` | creates the CR from the WorkloadRunner, owned by the workload, only when the CRD is installed | 🆕 NEW |
| `control-plane/workloads-manager/pkg/inferenceendpoint` | resolves intent: cluster defaults (controller + Gateway present → profile `gie-v1.5`), LWS adapter (leader selector, port, hostname) | 🆕 NEW |
| `runai-cluster/cluster/cluster-sync`, `control-plane/cluster-service` | readiness projection back to the control plane (`InferenceEndpointReady`), reported cluster config | 🆕 NEW |
| `runai-cluster/cluster/go-operator`, `runai-chart`, `runai-crd-upgrader` | operand, RBAC, CRD packaging | 🆕 NEW |
| `designs/workloads/inference-endpoints/*.html` | the design pages | 🆕 NEW |
| Knative | nothing | by design, see phase 3 |

Detailed implementation ledger from the integration work:
`runai-cluster/cluster/anyworkload-controller/designs/inference-endpoint-integration/2026-09-30-plan.md`.

## Phases

| Phase | Scope | Effort | Gate |
|---|---|---|---|
| **1 · LWS (DistributedInference)** | this PR. Pool = leaders (`lws name` + `worker-index "0"`), port = leader port or 4180 when protected | M, mostly E2E | HTTP readiness probe on the leader; `workload.port` correct for protected serving |
| **2 · NIMService** | pool = operator frontend Pods, HTTP target port. Auth: Gateway ext_authz → the existing oauth2-proxy Deployment (`/oauth2/auth`); `Ready=False` for protected NIM until the policy is bound | M public, L protected | operator Pod labels verified per operator version; ext_authz policy resource |
| **3 · Knative (Inference)** | decision first. A: Gateway backend failover to the Activator (no EPP fork, loses warm-path buffering and revision rollout). B: upstream `net-gateway-api` emits InferencePool. C: Deployment-backed stack (KServe RawDeployment) where EPP matters, Knative where scale-to-zero matters | A: S spike / M prod · B: XL calendar · C: M composer | which Gateway implementation (failover support); whether these workloads need burst buffering and weighted rollout |
| **4 · CRD additions** | `spec.objectives[]` (priority lanes → `InferenceObjective`), `routing.pathPrefixes`, `routing.requestTimeout`, `BackendsAvailable` condition, auth-port guard | S each | after phase 1 ships |

## Open decisions

| Decision | Options | Owner |
|---|---|---|
| Knative path | A / B / C above; recommendation on `knative.html` → Options & KServe | design review |
| Priority-lane ownership (`objectives[].priority`) | user per model vs admin per project | design review |
| Gateway implementation to standardise on | Envoy Gateway, kgateway, Istio, GKE; decides phase 3 option A viability | platform |
| KServe version | pins the LLMInferenceService column on `knative.html` ("verify version") | platform |

## Not in this PR, on purpose

- Any Knative mode in the controller or the plugin.
- `spec.objectives[]` and the other phase-4 fields (shown as 🔜 PLANNED in the CRD tree).
- A NIM auth policy resource; NIM public serving works, protected NIM is phase 2.

## 🔧 If you only do one thing

Review phase 1 end to end on `lws.html` → Architecture & flow. Every other phase reuses that mechanism.

## 🧠 Remember it as

The old CRD keeps owning the workload; the new CRD only owns the route to it.
