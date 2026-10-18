# Running AI Agents with NVIDIA OpenShell on NVIDIA Run:ai

This tutorial demonstrates how to run autonomous AI agents in NVIDIA OpenShell sandboxes on an NVIDIA Run:ai-managed Kubernetes cluster, with one governed workspace for each NVIDIA Run:ai project. Agents are scheduled under their project's quota like any other workload. You can adapt this workflow for other teams, projects, model endpoints, and agent harnesses.

In this tutorial, you will learn how to:

* Install the OpenShell gateway on an NVIDIA Run:ai cluster in `operator` workspace mode
* Connect the gateway to your identity provider and sign users in
* Create one OpenShell workspace per NVIDIA Run:ai project and add users to it
* Give agents model access through providers that keep the credential in the gateway, including a model served on NVIDIA Run:ai behind OIDC authentication
* Run a bug-fix agent with Claude Code, check the result yourself, and use other agent harnesses
* Verify that users are confined to their workspaces
* View and govern agents as NVIDIA Run:ai workloads

## How It Fits Together

<img src="../../assets/openshell-on-runai-architecture.svg" alt="OpenShell on NVIDIA Run:ai: the user's openshell CLI signs in through the identity provider and calls the OpenShell gateway over gRPC with an OIDC token through the ingress. The gateway runs in the openshell namespace and checks the token's groups claim. Workspace runai-team-a maps to namespace runai-team-a, the namespace of NVIDIA Run:ai project team-a, where the agent pod and supervisor pod run, all scheduled by runai-scheduler. A platform administrator creates the workspace and adds each user to it." />

- **One OpenShell workspace per NVIDIA Run:ai project.** In `operator` mode, the workspace `runai-team-a` runs its sandboxes in the existing namespace `runai-team-a`, the namespace of NVIDIA Run:ai project `team-a`. The NVIDIA Run:ai admission controller assigns every pod there to `runai-scheduler` and the project's queue, so the project's quota, fairness and node pools apply to agents exactly as they do to other workloads.
- **Users sign in with your identity provider.** The gateway accepts any OIDC identity provider (IdP) that puts a `groups` claim in its tokens. Users do not need NVIDIA Run:ai accounts. The token's subject, an identifier chosen by the IdP, is the identity OpenShell records and checks.
- **A platform administrator decides who can use which workspace.** OpenShell workspace membership is the access control: a user can create and use sandboxes only in the workspaces they are members of.
- **Each agent runs as an agent pod and an OpenShell supervisor pod** in the project namespace. The supervisor enforces the sandbox policy (which binaries may reach which endpoints) and injects real credentials into approved requests, so the agent holds only a placeholder.
- **The OpenShell gateway** is platform infrastructure. It runs outside the NVIDIA Run:ai projects, so project quota covers the agents.

## Who Does What

This tutorial involves three roles. A single person can hold more than one role. The following table describes each role:

| Role | Who they are | Responsible for | Works with |
|---|---|---|---|
| **System administrator** | Cluster and infrastructure administrator. The identity provider (IdP) administrator joins for the IdP steps. | Installs and configures the OpenShell gateway: Helm charts, IdP settings, network policy, ingress and certificates | `kubectl`, Helm, the IdP admin console |
| **Platform administrator** | A member of the IdP group `openshell-admins` | Runs OpenShell for the organization: creates a workspace for each NVIDIA Run:ai project, adds users to workspaces, and creates the providers that supply model credentials. Can delegate adding users to workspace admins. | The `openshell` CLI. |
| **User** | A member of the IdP group `openshell-users` and of one or more workspaces | Runs agents in their team's workspace | The `openshell` CLI |

The following table shows which role performs each step:

| Step | Role |
|---|---|
| [Step 1: Installing OpenShell](#step-1-installing-openshell) | System administrator |
| [Step 2: Signing In to OpenShell](#step-2-signing-in-to-openshell) | System administrator registers the OIDC client; then everyone signs in |
| [Step 3: Creating a Workspace for Each Project](#step-3-creating-a-workspace-for-each-project) | Platform administrator |
| [Step 4: Adding Users to Workspaces](#step-4-adding-users-to-workspaces) | Platform administrator (workspace admins can add users to their own workspace) |
| [Step 5: Giving Agents Model Access](#step-5-giving-agents-model-access) | Platform administrator |
| [Steps 6 to 8: Running Agents](#step-6-running-a-bug-fix-agent) | User |
| [Step 9: Viewing and Governing Agents in NVIDIA Run:ai](#step-9-viewing-and-governing-agents-in-nvidia-runai) | Platform administrator, and NVIDIA Run:ai administrators in the NVIDIA Run:ai UI |
| [Step 10: Cleaning Up the Environment](#step-10-cleaning-up-the-environment) | User, platform administrator, and system administrator |

## Prerequisites

Before you start, make sure the following requirements are met. They are grouped by the step that needs them.

### Client Tools

| Who | Needs | Used in |
|---|---|---|
| System administrator | `kubectl` with cluster-administrator access, and Helm 3.x | [Step 1](#step-1-installing-openshell) |
| Platform administrator | The `openshell` CLI, a web browser for sign-in, and `curl` to check the model endpoint | [Step 2](#step-2-signing-in-to-openshell) to [Step 5](#step-5-giving-agents-model-access) |
| User | The `openshell` CLI (the version that matches the gateway) and a web browser for sign-in. Users work from their own machines through the CLI. | [Step 2](#step-2-signing-in-to-openshell) and [Step 6](#step-6-running-a-bug-fix-agent) to [Step 8](#step-8-using-other-agent-harnesses) |

### For Step 1: Installing OpenShell

Needed by the system administrator:

- **A CNI that enforces `NetworkPolicy`.** OpenShell isolates agent pods with NetworkPolicies, and enforcement keeps every request going through the supervisor. On Amazon EKS, turn on network policy support in the Amazon VPC CNI (`enable-network-policy-controller: "true"` in the `amazon-vpc-cni` ConfigMap and `--enable-network-policy=true` on the `aws-eks-nodeagent` container).

For the validated versions, refer to [Scope and Limitations](#scope-and-limitations).

### For Step 2: Signing In to OpenShell

In [Step 2](#step-2-signing-in-to-openshell), the system administrator, with the IdP administrator, registers an OIDC client for the OpenShell CLI in your identity provider so that users can sign in.

- **An OIDC identity provider (IdP)** that your users already sign in to, such as Okta, Microsoft Entra ID or Keycloak. It must issue tokens with a `groups` claim, and you must be able to register an OIDC client in it for the OpenShell CLI.
- **Two IdP groups.** OpenShell decides who is a user and who is a platform administrator from the `groups` claim of the user's token. Create, for example, `openshell-users` (everyone who runs agents) and `openshell-admins` (the platform team). The IdP administrator adds people to these groups.

### For Steps 3 and 4: Creating Workspaces and Adding Users

An NVIDIA Run:ai [project](../../platform-management/aiinitiatives/organization/projects.md) for the team that runs agents, with the quota you want to limit its agents to. This tutorial uses the project `team-a` and the user `user@example.com`. Users do not need NVIDIA Run:ai accounts.

Agents run in the project under its quota. In [Step 3](#step-3-creating-a-workspace-for-each-project), the platform administrator creates an OpenShell workspace that matches the project, and in [Step 4](#step-4-adding-users-to-workspaces) adds the team's users to it.

### For Step 5: Giving Agents Model Access

In [Step 5](#step-5-giving-agents-model-access), the platform administrator connects the agents to the model. The agents use it in [Step 6](#step-6-running-a-bug-fix-agent) and [Step 8](#step-8-using-other-agent-harnesses).

- **A model endpoint reachable from the cluster**, with its API key if the model runs outside NVIDIA Run:ai.
- **An Anthropic-compatible API (`/v1/messages`) with tool calling.** Use your organization's model gateway or a vLLM server on NVIDIA Run:ai ([Step 5](#step-5-giving-agents-model-access)). The Claude Code example in [Step 6](#step-6-running-a-bug-fix-agent) calls this API. The model needs a context window of about 32,000 tokens and must be strong enough to use tools.

### Values to Replace

Throughout this tutorial, replace:

| Placeholder | Meaning | Example |
|---|---|---|
| `<idp-issuer-url>` | Issuer URL of your OIDC identity provider | `https://idp.example.com/realms/corp` |
| `<runai-url>` | NVIDIA Run:ai control-plane hostname | `runai.example.com` |
| `<gateway-host>` | Hostname users connect to for the OpenShell gateway, under NVIDIA Run:ai's wildcard domain | `openshell.runai.example.com` |
| `<project>` | The NVIDIA Run:ai project that runs agents | `team-a` |
| `runai-<project>` | A project's Kubernetes namespace, and its OpenShell workspace | `runai-team-a` |
| `<model-host>` | Hostname of your model endpoint | `llm-gateway.example.com` |
| `<model-name>` | Model the agent uses, as named by your endpoint | `Qwen/Qwen3-8B-AWQ` |

## Step 1: Installing OpenShell

**Role: system administrator.**

Install OpenShell on the cluster by following the OpenShell [Kubernetes setup](https://docs.nvidia.com/openshell/latest/kubernetes/setup) guide. It installs the Kubernetes [Agent Sandbox](https://agent-sandbox.sigs.k8s.io) controller and the OpenShell gateway chart. This tutorial uses OpenShell **0.1.3** and Agent Sandbox **1.0.5**. Apply these NVIDIA Run:ai-specific settings while you follow it. The sections below cover each one.

### Configuring the Gateway Values

Run the gateway in `operator` workspace mode over the NVIDIA Run:ai project namespaces, and authenticate users against your identity provider, with roles from IdP groups.

**Where agents run**

* `workspaceMode: operator` - Runs each sandbox in an existing Kubernetes namespace instead of creating one. The workspace `runai-<project>` runs in the namespace with that name.
* `operatorNamespaceLabel: runai/queue` - Treats every namespace with this label as a place where agents can run. NVIDIA Run:ai puts the label on every project namespace, so each project can host agents under its quota.
* `workspaceResources.enabled: false` - Leaves the per-namespace resources to the `openshell-workspace` chart, which you install in [Installing the Workspace Chart](#installing-the-workspace-chart).

**How users are verified** (for more settings, refer to [Authentication](https://docs.nvidia.com/openshell/latest/how-it-works/gateways/authentication))

* `issuer` - The identity provider whose tokens the gateway trusts.
* `audience` - The audience the gateway requires in a token. Your IdP must put it in the tokens issued for OpenShell, as you configure in [Registering the OIDC Client](#registering-the-oidc-client).
* `caConfigMapName` - Optional. The CA bundle for an IdP that uses a private CA, described below.

<Note>

**Note**

If your IdP uses a certificate issued by a private CA, put the CA bundle in a ConfigMap and reference it with `caConfigMapName`. With it, the gateway validates the tokens of your IdP. The same applies to an NVIDIA Run:ai-served model endpoint that the gateway fetches tokens for ([Step 5](#step-5-giving-agents-model-access)).

</Note>

**How roles are assigned** (for more settings, refer to [Access Control](https://docs.nvidia.com/openshell/latest/kubernetes/access-control))

* `rolesClaim` - The token field that lists the user's IdP groups.
* `userRole` - The group whose members can use the workspaces they belong to.
* `adminRole` - The group whose members are platform administrators, who create workspaces and manage members.

Use the following values file when you install the gateway chart:

```yaml
# openshell-values.yaml: OpenShell gateway on an NVIDIA Run:ai cluster
workspaceResources:
  enabled: false                     # per-namespace resources come from the workspace chart (see Installing the Workspace Chart)
server:
  drivers:
    kubernetes:
      workspaceMode: operator        # workspace "runai-<project>" runs in that existing namespace
      operatorNamespaceLabel: runai/queue   # every Run:ai project namespace may host agents
  oidc:
    issuer: <idp-issuer-url>
    audience: openshell              # the audience your IdP puts in tokens issued for OpenShell
    rolesClaim: groups               # the token claim that lists the user's IdP groups
    userRole: openshell-users        # may use the workspaces they are members of
    adminRole: openshell-admins      # platform administrators: create workspaces, manage members
    # caConfigMapName: idp-ca        # only if the IdP uses a private CA (see below)
```

### Installing the Workspace Chart

In operator mode, install the `openshell-workspace` chart into each NVIDIA Run:ai project namespace that runs agents, as described under [Split gateway and workspace releases](https://docs.nvidia.com/openshell/latest/kubernetes/setup#split-gateway-and-workspace-releases) in the setup guide. The chart is the only per-project step on the cluster.

The chart creates:

* The sandbox ServiceAccount
* The gateway's Role for Sandboxes and Secrets
* The sandbox NetworkPolicy

Keep in mind:

* This is one install for each project that runs agents, and it is part of onboarding every new project.
* OpenShell works this way on purpose: the gateway reads Secrets only in the namespaces where this chart's Role grants it.

Install the chart in the namespace of the project:

```bash
helm upgrade --install openshell-workspace oci://ghcr.io/nvidia/openshell/openshell-workspace \
  --version 0.1.3 --namespace runai-team-a
```

For each project you add later, run the same command with that project's namespace.

<Note>

**Note**

If you onboard projects often, automate this step with the tooling you already use to configure new namespaces, such as a GitOps release per project namespace.

</Note>

### Verifying the Gateway

Check the gateway logs to confirm that it accepts tokens from your IdP and has found the project namespaces:

```bash
kubectl -n openshell logs openshell-0 | grep -E "OIDC|operator namespace"
```

```text
OIDC JWT validation enabled (issuer: <idp-issuer-url>)
operator namespace label watcher spawned label_selector=runai/queue
operator namespace allowlist replaced from full relist total=2
```

### Exposing the Gateway

Expose the gateway to users through your cluster's ingress controller, so that they connect to a hostname with a certificate from their own machines.

This tutorial uses what your NVIDIA Run:ai installation already has, plus a hostname for the gateway:

* **Ingress controller** - Routes traffic to the gateway.
* **DNS** - The wildcard record already points names under the domain at the ingress controller.
* **Certificate** - The controller already serves the wildcard certificate for any name under the domain, so the gateway's ingress uses it.
* **Hostname** - `<gateway-host>`, which you choose under the same wildcard domain, for example `openshell.<runai-domain>`.

Traffic to the gateway is protected as follows:

* TLS ends at the ingress, and the hop from the ingress to the gateway pod stays encrypted.
* The gateway still authenticates every request by its OIDC token.

To expose the gateway:

1. Create the ingress for your controller. NVIDIA Run:ai recommends the HAProxy Kubernetes Ingress Controller. Clusters that were upgraded from an earlier version can still run NGINX. In both cases:

   * The gateway speaks gRPC, so the ingress must use HTTP/2 to reach the gateway pod.
   * Long timeouts keep streaming commands (exec, upload, and download) open.

<Tabs>

<Tab title="HAProxy (recommended)">

```yaml
# openshell-gateway-ingress-haproxy.yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: openshell-gateway
  namespace: openshell
  annotations:
    haproxy.org/server-ssl: "true"      # encrypt the hop to the gateway pod
    haproxy.org/server-proto: "h2"      # gRPC needs HTTP/2 to the gateway pod
    haproxy.org/timeout-server: "1h"    # keep streaming commands open
    haproxy.org/timeout-client: "1h"
    haproxy.org/timeout-tunnel: "1h"
spec:
  ingressClassName: haproxy
  rules:
    - host: <gateway-host>
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: openshell
                port:
                  number: 8080
```

</Tab>

<Tab title="NGINX">

```yaml
# openshell-gateway-ingress-nginx.yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: openshell-gateway
  namespace: openshell
  annotations:
    nginx.ingress.kubernetes.io/backend-protocol: "GRPCS"      # gRPC over TLS to the gateway pod
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"     # keep streaming commands open
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-body-size: "0"           # file uploads and downloads
spec:
  ingressClassName: nginx
  rules:
    - host: <gateway-host>
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: openshell
                port:
                  number: 8080
```

</Tab>

</Tabs>

2. Apply the file for your controller:

```bash
kubectl apply -f openshell-gateway-ingress-haproxy.yaml   # or openshell-gateway-ingress-nginx.yaml
```

<Note>

**Note**

To expose the gateway through the Kubernetes Gateway API instead, follow the OpenShell [Ingress](https://docs.nvidia.com/openshell/latest/kubernetes/ingress) guide.

</Note>

### Restricting In-Cluster Access

Restrict in-cluster access to the gateway API with a NetworkPolicy that admits only two kinds of traffic:

* **OpenShell supervisors** in NVIDIA Run:ai project namespaces.
* **The ingress controller**, so that users can reach the gateway.

To restrict access:

1. Create the NetworkPolicy. The first rule admits the supervisors, and the second rule admits the ingress controller. The second rule is for HAProxy. For NGINX, use the commented rule at the end of the file instead.

```yaml
# openshell-gateway-netpol.yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: openshell-gateway-ingress
  namespace: openshell
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: openshell
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchExpressions:
              - key: runai/queue          # every Run:ai project namespace
                operator: Exists
          podSelector:
            matchLabels:
              openshell.ai/managed-by: openshell
      ports:
        - port: 8080
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: haproxy-controller   # the namespace of your ingress controller
          podSelector:
            matchLabels:
              app.kubernetes.io/name: kubernetes-ingress
      ports:
        - port: 8080
    # For NGINX, use this rule instead:
    # - from:
    #     - namespaceSelector:
    #         matchLabels:
    #           kubernetes.io/metadata.name: nginx-ingress
    #       podSelector:
    #         matchLabels:
    #           app.kubernetes.io/name: ingress-nginx
    #   ports:
    #     - port: 8080
```

2. Apply the NetworkPolicy:

```bash
kubectl apply -f openshell-gateway-netpol.yaml
```

When the gateway is ready, give the platform administrators and users the gateway address, `<gateway-host>`. They use it to sign in in [Step 2](#signing-in-to-the-gateway).

## Step 2: Signing In to OpenShell

**Role: the system administrator registers the OIDC client. Then every platform administrator and user installs the CLI and signs in.**

Every user, including platform administrators, signs in to OpenShell through your identity provider. The resulting token names the user (`sub`, an identifier chosen by the IdP) and carries their IdP groups, which OpenShell maps to the `openshell-users` and `openshell-admins` roles.

### Registering the OIDC Client

As the system administrator, register an OIDC client for the OpenShell CLI in your IdP, once. The CLI signs users in with a browser (authorization code flow with PKCE) and receives the token on a local port. Configure the client as:

* **Client ID:** `openshell-cli`
* **Client type:** public
* **Valid redirect URIs:** `http://127.0.0.1:*` (the CLI listens on a random local port)
* **Token contents:** the `groups` claim, and the audience set in the gateway values (`openshell`)

Use the IdP's own client-registration steps. The client must be separate from other applications, so that you can change its redirect URIs and claims independently.

### Installing the OpenShell CLI

Everyone installs the OpenShell CLI matching the gateway version, as described in [Installation](https://docs.nvidia.com/openshell/latest/about/installation).

### Signing In to the Gateway

Everyone registers the gateway and signs in. Before you start:

* Use the hostname of the gateway's ingress ([Step 1](#exposing-the-gateway)).
* A certificate from a public certificate authority works as is. For a certificate from a private authority, first install that authority in the trust store of each user's machine.

To register the gateway and sign in:

1. Register the gateway. The command with the OIDC options registers the gateway and opens a browser to sign in:

```bash
openshell gateway add https://<gateway-host> --name runai \
  --oidc-issuer <idp-issuer-url> --oidc-client-id openshell-cli --oidc-audience openshell
```

2. Sign in through the browser, and then check the connection:

```bash
openshell status
```

The output shows the signed-in user:

```text
✓ Authenticated to gateway 'runai' as user@example.com
  Endpoint: https://<gateway-host>
  Auth: oidc
```

The CLI stores a refresh token, so users sign in again only when the IdP session ends. To sign in again, run `openshell gateway login runai`.

<Note>

**Note**

If your IdP ends idle sessions quickly (Keycloak's default is 30 minutes), users are asked to sign in again often. Choose session lifetimes that suit how long agents run.

</Note>

### Checking the User's Identity and Roles

After signing in, check the user's identity and roles with `openshell whoami`:

```bash
openshell whoami
```

```text
Current User

  Subject: 95df8792-5a02-4e09-9329-4bf483f70581
  Name: user@example.com
  Provider: oidc
  Roles: openshell-users
```

The output shows:

* **Subject** - The token's `sub` claim, which is the user's identity in OpenShell. Most identity providers issue an opaque identifier as the subject. Platform administrators need this value to add the user to a workspace ([Step 4](#step-4-adding-users-to-workspaces)).
* **Roles** - The role that OpenShell maps from the user's IdP groups: `openshell-users` or `openshell-admins`.

If the roles look wrong:

* `openshell status` can show `Authenticated (OIDC; authorization denied)` for a user with the `openshell-users` role while every command in their workspaces works.
* If `whoami` shows no roles, or the commands fail with `role 'openshell-users' required`, the token needs a `groups` claim with `openshell-users` or `openshell-admins`. To fix it:
  * Decode the token's payload to check its claims.
  * Fix the client's claim mapping in your IdP.

## Step 3: Creating a Workspace for Each Project

**Role: platform administrator.**

As a **platform administrator** (a user in the `openshell-admins` group), create one [workspace](https://docs.nvidia.com/openshell/latest/how-it-works/workspaces) per NVIDIA Run:ai project. In operator mode, the workspace name must equal the project's namespace name.

For every new project, the order is:

1. The NVIDIA Run:ai project exists.
2. The system administrator installs the `openshell-workspace` chart in the project's namespace ([Step 1](#installing-the-workspace-chart)).
3. You create the workspace:

```bash
openshell workspace create --name runai-team-a
openshell workspace list
```

```text
NAME          STATUS        CREATED
default       Active        2026-10-05 12:55:36
runai-team-a  Active        2026-10-07 10:17:23
```

<Note>

**Note**

**Name limit.** OpenShell workspace names are at most 19 characters, so NVIDIA Run:ai project names used for agents can have at most 13 characters (`runai-` + name).

</Note>

## Step 4: Adding Users to Workspaces

**Role: platform administrator. A workspace admin can also add users to their own workspace.**

A workspace admits only its members. A **platform administrator** adds each user to the workspace of the project they work in.

A member is identified by the `subject` of their token, the value the user sees in `openshell whoami` ([Step 2](#checking-the-users-identity-and-roles)), and has one of two roles in that workspace. In this tutorial, `<user-subject>` stands for that value for the user:

| Workspace role | Can |
|---|---|
| `user` | Create and use sandboxes in the workspace |
| `admin` | Also manage the workspace's providers and policies, and add or remove `user` members of that workspace. Only a platform administrator can assign the `admin` role. |

```bash
openshell workspace member add --workspace runai-team-a --subject <user-subject> --role user
openshell workspace member list --workspace runai-team-a
```

Keep in mind:

* Members are added one subject at a time.
* To find a user's subject, ask them to run `openshell whoami` and send you the `Subject` line.
* To remove access, run `openshell workspace member remove` with the same workspace and subject.
* Platform administrators can manage any workspace. A workspace `admin` can add and remove `user` members in their own workspace, which lets a team lead onboard teammates without a platform administrator.

The user now sees only their team's workspace. Set it as the default so that commands use it by default:

```bash
openshell workspace list
export OPENSHELL_WORKSPACE=runai-team-a
```

```text
NAME          STATUS        CREATED
runai-team-a  Active        2026-10-07 10:17:23
```

## Step 5: Giving Agents Model Access

**Role: platform administrator.**

OpenShell delivers credentials through [providers](https://docs.nvidia.com/openshell/latest/how-it-works/providers/overview). A provider profile declares the credential, the endpoints it may be sent to, and the binaries allowed to reach those endpoints. The sandbox receives only a placeholder value. The supervisor proxy substitutes the real key, and only for requests to the declared endpoints.

Where the model runs decides the credential: a model served on NVIDIA Run:ai uses an NVIDIA Run:ai access key (option A), and a model from another service uses its API key (option B). Either way, model access is set up at two levels:

- **The provider profile** is the definition. Import it once for the whole platform (`--global`) so that every workspace can use it, or into one workspace to keep it private to that team.
- **The provider** holds the actual credential and belongs to **one workspace**. Only sandboxes in that workspace can use it. Create a provider in each workspace whose agents need the model, with that team's own credential. Each workspace needs its own provider to reach the model. A workspace `admin` ([Step 4](#step-4-adding-users-to-workspaces)) can also manage the providers of their own workspace.

### Option A: A Model Served on NVIDIA Run:ai (Access Key)

NVIDIA Run:ai can serve a model, for example with vLLM or an NVIDIA NIM, as an inference workload whose endpoint accepts only requests with a valid NVIDIA Run:ai access token. A token lasts about 30 minutes, so the gateway uses an NVIDIA Run:ai access key to fetch and renew it, and never gives the key to the agent.

1. **Deploy the model in NVIDIA Run:ai** as an inference workload, following [Hugging Face inference](../../workloads-in-nvidia-run-ai/using-inference/hugging-face-inference.md) or [NVIDIA NIM inference](../../workloads-in-nvidia-run-ai/using-inference/nim-inference.md). Set access to **All authenticated users and service accounts**, so that the endpoint requires a token.

   For an agent harness such as Claude Code, the model server must:

   * Serve Anthropic's Messages API (`/v1/messages`), which vLLM does.
   * Have tool calling enabled.
   * Have a context window of about 32,000 tokens and be strong enough to use tools.

   This tutorial uses `Qwen/Qwen3-8B-AWQ`, a 4-bit model, on one NVIDIA A10 (24 GB), with these vLLM arguments:

   ```text
   --model Qwen/Qwen3-8B-AWQ --max-model-len 32768 --gpu-memory-utilization 0.92 --enable-auto-tool-choice --tool-call-parser hermes --default-chat-template-kwargs '{"enable_thinking": false}'
   ```

   * `--max-model-len 32768` - Sets the context window to about 32,000 tokens.
   * `--enable-auto-tool-choice --tool-call-parser hermes` - Enables tool calling for a Qwen model.
   * `--gpu-memory-utilization 0.92` - Lets vLLM use 92% of the GPU memory.
   * `--default-chat-template-kwargs '{"enable_thinking": false}'` - Turns off the model's thinking mode.

2. **Create the provider profile** in the workspace that uses the model. Take `<endpoint-host>` from the endpoint URL in the workload's **Connections** column.

   The profile defines:

   * `credentials` - The environment variable the sandbox sees, `RUNAI_MODEL_TOKEN`, which holds a placeholder. The supervisor replaces it with the real token on requests to the endpoint.
   * `refresh` - How the gateway gets the real token: it uses the access key in an OAuth2 client credentials request to `token_url`, and renews the token `refresh_before_seconds` (300) before it expires.
   * `endpoints` - The model endpoint that the credential may be sent to.
   * `binaries` - The programs allowed to reach the endpoint. List both paths, because the supervisor matches the resolved binary.

   ```yaml
   id: runai-model
   display_name: Model served on Run:ai
   description: A Run:ai inference endpoint protected by OIDC authentication
   category: inference
   inference_capable: true
   credentials:
     - name: access_token
       description: Run:ai access token, fetched and refreshed by the gateway
       env_vars: [RUNAI_MODEL_TOKEN]
       required: true
       auth_style: bearer
       header_name: authorization
       refresh:
         strategy: oauth2_client_credentials
         token_url: https://<runai-url>/auth/realms/runai/protocol/openid-connect/token
         refresh_before_seconds: 300
   discovery:
     credentials: [access_token]
   endpoints:
     - host: <endpoint-host>
       port: 443
       protocol: rest
       access: read-write
       enforcement: enforce
   binaries:                                   # the supervisor matches the resolved binary, so list both paths
     - /sandbox/tools/bin/claude
     - /sandbox/tools/lib/node_modules/@anthropic-ai/**
   ```

   Set `refresh_before_seconds` larger than the longest outage of the token endpoint you want to survive.

3. **Create the provider and give it the access key.** First create an NVIDIA Run:ai [access key](../../settings/user-settings/user-access-keys.md) for the team, or use a service account. Use one per team. The key stays in the gateway.

   The commands do the following:

   * `provider profile import` - Imports the profile into the workspace.
   * `provider create` - Creates the provider with a placeholder credential. The type matches the profile `id`.
   * `export MODEL_CLIENT_ID ... MODEL_CLIENT_SECRET` - Puts the access key's client ID and secret in environment variables for the next command.
   * `provider refresh configure` - Gives the gateway the access key, so that it can fetch and renew the real token.
   * `provider refresh status` - Shows when the token expires and when it is next renewed.

   ```bash
   openshell provider profile import --workspace runai-team-a --file runai-model.yaml
   openshell provider create --workspace runai-team-a --name runai-model --type runai-model \
     --credential RUNAI_MODEL_TOKEN=placeholder
   export MODEL_CLIENT_ID=<access-key-id> MODEL_CLIENT_SECRET=<access-key-secret>
   openshell provider refresh configure --workspace runai-team-a runai-model \
     --credential-key RUNAI_MODEL_TOKEN --strategy oauth2-client-credentials \
     --secret-material-env client_id=MODEL_CLIENT_ID --secret-material-env client_secret=MODEL_CLIENT_SECRET
   openshell provider refresh status --workspace runai-team-a runai-model
   ```

   The status shows the token's state:

   ```text
   PROVIDER     CREDENTIAL_KEY     STRATEGY                   STATUS     EXPIRES_AT           NEXT_REFRESH
   runai-model  RUNAI_MODEL_TOKEN  oauth2_client_credentials  refreshed  2026-10-07 18:14:49  2026-10-07 18:09:49
   ```

   If it shows `oauth_token_endpoint_unavailable`, check that the gateway can reach the token endpoint and trusts its certificate.

   The rest is automatic, as long as the access key stays valid:

   * The gateway renews the token before it expires.
   * Every running sandbox picks up the new token within seconds while it keeps running.

<Note>

**Note**

To change the profile later, export it with `openshell provider profile export`, edit it, and apply it with `openshell provider profile update`.

</Note>

### Option B: A Model from Another Service (API Key)

If your model runs outside NVIDIA Run:ai, for example your organization's model gateway or a managed model service, give the agents its static API key instead. Create `model-endpoint.yaml` for your endpoint, with one entry under `endpoints` for each endpoint the agents may reach:

```yaml
id: model-endpoint
display_name: Model endpoint
description: The organization's model endpoint for agent harnesses
category: inference
inference_capable: true
credentials:
  - name: api_key
    description: Model endpoint API key
    env_vars: [MODEL_API_KEY]
    required: true
    auth_style: bearer              # match your endpoint's authentication scheme
    header_name: authorization
discovery:
  credentials: [api_key]
endpoints:
  - host: <model-host>
    port: 443
    protocol: rest
    access: read-write
    enforcement: enforce
binaries:                                   # agent harness binaries allowed to reach the endpoint
  - /sandbox/tools/bin/claude
  - /sandbox/tools/lib/node_modules/@anthropic-ai/**
```

As the platform administrator, import the profile once for all workspaces, then create a provider in each workspace that runs agents. Providers belong to a workspace, so each project's team can have its own key, for example to track model spend per team:

```bash
openshell profile import --global -f model-endpoint.yaml
export MODEL_API_KEY=<team key>
openshell provider create --workspace runai-team-a --name model-api \
  --type model-endpoint --credential MODEL_API_KEY
```

## Step 6: Running a Bug-Fix Agent

**Role: user.**

This step runs a Claude Code agent on a bug in `invoice-lib`, a small invoicing library: discounted invoices charge VAT on the full price. The task is small on purpose, so that it finishes in minutes and the result is easy to check. The agent runs the tests, sees one fail, fixes one line, and runs the tests again.

### Creating the Sample Code

The agent works on a small Node.js library with one failing test. Create the library on your machine in a folder named `invoice-lib`:

```bash
mkdir -p invoice-lib/src invoice-lib/test
cd invoice-lib

cat > package.json <<'EOF'
{
  "name": "invoice-lib",
  "version": "1.4.2",
  "description": "Invoice totals for the billing service",
  "type": "module",
  "scripts": { "test": "node --test" },
  "license": "Apache-2.0"
}
EOF

cat > src/invoice.js <<'EOF'
// Invoice totals for the billing service.

/** Round a currency amount to cents. */
export function round2(amount) {
  return Math.round((amount + Number.EPSILON) * 100) / 100;
}

/**
 * Compute an invoice's totals.
 * @param {{unitPrice: number, qty: number}[]} lines
 * @param {{discountPct?: number, taxRate?: number}} options
 *   discountPct: order discount in percent (10 = 10%); taxRate: VAT as a fraction (0.17 = 17%)
 */
export function invoiceTotal(lines, { discountPct = 0, taxRate = 0 } = {}) {
  const subtotal = round2(lines.reduce((sum, line) => sum + line.unitPrice * line.qty, 0));
  const discount = round2((subtotal * discountPct) / 100);
  const tax = round2(subtotal * taxRate);
  return { subtotal, discount, tax, total: round2(subtotal - discount + tax) };
}
EOF

cat > test/invoice.test.js <<'EOF'
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { invoiceTotal } from '../src/invoice.js';

test('adds VAT to the subtotal', () => {
  const t = invoiceTotal([{ unitPrice: 100, qty: 2 }], { taxRate: 0.17 });
  assert.deepEqual(t, { subtotal: 200, discount: 0, tax: 34, total: 234 });
});

test('applies an order discount', () => {
  const t = invoiceTotal([{ unitPrice: 50, qty: 1 }, { unitPrice: 25, qty: 2 }], { discountPct: 10 });
  assert.deepEqual(t, { subtotal: 100, discount: 10, tax: 0, total: 90 });
});

test('VAT is charged on the discounted amount (INV-1042 / BILL-2291)', () => {
  const t = invoiceTotal([{ unitPrice: 19.99, qty: 3 }], { discountPct: 10, taxRate: 0.17 });
  assert.deepEqual(t, { subtotal: 59.97, discount: 6.0, tax: 9.17, total: 63.14 });
});
EOF

cat > ISSUE.md <<'EOF'
# BILL-2291: Discounted invoices overcharge VAT

**Reported by:** Customer Support, on behalf of Contoso Retail
**Severity:** High (customers are overcharged; finance must issue credit notes)

Contoso Retail's invoice INV-1042 was overcharged.

| Line | Unit price | Qty |
|---|---|---|
| Thermal paper roll | $19.99 | 3 |

The order has a 10% discount and 17% VAT.

- **Invoice total:** $64.16
- **Expected total:** $63.14 (finance recalculated it by hand)

Finance says VAT must be charged on the amount after the discount. It looks like
it is charged on the full price instead. Every discounted invoice since release
1.4.0 may be affected.
EOF

cat > TASK.md <<'EOF'
Fix the bug in this repository. Work in the current directory.

1. Run `npm test`. One test fails.
2. The bug is on the line in src/invoice.js that computes `tax`: VAT must be charged on the subtotal after the discount, not before it. Change only that line. Do not change any other line or any test.
3. Run `npm test` again. All tests must pass.
4. Write FIX_REPORT.md with two lines: the root cause, and the exact line you changed.
EOF

cd ..
```

The folder contains:

* `src/invoice.js` - The library. It computes VAT on the subtotal before the discount, which is the bug.
* `test/invoice.test.js` - Three tests. The test for the reported invoice fails.
* `ISSUE.md` - The bug report, for your reference. The agent does not read it.
* `TASK.md` - The instructions the agent receives.

The folder is self-contained, so the agent needs only Node.js and the model.

### Creating the Sandbox

As the user, create the agent's sandbox in `runai-team-a` and prepare it to run Claude Code:

1. **Create the policy file.** The sandbox needs the Claude Code CLI, which is installed from npm. Allow npm, and only npm, to reach the registry with a sandbox [policy](https://docs.nvidia.com/openshell/latest/how-it-works/policies/overview). Save the following as `policy-npm-install.yaml`:

```yaml
version: 1
network_policies:
  npm_registry:
    endpoints:
      - host: registry.npmjs.org
        port: 443
        protocol: rest
        access: read-only
        enforcement: enforce
        allow_encoded_slash: true     # scoped packages such as @anthropic-ai/claude-code
    binaries:
      - path: /usr/local/bin/node
```

2. **Create the sandbox** in `runai-team-a` with 2 CPU cores and 4 GiB of memory. The sandbox uses the `runai-model` provider from [Step 5](#step-5-giving-agents-model-access). To use another endpoint, use its provider instead:

```bash
openshell sandbox create --name invoice-bugfix --from docker.io/library/node:22-bookworm-slim \
  --provider runai-model --policy policy-npm-install.yaml --cpu 2 --memory 4Gi --detach
```

3. **Upload the folder** to the sandbox:

```bash
openshell sandbox upload invoice-bugfix ./invoice-lib /sandbox/work
```

4. **Install Claude Code** into the sandbox, under `/sandbox/tools`:

```bash
openshell sandbox exec -n invoice-bugfix --no-tty -- \
  npm install -g --prefix /sandbox/tools @anthropic-ai/claude-code </dev/null
```

5. **Check the sandbox's pods.** They run in the project namespace:

```bash
kubectl -n runai-team-a get pods
```

```text
NAME                                                 READY   STATUS    RESTARTS   AGE
invoice-bugfix                                       1/1     Running   0          2m
os-supervisor-2d36f106-d83b-4aef-91f5-be3c4bcbfb7c   1/1     Running   0          2m
```

### Confirming the Bug

Before the agent runs, confirm the bug yourself. Run the tests in the sandbox. The customer's invoice fails:

```bash
openshell sandbox exec -n invoice-bugfix --no-tty --workdir /sandbox/work/invoice-lib -- npm test </dev/null
```

```text
    total: 63.14
  actual:
    subtotal: 59.97
    discount: 6
    tax: 10.19
    total: 64.16
...
# tests 3
# pass 2
# fail 1
```

### Running the Agent

Run the agent on the task. Claude Code reads the Anthropic-compatible endpoint from `ANTHROPIC_BASE_URL` and sends the provider's placeholder (`RUNAI_MODEL_TOKEN`) as a bearer token. The supervisor swaps in the real token. The OpenShell sandbox is the security boundary, so the command skips Claude Code's own permission prompts:

```bash
openshell sandbox exec -n invoice-bugfix --no-tty --workdir /sandbox/work/invoice-lib -- sh -c '
  ANTHROPIC_BASE_URL=https://<model-host> ANTHROPIC_AUTH_TOKEN="$RUNAI_MODEL_TOKEN" \
  ANTHROPIC_MODEL=<model-name> CLAUDE_CODE_MAX_OUTPUT_TOKENS=4096 CLAUDE_CODE_MAX_CONTEXT_TOKENS=32768 \
  CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
  /sandbox/tools/bin/claude -p "$(cat TASK.md)" --tools "Bash,Read,Edit,Write" --dangerously-skip-permissions' </dev/null
```

Three settings fit the request to the model. For a model it has not seen, Claude Code assumes a 32,000-token output and a 200,000-token context, which a model with a 32,768-token window rejects with a `400` error.

* `CLAUDE_CODE_MAX_OUTPUT_TOKENS` and `CLAUDE_CODE_MAX_CONTEXT_TOKENS` set the real limits. Set the context value to your model's window.
* `--tools` keeps the four tools this task needs, which makes the prompt smaller.

With the model above, the agent finishes in under a minute, and the command prints the agent's closing summary when it does.

### Verifying the Result

Verify the result in the sandbox yourself, because an agent's summary can describe a fix it never made.

1. **Run the tests again.** All three pass:

   ```bash
   openshell sandbox exec -n invoice-bugfix --no-tty --workdir /sandbox/work/invoice-lib -- npm test </dev/null
   ```

   ```text
   # tests 3
   # pass 3
   # fail 0
   ```

2. **Download the result and compare it with the original.** One line changed, in `src/invoice.js`. The tests did not change, and the agent added `FIX_REPORT.md`:

   ```bash
   openshell sandbox download invoice-bugfix /sandbox/work/invoice-lib ./invoice-bugfix-result
   diff -ru invoice-lib invoice-bugfix-result
   ```

   ```text
   Only in invoice-bugfix-result: FIX_REPORT.md
   --- invoice-lib/src/invoice.js
   +++ invoice-bugfix-result/src/invoice.js
   @@ -14,6 +14,6 @@
      const discount = round2((subtotal * discountPct) / 100);
   -  const tax = round2(subtotal * taxRate);
   +  const tax = round2((subtotal - discount) * taxRate);
      return { subtotal, discount, tax, total: round2(subtotal - discount + tax) };
   ```

3. **Read the report:**

   ```bash
   cat invoice-bugfix-result/FIX_REPORT.md
   ```

   ```text
   Tax was calculated on the subtotal before applying the discount.

   src/invoice.js:16: const tax = round2((subtotal - discount) * taxRate);
   ```

The fix changes one line, the failing test now passes, and the existing tests are untouched. The agent's line number in the report can be off by one, which is another reason to check the diff.

Throughout the run, the agent reached the model endpoint (with the token injected by the supervisor) and, for `node`, the npm registry. It held only a placeholder for the token.

<Note>

**Note**

For production, bake the harness into your own image instead of installing it at startup (`FROM node:22-bookworm-slim` + `RUN npm install -g @anthropic-ai/claude-code`). Then drop the npm policy and update the profile's `binaries` paths to match the image.

</Note>

### Working Interactively

To work with Claude Code interactively in the same sandbox, open a shell with `openshell sandbox exec -n invoice-bugfix --tty -- bash`. In that shell, add `/sandbox/tools/bin` to `PATH` and export the same `ANTHROPIC_*` variables as in the command above.

<Note>

**Note**

**Open interactive shells with `exec`.** A sandbox created without a command runs an interactive shell as its main process, and `openshell sandbox connect` attaches to that shell, so exiting it ends the main process and the sandbox goes to `Error`. `openshell sandbox exec --tty -- bash` opens a separate shell that you can exit safely.

</Note>

<Note>

**Note**

**Work through OpenShell sessions.** Sessions opened with the `openshell` CLI carry the provider credentials and the sandbox policy, so the agent reaches its model. A terminal on the agent pod opened from the NVIDIA Run:ai UI or with `kubectl exec` runs outside OpenShell and has no provider credentials.

</Note>

## Step 7: Verifying That Users Are Confined to Their Workspace

**Role: user.**

Access follows workspace membership. Consider a user who is in the IdP group `openshell-users` but is not a member of `runai-team-a`. That user can sign in but sees no workspace, and OpenShell refuses them when they try to create a sandbox there:

```bash
openshell workspace list
openshell sandbox create --workspace runai-team-a --name test --from docker.io/library/node:22-bookworm-slim --detach
```

```text
No workspaces found.
```

```text
Error:   × code: 'The caller does not have permission to execute the specified
  │ operation', message: "not a member of workspace 'runai-team-a'; ask a
  │ platform admin to run: openshell workspace member add --workspace 'runai-
  │ team-a' --subject '<non-member-subject>' --role user"
```

Creating a workspace and adding members require the `openshell-admins` role. To give a non-member access, a platform administrator adds them to the workspace with `openshell workspace member add` (Step 4).

## Step 8: Using Other Agent Harnesses

**Role: user.** The platform administrator adds each harness's binary paths to the provider profile.

Any agent harness runs the same way: a sandbox image that contains the harness (or installs it, as above), a provider that supplies the model key, and a harness configuration that points at the model endpoint and reads the key placeholder from the provider's environment variable. Two settings change per harness:

- **The harness configuration.** Point the harness at your endpoint and its API style. A harness that uses the OpenAI API can use any OpenAI-compatible endpoint, such as [build.nvidia.com](https://build.nvidia.com) or an NVIDIA NIM served on the same NVIDIA Run:ai cluster. For example, OpenCode uses an OpenAI-compatible base URL in `opencode.json`, and Codex uses the OpenAI Responses API in `~/.codex/config.toml`. See the [OpenCode](https://opencode.ai/docs) and [Codex](https://github.com/openai/codex) documentation.
- **The provider profile's `binaries`.** Add the harness's binary paths to the profile from Step 5 (for example `/usr/local/bin/opencode`), so the supervisor injects the key for that harness's requests.

For images, templates and runtimes, see OpenShell [Sandboxes](https://docs.nvidia.com/openshell/latest/how-it-works/sandboxes/overview). For more end-to-end examples, such as running Pi with OpenRouter, see the OpenShell [tutorials](https://docs.nvidia.com/openshell/latest/tutorials). Everything else in this tutorial, including workspaces, membership and NVIDIA Run:ai scheduling, is the same for every harness.

## Step 9: Viewing and Governing Agents in NVIDIA Run:ai

**Role: platform administrator, and NVIDIA Run:ai administrators in the NVIDIA Run:ai UI.**

### Viewing Agents

A platform administrator sees the agents in every workspace:

```bash
openshell sandbox list --all-workspaces
```

```text
WORKSPACE     NAME            CREATED              PHASE
runai-team-a  invoice-bugfix  2026-10-07 10:23:44  Ready
```

In the NVIDIA Run:ai UI, **Workload manager → Workloads** lists the agents' pods as workloads in their project. Project and department admins see their own teams' agents. From there you can:

- **See consumption:** CPU, memory, and (when requested with `openshell sandbox create --gpu`) GPU allocation per agent, rolled up to the project and department.
- **Govern capacity:** agents are scheduled under the project's quota, fairness and over-quota rules, like other NVIDIA Run:ai workloads.
- **Control placement:** use node pools and the project's node-pool priorities to keep agents on CPU nodes and away from training GPUs.

### Watching Enforcement

To watch the sandbox's own enforcement, list the supervisor's policy decisions:

```bash
openshell rule get invoice-bugfix --status pending   # denied destinations the agent tried to reach, proposed as rules
openshell logs invoice-bugfix
```

Approve a proposed rule with `openshell rule approve` only if you want to grant it.

### Stopping and Starting an Agent

To park an agent and free its pods, stop it with OpenShell, which keeps the `/sandbox` workspace. Start it again when it is needed:

```bash
openshell sandbox stop invoice-bugfix
openshell sandbox start invoice-bugfix
```

Stopping sets the Sandbox's `spec.operatingMode` to `Suspended`, and NVIDIA Run:ai shows the agent's pods leave and return.

## Step 10: Cleaning Up the Environment

**Role: user, platform administrator, and system administrator.**

Remove the resources in this order:

1. **Users** delete their own sandboxes:

   ```bash
   openshell sandbox delete invoice-bugfix
   ```

2. The **platform administrator** deletes the workspace after its sandboxes and providers are removed:

   ```bash
   openshell workspace delete runai-team-a
   ```

3. The **system administrator** uninstalls OpenShell as described in the OpenShell [Installation](https://docs.nvidia.com/openshell/latest/about/installation) guide:

   ```bash
   helm uninstall openshell-workspace -n runai-team-a
   kubectl delete -f openshell-gateway-ingress-haproxy.yaml   # or openshell-gateway-ingress-nginx.yaml
   kubectl delete -f openshell-gateway-netpol.yaml
   ```

4. If you set up a model served on NVIDIA Run:ai, the platform administrator deletes the access key or service account the provider used.

## Scope and Limitations

This tutorial was validated with OpenShell 0.1.3 and Agent Sandbox 1.0.5, on Amazon EKS with one NVIDIA A10 GPU. Behavior may differ in other releases, so check the OpenShell release notes for the items below.

Known limitations:

### Access and Identity

* **Two permission systems.**
  * OpenShell creates the Sandbox with the gateway's own Kubernetes identity, so NVIDIA Run:ai's project permissions are not checked at submission.
  * Access is enforced by OpenShell workspace membership, which a platform administrator manages ([Step 4](#step-4-adding-users-to-workspaces)).
  * NVIDIA Run:ai access rules and OpenShell workspace membership are two separate lists, so keep them consistent when a person changes teams.
* **NVIDIA Run:ai shows no submitter for agents.** NVIDIA Run:ai records the gateway, not the user, as the creator. Use the project (and OpenShell's workspace membership and audit) to attribute agents.
* **Roles come from the `groups` claim.** OpenShell reads roles from the token's `groups` claim, so your identity provider must put the user's groups in that claim. A token without it is authenticated but not authorized (`role 'openshell-users' required`).
* **Membership is per user, not per group.**
  * Workspace members are identified by the token's `subject`, one at a time with `openshell workspace member add`.
  * OpenShell does not support adding an IdP group to a workspace, so the groups claim controls only the gateway-wide role (`openshell-users` or `openshell-admins`).
  * For large teams, automate the `member add` and `member remove` commands with your own tooling.
  * A user can run `openshell whoami` to report the subject to add.
* **NVIDIA Run:ai's own identity clients are not used for sign-in.** The OpenShell CLI's browser login redirects to `http://127.0.0.1:<random port>`, which NVIDIA Run:ai's built-in clients do not allow, and their device flow is disabled. Register a dedicated OIDC client in your identity provider instead ([Step 2](#step-2-signing-in-to-openshell)).

### Models and Agents

* **Model served on NVIDIA Run:ai: a private CA was not validated.**
  * The gateway fetches and refreshes the token with an OAuth2 client-credentials grant and verifies the endpoint's TLS certificate.
  * This was validated with a public CA.
  * With a private CA that the gateway does not trust, the refresh fails with `oauth_token_endpoint_unavailable`.
  * At the validated version, the gateway's custom-CA setting is documented for the IdP, and whether it also covers provider token requests was not confirmed.
  * Use certificates from a public CA on the model's control plane until you have validated the private-CA case.
* **Agent quality depends on the model.**
  * The agent's tool calls must be well-formed.
  * In validation, the 4-bit Qwen3-8B completed the [Step 6](#step-6-running-a-bug-fix-agent) task in three of three runs, while a 1.5B model, Qwen2.5-7B-Instruct, and Qwen3-8B with a 24,000-token window did not.
  * Always check the result in the sandbox ([Step 6](#step-6-running-a-bug-fix-agent)), because a weak model can report a fix it did not make.
* **GPU agents.** `openshell sandbox create --gpu N` requests `nvidia.com/gpu` and is accounted against the project's GPU quota. This was not exercised end to end in this tutorial.

### Setup and Operations

* **Workspace setup per project.**
  * OpenShell has no single cluster-wide install for sandbox namespaces. Every project that runs agents needs three things: the `openshell-workspace` chart in its namespace, installed by the system administrator ([Step 1](#step-1-installing-openshell)); a workspace, created by a platform administrator ([Step 3](#step-3-creating-a-workspace-for-each-project)); and a provider ([Step 5](#step-5-giving-agents-model-access)).
  * Plan to include these in your process for onboarding a new NVIDIA Run:ai project, or automate them.
  * The per-namespace chart is deliberate, because it limits where the gateway can read Secrets.
  * The other workspace modes avoid the per-project install, with trade-offs: in `shared` mode every sandbox runs in one namespace, so all teams share one NVIDIA Run:ai project and one quota, and in `managed` mode the gateway creates the namespaces itself. Binding those namespaces to NVIDIA Run:ai projects was not validated in this tutorial.
  * Workspace names are limited to 19 characters.
  * The `openshell-workspace` chart is marked experimental upstream.
* **The supervisor pod has no resource requests.** At the validated version, OpenShell creates the supervisor with no CPU or memory requests (Kubernetes `BestEffort`), so only the agent pod's requests count toward project quota, and the supervisor is the first pod evicted under node memory pressure.
* **CLI usage notes.**
  * At the validated version, `sandbox create --upload` cannot be combined with a command, so upload and exec as separate steps.
  * `sandbox exec` waits for standard input, so append `</dev/null` in scripts.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `not a member of workspace 'runai-<project>'` | The user is not a member of that workspace | A platform administrator or the workspace's admin adds the user ([Step 4](#step-4-adding-users-to-workspaces)) |
| `role 'openshell-users' required` | The user's token has no `groups` claim with `openshell-users` | Add the user to the IdP group; check the OIDC client's `groups` claim mapping |
| `role 'openshell-admins' required` | A non-admin tried a platform operation (workspace create, member add) | Sign in as a user in `openshell-admins` |
| `Invalid parameter: redirect_uri` at sign-in | The OIDC client does not allow the CLI's local redirect | Add `http://127.0.0.1:*` to the client's valid redirect URIs |
| `unauthorized_client` at sign-in | The OIDC client is confidential | Make the client public (no secret), as in [Step 2](#registering-the-oidc-client) |
| Provider refresh shows `oauth_token_endpoint_unavailable` | The gateway cannot connect to the model's token endpoint, typically a TLS certificate it does not trust | Check network access; make the gateway trust the endpoint's CA ([Step 5](#step-5-giving-agents-model-access)) |
| `workspace '...' is not in the operator namespace allowlist` | The namespace has no `runai/queue` label, or the workspace name differs from the namespace name | Name the workspace exactly `runai-<project>` |
| Sandbox stuck before starting; the gateway logs a Secret or Role error in the project namespace | The `openshell-workspace` chart is missing in that namespace | Install it ([Step 1](#installing-the-workspace-chart)) |
| `504 Gateway Time-out` or a hang when connecting to `<gateway-host>`; the ingress controller log shows `upstream timed out ... while connecting to upstream` | A `NetworkPolicy` on the gateway admits only supervisors, not the ingress controller | Add the ingress controller rule to the gateway's `NetworkPolicy` ([Step 1](#restricting-in-cluster-access)) |
| `cached OIDC token has expired` | The refresh token expired or the IdP session ended | `openshell gateway login runai` |
| `openshell sandbox exec` never returns | CLI waits for standard input | Append `</dev/null` |
| `claude: not found`, or the agent cannot reach the model, in a pod terminal | The terminal was opened with `kubectl exec` or the NVIDIA Run:ai UI, which bypasses OpenShell | Use `openshell sandbox exec -n <sandbox> --tty -- bash` |
| Agent pod shows no logs in NVIDIA Run:ai | The agent ran through `openshell sandbox exec`, so its output went to the caller's terminal | Check the agent's output in your terminal, and `openshell logs <sandbox>` for its network activity |
| `API Error: Connection refused`, and the supervisor log shows `DENIED ... claude.exe ... transparent_tcp_policy_denied` | The provider profile's `binaries` lists only `/sandbox/tools/bin/claude`; the supervisor matches the resolved binary under `node_modules` | Add `/sandbox/tools/lib/node_modules/@anthropic-ai/**` to the profile and update it ([Step 5](#step-5-giving-agents-model-access)) |
| `API Error: 400 ... maximum context length is N tokens ... you requested 32000 output tokens` | Claude Code does not know the model's limits | Set `CLAUDE_CODE_MAX_OUTPUT_TOKENS` and `CLAUDE_CODE_MAX_CONTEXT_TOKENS` ([Step 6](#running-the-agent)) |
| `Autocompact is thrashing: the context refilled to the limit` and the agent never finishes | The model's context window is too small for Claude Code's prompt plus the conversation (24,000 tokens was too small) | Serve the model with a window of about 32,000 tokens ([Step 5](#step-5-giving-agents-model-access)) and set `CLAUDE_CODE_MAX_CONTEXT_TOKENS` to it ([Step 6](#running-the-agent)) |
| `API Error: 400 "auto" tool choice requires --enable-auto-tool-choice and --tool-call-parser` | The vLLM server was started without tool calling | Add `--enable-auto-tool-choice --tool-call-parser hermes` (Qwen) and redeploy the model ([Step 5](#step-5-giving-agents-model-access)) |
| The agent says it fixed the bug, but the tests still fail and the code is unchanged | The model wrote its edit as text instead of a tool call, or invented the result | Use a stronger model; always run the checks in [Step 6](#verifying-the-result) |
| `OIDC token refresh failed ... invalid_grant: Token is not active` | The IdP session expired | `openshell gateway login runai`; lengthen the IdP's session lifetimes if agents run long |
| `workspace member add` succeeds, but the user sees no workspaces | The member was added by email, but the token's subject is an opaque ID | Add the `Subject` shown by `openshell whoami` ([Step 2](#checking-the-users-identity-and-roles)) |
| Sandbox in `Error`, `connect` fails with `canonical main process already finished` | An earlier `connect` session was exited, which ended the sandbox's main shell | `openshell sandbox stop <name> && openshell sandbox start <name>`; use `exec --tty -- bash` for interactive work |
