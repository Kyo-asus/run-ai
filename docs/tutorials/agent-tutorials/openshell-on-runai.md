# Running AI Agents with NVIDIA OpenShell on NVIDIA Run:ai

This tutorial demonstrates how to run autonomous AI agents in NVIDIA OpenShell sandboxes on an NVIDIA Run:ai-managed Kubernetes cluster, with an OpenShell workspace for an [NVIDIA Run:ai project](../../platform-management/aiinitiatives/organization/projects.md). Agents are scheduled under their NVIDIA Run:ai project's [quota](../../platform-management/runai-scheduler/scheduling/concepts-and-principles.md#quota) like any other workload. You can adapt this workflow for other teams, NVIDIA Run:ai projects, model endpoints, and agent harnesses.

In this tutorial, you will learn how to:

* Install the OpenShell gateway on an NVIDIA Run:ai cluster in `operator` mode
* Connect the gateway to your identity provider and sign users in
* Create an OpenShell workspace for an NVIDIA Run:ai project and add users to it
* Give agents model access through providers that keep the credential in the gateway, including a model served on NVIDIA Run:ai behind OIDC authentication
* Run an agent with Claude Code that pushes to GitHub
* Monitor agents as NVIDIA Run:ai workloads

## How OpenShell and NVIDIA Run:ai Work Together

OpenShell and NVIDIA Run:ai each handle a different part of running AI agents. OpenShell is the agent platform. Users sign in to it, and it runs each agent in a sandbox, a protected environment that limits what the agent can reach. NVIDIA Run:ai is the orchestration and workload monitoring layer. It decides where the agents' pods run on the cluster, and it shows them as workloads that administrators can monitor. Each OpenShell workspace maps to an NVIDIA Run:ai project namespace, which lets NVIDIA Run:ai schedule and monitor the sandbox workloads under the quota of that project.

Each part of the setup is responsible for the following:

- **OpenShell** is the platform where users manage agents. It provides:
  - **Sign-in with your identity provider.** The gateway accepts any OIDC identity provider (IdP) that puts a `groups` claim in its tokens. Users do not need NVIDIA Run:ai accounts. The token's subject, an identifier chosen by the IdP, is the identity OpenShell records and checks.
  - **OpenShell workspaces and permissions.** A platform administrator decides who can use which OpenShell workspace. OpenShell workspace membership is the access control: a user can create and use sandboxes only in the OpenShell workspaces they are members of.
  - **Sandboxes.** Each agent runs as an agent pod and an OpenShell supervisor pod in the NVIDIA Run:ai project namespace. The supervisor enforces the sandbox policy (which binaries may reach which endpoints) and injects real credentials into approved requests, so the agent holds only a placeholder.
  - **The OpenShell gateway** is platform infrastructure. It runs outside the NVIDIA Run:ai projects, so NVIDIA Run:ai project quota covers the agents.
- **NVIDIA Run:ai** schedules the agents' workloads and monitors them. It is responsible for:
  - **Scheduling.** NVIDIA Run:ai schedules every pod in a project namespace under the NVIDIA Run:ai project's [quota](../../platform-management/runai-scheduler/scheduling/concepts-and-principles.md#quota), [fairness](../../platform-management/runai-scheduler/scheduling/concepts-and-principles.md#fairness-fair-resource-distribution), and [node pools](../../platform-management/aiinitiatives/resources/node-pools.md), exactly like any other workload.
  - **Workload monitoring.** The agents' pods appear as workloads in NVIDIA Run:ai, where administrators can monitor them.

<img src="../../assets/openshell-on-runai-architecture.svg" alt="OpenShell on NVIDIA Run:ai: the user's openshell CLI signs in through the identity provider and calls the OpenShell gateway over gRPC with an OIDC token through the ingress. The gateway runs in the openshell namespace and checks the token's groups claim. OpenShell workspace runai-team-a maps to the project namespace runai-team-a, the namespace of NVIDIA Run:ai project team-a, where the agent pod and supervisor pod run, all scheduled and monitored by the NVIDIA Run:ai scheduler. The supervisor pod sends the agent's model requests to a model endpoint, which is an inference workload served on NVIDIA Run:ai. A Platform Admin creates the OpenShell workspace and adds each user to it." />

## Who Does What

This tutorial involves four roles. A single person can hold more than one role. The following table describes each role:

| Role | Who they are | Responsible for | Works with |
|---|---|---|---|
| **Cluster administrator** | Cluster and infrastructure administrator. The identity provider (IdP) administrator joins for the IdP steps. | Installs and configures the OpenShell gateway: Helm charts, IdP settings, network policy, ingress and certificates | `kubectl`, Helm, the IdP admin console |
| **Platform Admin** | A member of the IdP group `openshell-admins` | Runs OpenShell for the organization: creates an OpenShell workspace for the NVIDIA Run:ai project, adds users to it, and creates the providers that supply model credentials. Can delegate adding users to Workspace Admins. | The `openshell` CLI. |
| **Workspace Admin** | A user that a Platform Admin gave the `admin` role in an OpenShell workspace | Adds users to their own OpenShell workspace and manages the providers of their own OpenShell workspace | The `openshell` CLI |
| **Workspace User** | A member of the IdP group `openshell-users` and of the team's OpenShell workspace | Runs agents in their team's OpenShell workspace | The `openshell` CLI |

## Prerequisites

Before you start, make sure the following requirements are met:

### OpenShell Prerequisites

Before you install OpenShell in [Step 1](#step-1-installing-openshell), make sure the following requirements are met. Each one is described in the OpenShell documentation:

- **Cluster and tools:** Kubernetes 1.29 or later, Helm 3.x, `kubectl`, the CNI, and the Agent Sandbox controller. Refer to the [prerequisites](https://docs.nvidia.com/openshell/kubernetes/setup#prerequisites) in the Kubernetes setup guide and the [Support Matrix](https://docs.nvidia.com/openshell/latest/about/support-matrix#software-prerequisites).
- **Installer permissions:** The cluster administrator needs cluster-scoped permissions. Refer to the [RBAC](https://docs.nvidia.com/openshell/kubernetes/setup#rbac) section of the setup guide.
- **Identity provider:** OpenShell needs an OIDC identity provider for sign-in. Refer to [Gateway Authentication](https://docs.nvidia.com/openshell/latest/how-it-works/gateways/authentication).

### NVIDIA Run:ai Prerequisites

Make sure the following are set up in NVIDIA Run:ai:

- **A project.** An NVIDIA Run:ai [project](../../platform-management/aiinitiatives/organization/projects.md) for the team that runs agents, with the quota you want to limit its agents to. This tutorial uses the NVIDIA Run:ai project `team-a` and the user `user@example.com`. Users do not need NVIDIA Run:ai accounts. Agents run in the NVIDIA Run:ai project under its quota. In [Step 3](#step-3-creating-an-openshell-workspace-for-the-nvidia-runai-project), the Platform Admin creates an OpenShell workspace that matches the NVIDIA Run:ai project, and in [Step 4](#step-4-adding-users-to-the-openshell-workspace) adds the team's users to it.
- **A service account.** A [service account](../../platform-management/authentication/service-accounts.md) has a client ID and a client secret. In [Step 5](#step-5-giving-agents-model-access), the OpenShell gateway uses them to get access tokens for the model endpoint.
- **The sandbox workload type.** An administrator registers the OpenShell sandbox as a workload type, so that NVIDIA Run:ai can schedule and monitor the sandbox workloads. Use the [sandbox workload type](https://github.com/dsx-ai-factory/workload-map/blob/main/docs/catalog/agents-x-k8s-io-sandbox-v1beta1.yaml) from the workload-map repository, and follow [Registering New Workload Types](../../workloads-in-nvidia-run-ai/workload-types/extending-workload-support/README.md#registering-new-workload-types).
- **A language model for the agents to call.** In [Step 5](#step-5-giving-agents-model-access), the Platform Admin connects the agents to the model, and the agents use it in [Step 6](#step-6-running-an-agent-that-pushes-to-github). Deploy the model on NVIDIA Run:ai as an inference workload, using the UI, the API or the CLI, as described in [Deploy Inference Workloads with vLLM](../../workloads-in-nvidia-run-ai/using-inference/hugging-face-inference.md) (Hugging Face models) or [Deploy Inference Workloads with NVIDIA NIM](../../workloads-in-nvidia-run-ai/using-inference/nim-inference.md).

  Claude Code needs three things from the model server:

  - **The Anthropic API format.** Claude Code sends requests in Anthropic's Messages API format (`/v1/messages`). vLLM supports it, so no argument is needed.
  - **Tool calling.** The model must be able to ask Claude Code to run commands and edit files, for example to run the tests. The server must have this switched on.
  - **Enough context.** The context window is how much text the model can hold at once. Claude Code sends large prompts, so the window needs to be about 32,000 tokens. The model must also be strong enough to use tools correctly.

  This tutorial uses `Qwen/Qwen3-8B-AWQ`, a 4-bit model, on one NVIDIA A10 (24 GB), with these vLLM arguments:

  ```text
  --model Qwen/Qwen3-8B-AWQ --max-model-len 32768 --gpu-memory-utilization 0.92 --enable-auto-tool-choice --tool-call-parser hermes --default-chat-template-kwargs '{"enable_thinking": false}'
  ```

  - `--max-model-len 32768` - Sets the context window to about 32,000 tokens.
  - `--enable-auto-tool-choice --tool-call-parser hermes` - Enables tool calling for a Qwen model.
  - `--gpu-memory-utilization 0.92` - Lets vLLM use 92% of the GPU memory.
  - `--default-chat-template-kwargs '{"enable_thinking": false}'` - Turns off the model's thinking mode.

  For a vLLM server, set these arguments in the **Command & Arguments** field when you deploy the workload. For the arguments for another model, refer to the [vLLM documentation](https://docs.vllm.ai).

### Values to Replace

Throughout this tutorial, replace:

| Placeholder | Meaning | Example |
|---|---|---|
| `<idp-issuer-url>` | Issuer URL of your OIDC identity provider | `https://idp.example.com/realms/corp` |
| `<runai-url>` | NVIDIA Run:ai control-plane hostname | `runai.example.com` |
| `<gateway-host>` | Hostname users connect to for the OpenShell gateway, under NVIDIA Run:ai's wildcard domain | `openshell.runai.example.com` |
| `<project>` | The NVIDIA Run:ai project that runs agents | `team-a` |
| `runai-<project>` | An NVIDIA Run:ai project's Kubernetes namespace, and its OpenShell workspace | `runai-team-a` |
| `<model-host>` | Hostname of the endpoint of your model served on NVIDIA Run:ai | `qwen3-8b.runai-inference.example.com` |
| `<model-name>` | Model the agent uses, as named by your endpoint | `Qwen/Qwen3-8B-AWQ` |

## Step 1: Installing OpenShell

<Note>

**Note**

**Role:** cluster administrator.

</Note>

Install OpenShell on the cluster and expose the gateway to users:

1. **Install OpenShell.** Follow the OpenShell [Kubernetes setup](https://docs.nvidia.com/openshell/kubernetes/setup) guide. This tutorial uses OpenShell **0.1.4** and Agent Sandbox **1.0.7**. When you configure the gateway values, make sure to set the following:

   * `workspaceMode: operator` - Runs each sandbox in an existing namespace. For NVIDIA Run:ai, the OpenShell workspace `runai-<project>` runs in the namespace of the NVIDIA Run:ai project with that name, under the project's quota.
   * `workspaceResources.enabled: false` - Splits the gateway from the per-namespace resources, as described in [Split gateway and workspace releases](https://docs.nvidia.com/openshell/kubernetes/setup#split-gateway-and-workspace-releases). The `openshell-workspace` chart creates those resources instead, as described in the next step.

   ```yaml
   workspaceResources:
     enabled: false
   server:
     drivers:
       kubernetes:
         workspaceMode: operator
   ```

2. **Install the workspace chart in each NVIDIA Run:ai project namespace.** In operator mode, install the `openshell-workspace` chart in the namespace of every NVIDIA Run:ai project that runs agents, as described under [Split gateway and workspace releases](https://docs.nvidia.com/openshell/kubernetes/setup#split-gateway-and-workspace-releases) in the setup guide. Any other NVIDIA Run:ai project that runs agents needs the same install when you onboard it. For example, to install the chart in the namespace of NVIDIA Run:ai project `team-a`:

   ```bash
   helm upgrade --install openshell-workspace oci://ghcr.io/nvidia/openshell/openshell-workspace \
     --version 0.1.4 --namespace runai-team-a
   ```

3. **Verify the gateway.** Check the gateway logs to confirm that it accepts tokens from your IdP and has found the NVIDIA Run:ai project namespaces:

   ```bash
   kubectl -n openshell logs openshell-0 | grep -E "OIDC|operator namespace"
   ```

4. **Create the ingress.** Expose the gateway through your cluster's ingress controller, so that users connect to a hostname with a certificate from their own machines. This uses the ingress controller, the wildcard DNS record, and the wildcard certificate that your NVIDIA Run:ai installation already has, plus a hostname for the gateway, `<gateway-host>`, which you choose under the same wildcard domain, for example `openshell.<runai-domain>`.

   NVIDIA Run:ai recommends the HAProxy Kubernetes Ingress Controller. Clusters that were upgraded from an earlier version can still run NGINX. In both cases, the gateway speaks gRPC, so the ingress must use HTTP/2 to reach the gateway pod, and long timeouts keep streaming commands (exec, upload, and download) open. Create the ingress for your controller:

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

<Note>

**Note**

OpenShell also documents exposing the gateway with the Gateway API. Refer to [Ingress](https://docs.nvidia.com/openshell/latest/kubernetes/ingress) in the OpenShell documentation.

</Note>

5. **Apply the ingress.** Apply the file for your controller:

   ```bash
   kubectl apply -f openshell-gateway-ingress-haproxy.yaml   # or openshell-gateway-ingress-nginx.yaml
   ```

6. **Share the gateway address.** Give the Platform Admins and Workspace Users the gateway address, `<gateway-host>`. They use it to sign in in [Step 2](#step-2-signing-in-to-openshell).

<Note>

**Note**

To expose the gateway through the Kubernetes Gateway API instead, follow the OpenShell [Ingress](https://docs.nvidia.com/openshell/latest/kubernetes/ingress) guide.

</Note>

## Step 2: Signing In to OpenShell

<Note>

**Note**

**Role:** the cluster administrator registers the OIDC client. Then every Platform Admin and Workspace User installs the CLI and signs in.

</Note>

Platform Admins and Workspace Users sign in to OpenShell through your identity provider (IdP). Workspace Users are the people who run the agents. Follow these steps:

1. **Register an OIDC client in your IdP.** As the cluster administrator, register a client for the OpenShell CLI, once. For the settings, refer to [Gateway Authentication](https://docs.nvidia.com/openshell/latest/how-it-works/gateways/authentication). For how IdP roles map to OpenShell roles, refer to [Access Control](https://docs.nvidia.com/openshell/latest/kubernetes/access-control).
2. **Install the OpenShell CLI.** Each Platform Admin and Workspace User installs the CLI, as described in [Installation](https://docs.nvidia.com/openshell/latest/about/installation).
3. **Register the gateway and sign in.** Each Platform Admin and Workspace User runs `openshell gateway add` with the gateway address from [Step 1](#step-1-installing-openshell), `https://<gateway-host>`. The command opens a browser, where you sign in through your IdP. For the options of the command, refer to [Gateway Authentication](https://docs.nvidia.com/openshell/latest/how-it-works/gateways/authentication).
4. **Check your identity.** Run `openshell whoami` to see the identity and roles that OpenShell sees for you.

## Step 3: Creating an OpenShell Workspace for the NVIDIA Run:ai Project

<Note>

**Note**

**Role:** Platform Admin.

</Note>

This tutorial creates one OpenShell workspace, for the [NVIDIA Run:ai project](../../platform-management/aiinitiatives/organization/projects.md) `team-a`. To give another NVIDIA Run:ai project agents, repeat this step, and the chart install in [Step 1](#step-1-installing-openshell), for that NVIDIA Run:ai project.

As a **Platform Admin** (a user in the `openshell-admins` group), create the [OpenShell workspace](https://docs.nvidia.com/openshell/latest/how-it-works/workspaces) for the NVIDIA Run:ai project. In operator mode, the OpenShell workspace name must equal the NVIDIA Run:ai project namespace name.

For the NVIDIA Run:ai project, the order is:

1. The NVIDIA Run:ai project exists.
2. The cluster administrator installs the `openshell-workspace` chart in the NVIDIA Run:ai project namespace ([Step 1](#step-1-installing-openshell)).
3. You create the OpenShell workspace:

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

## Step 4: Adding Users to the OpenShell Workspace

<Note>

**Note**

**Role:** Platform Admin. A Workspace Admin can also add users to their own OpenShell workspace.

</Note>

An OpenShell workspace admits only its members. A **Platform Admin** adds each user to the OpenShell workspace of the NVIDIA Run:ai project they work in. For the roles and for managing members, refer to the OpenShell [Workspaces](https://docs.nvidia.com/openshell/latest/how-it-works/workspaces) documentation.

A member is identified by the `subject` of their token, the value the user sees in `openshell whoami` ([Step 2](#step-2-signing-in-to-openshell)). In this tutorial, `<user-subject>` stands for that value for the user:

```bash
openshell workspace member add --workspace runai-team-a --subject <user-subject> --role user
openshell workspace member list --workspace runai-team-a
```

The user now sees only their team's OpenShell workspace. Set it as the default so that commands use it by default:

```bash
export OPENSHELL_WORKSPACE=runai-team-a
```

## Step 5: Giving Agents Model Access

<Note>

**Note**

**Role:** Platform Admin.

</Note>

A model served on NVIDIA Run:ai accepts only requests with a valid NVIDIA Run:ai token. A token lasts about 30 minutes, so the gateway uses the client ID and client secret of an NVIDIA Run:ai [service account](../../platform-management/authentication/service-accounts.md) to fetch and renew it, and never gives them to the agent. For how providers work, refer to the OpenShell [Providers](https://docs.nvidia.com/openshell/latest/how-it-works/providers/overview) documentation.

The model and the service account are already set up, as described in [Prerequisites](#nvidia-runai-prerequisites). In this step, you create the provider profile and the provider in OpenShell.

1. **In OpenShell, create the provider profile** in the OpenShell workspace that uses the model. Take `<endpoint-host>` from the endpoint URL in the workload's **Connections** column in the NVIDIA Run:ai UI under [**Workload manager → Workloads**](../../workloads-in-nvidia-run-ai/workloads.md#connections-associated-with-the-workload).

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

2. **In OpenShell, create the provider and give it the service account.** Use the client ID and client secret of the NVIDIA Run:ai service account. Use one per team. The secret stays in the gateway.

   ```bash
   openshell provider profile import --workspace runai-team-a --file runai-model.yaml
   openshell provider create --workspace runai-team-a --name runai-model --type runai-model \
     --credential RUNAI_MODEL_TOKEN=placeholder
   export MODEL_CLIENT_ID=<client-id> MODEL_CLIENT_SECRET=<client-secret>
   openshell provider refresh configure --workspace runai-team-a runai-model \
     --credential-key RUNAI_MODEL_TOKEN --strategy oauth2-client-credentials \
     --secret-material-env client_id=MODEL_CLIENT_ID --secret-material-env client_secret=MODEL_CLIENT_SECRET
   openshell provider refresh status --workspace runai-team-a runai-model
   ```

   If the status shows `oauth_token_endpoint_unavailable`, check that the gateway can reach the token endpoint and trusts its certificate.

<Note>

**Note**

To use another agent harness, add its binary paths to the `binaries` list of the provider profile, so that the supervisor adds the token for that harness's requests. For provider profiles, refer to the OpenShell [Profiles](https://docs.nvidia.com/openshell/latest/how-it-works/providers/profiles) documentation.

</Note>

## Step 6: Running an Agent That Pushes to GitHub

<Note>

**Note**

**Role:** Workspace User.

</Note>

This step runs a Claude Code agent in an OpenShell sandbox that pushes code to a GitHub repository, using the model from [Step 5](#step-5-giving-agents-model-access). Follow the OpenShell [GitHub push access](https://docs.nvidia.com/openshell/latest/tutorials/github-push-access) tutorial, with these changes for NVIDIA Run:ai:

1. **Target the OpenShell workspace.** Run every command in the OpenShell workspace that is mapped to the NVIDIA Run:ai project, `runai-team-a`. Use the `OPENSHELL_WORKSPACE` setting from [Step 4](#step-4-adding-users-to-the-openshell-workspace), or add `--workspace runai-team-a` to each command. For how this works, refer to [Target a Workspace](https://docs.nvidia.com/openshell/latest/how-it-works/workspaces#target-a-workspace). The sandbox then runs in the NVIDIA Run:ai project namespace, under the project's quota.

2. **Use the model from Step 5.** Do not create the Anthropic provider from the OpenShell tutorial. Attach the `runai-model` provider to the sandbox instead, together with the GitHub provider:

   ```bash
   --provider runai-model --provider my-github
   ```

3. **Point Claude Code at the model.** Start Claude Code with these environment variables. Claude Code reads the Anthropic-compatible endpoint from `ANTHROPIC_BASE_URL` and sends the provider's placeholder (`RUNAI_MODEL_TOKEN`) as a bearer token. The supervisor swaps in the real token:

   ```bash
   ANTHROPIC_BASE_URL=https://<model-host>
   ANTHROPIC_AUTH_TOKEN="$RUNAI_MODEL_TOKEN"
   ANTHROPIC_MODEL=<model-name>
   CLAUDE_CODE_MAX_OUTPUT_TOKENS=4096
   CLAUDE_CODE_MAX_CONTEXT_TOKENS=32768
   CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
   ```

   `CLAUDE_CODE_MAX_OUTPUT_TOKENS` and `CLAUDE_CODE_MAX_CONTEXT_TOKENS` fit the request to the model. For a model it has not seen, Claude Code assumes a 32,000-token output and a 200,000-token context, which a model with a 32,768-token window rejects with a `400` error. Set the context value to your model's window.

4. **Check the sandbox in NVIDIA Run:ai.** The sandbox appears as a workload in NVIDIA Run:ai, as described in [Step 7](#step-7-monitoring-agents).

## Step 7: Monitoring Agents

<Note>

**Note**

**Role:** Platform Admin, and NVIDIA Run:ai administrators in the NVIDIA Run:ai UI.

</Note>

### Viewing Agents in OpenShell

A Platform Admin can list the agents in every OpenShell workspace. For how, refer to the OpenShell [Workspaces](https://docs.nvidia.com/openshell/latest/how-it-works/workspaces#target-a-workspace) documentation.

### Viewing Agents in NVIDIA Run:ai

In the NVIDIA Run:ai UI, [**Workload manager → Workloads**](../../workloads-in-nvidia-run-ai/workloads.md) lists the agents' pods as workloads in their NVIDIA Run:ai project. [Project](../../platform-management/aiinitiatives/organization/projects.md) and [department](../../platform-management/aiinitiatives/organization/departments.md) admins see their own teams' agents. From there you can:

- **See consumption:** CPU, memory, and (when requested with `openshell sandbox create --gpu`) GPU allocation per agent, rolled up to the NVIDIA Run:ai project and department.
- **Manage capacity:** agents are scheduled under the NVIDIA Run:ai project's [quota](../../platform-management/runai-scheduler/scheduling/concepts-and-principles.md#quota), [fairness](../../platform-management/runai-scheduler/scheduling/concepts-and-principles.md#fairness-fair-resource-distribution) and [over-quota](../../platform-management/runai-scheduler/scheduling/concepts-and-principles.md#over-quota) rules, like other NVIDIA Run:ai workloads.
- **Control placement:** use [node pools](../../platform-management/aiinitiatives/resources/node-pools.md) and the NVIDIA Run:ai project's node-pool priorities to keep agents on CPU nodes and away from training GPUs.

### Watching Enforcement in OpenShell

OpenShell, not NVIDIA Run:ai, enforces the sandbox policy. To watch the sandbox's own enforcement, refer to the OpenShell [Manage Sandboxes](https://docs.nvidia.com/openshell/latest/how-it-works/sandboxes/overview) documentation.

### Stopping and Starting an Agent in OpenShell

To park an agent and free its pods, stop it with OpenShell, which keeps the sandbox and its persistent workspace. Start it again when it is needed. For how, refer to the OpenShell [Manage Sandboxes](https://docs.nvidia.com/openshell/latest/how-it-works/sandboxes/overview) documentation.

Stopping sets the Sandbox's `spec.operatingMode` to `Suspended`, and NVIDIA Run:ai shows the agent's pods leave and return.

## Step 8: Cleaning Up the Environment

<Note>

**Note**

**Role:** Workspace User, Platform Admin, and cluster administrator.

</Note>

Remove the resources in this order:

1. **Users** delete their own sandboxes:

   ```bash
   openshell sandbox delete invoice-bugfix
   ```

2. The **Platform Admin** deletes the OpenShell workspace after its sandboxes and providers are removed:

   ```bash
   openshell workspace delete runai-team-a
   ```

3. The **cluster administrator** uninstalls OpenShell as described in the OpenShell [Installation](https://docs.nvidia.com/openshell/latest/about/installation) guide:

   ```bash
   helm uninstall openshell-workspace -n runai-team-a
   kubectl delete -f openshell-gateway-ingress-haproxy.yaml   # or openshell-gateway-ingress-nginx.yaml
   kubectl delete -f openshell-gateway-netpol.yaml
   ```

4. If you set up a model served on NVIDIA Run:ai, the Platform Admin deletes the [service account](../../platform-management/authentication/service-accounts.md) the provider used.

## Scope and Limitations

This tutorial was validated with OpenShell 0.1.4 and Agent Sandbox 1.0.7, on Amazon EKS with one NVIDIA A10 GPU. Behavior may differ in other releases, so check the OpenShell release notes for the items below.

### Access and Identity

* **Two permission systems.**
  * OpenShell creates the Sandbox with the gateway's own Kubernetes identity, so NVIDIA Run:ai's project permissions are not checked at submission.
  * Access is enforced by OpenShell workspace membership, which a Platform Admin manages ([Step 4](#step-4-adding-users-to-the-openshell-workspace)).
  * NVIDIA Run:ai access rules and OpenShell workspace membership are two separate lists, so keep them consistent when a person changes teams.
* **NVIDIA Run:ai shows no submitter for agents.** NVIDIA Run:ai records the gateway, not the user, as the creator. Use the NVIDIA Run:ai project (and OpenShell workspace membership and audit) to attribute agents.
* **Roles come from the `groups` claim.** OpenShell reads roles from the token's `groups` claim, so your identity provider must put the user's groups in that claim. A token without it is authenticated but not authorized (`role 'openshell-users' required`).
* **Membership is per user, not per group.**
  * OpenShell workspace members are identified by the token's `subject`, one at a time with `openshell workspace member add`.
  * OpenShell does not support adding an IdP group to an OpenShell workspace, so the groups claim controls only the gateway-wide role (`openshell-users` or `openshell-admins`).
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
  * In validation, the 4-bit Qwen3-8B completed the [Step 6](#step-6-running-an-agent-that-pushes-to-github) task in three of three runs, while a 1.5B model, Qwen2.5-7B-Instruct, and Qwen3-8B with a 24,000-token window did not.
  * Always check the result in the sandbox ([Step 6](#step-6-running-an-agent-that-pushes-to-github)), because a weak model can report a fix it did not make.
* **GPU agents.** `openshell sandbox create --gpu N` requests `nvidia.com/gpu` and is accounted against the NVIDIA Run:ai project's GPU [quota](../../platform-management/runai-scheduler/scheduling/concepts-and-principles.md#quota). This was not exercised end to end in this tutorial.

### Setup and Operations

* **OpenShell workspace setup for the NVIDIA Run:ai project.**
  * OpenShell has no single cluster-wide install for sandbox namespaces. The NVIDIA Run:ai project that runs agents needs three things: the `openshell-workspace` chart in its namespace, installed by the cluster administrator ([Step 1](#step-1-installing-openshell)); an OpenShell workspace, created by a Platform Admin ([Step 3](#step-3-creating-an-openshell-workspace-for-the-nvidia-runai-project)); and a provider ([Step 5](#step-5-giving-agents-model-access)).
  * Plan to include these in your process for onboarding a new NVIDIA Run:ai project, or automate them.
  * The per-namespace chart is deliberate, because it limits where the gateway can read Secrets.
  * The other OpenShell workspace modes avoid the install in each NVIDIA Run:ai project namespace, with trade-offs: in `shared` mode every sandbox runs in one namespace, so all teams share one NVIDIA Run:ai project and one quota, and in `managed` mode the gateway creates the namespaces itself. Binding those namespaces to NVIDIA Run:ai projects was not validated in this tutorial.
  * OpenShell workspace names are limited to 19 characters.
  * The `openshell-workspace` chart is marked experimental upstream.
* **The supervisor pod has no resource requests.** At the validated version, OpenShell creates the supervisor with no CPU or memory requests (Kubernetes `BestEffort`), so only the agent pod's requests count toward NVIDIA Run:ai project quota, and the supervisor is the first pod evicted under node memory pressure.
* **CLI usage notes.**
  * At the validated version, `sandbox create --upload` cannot be combined with a command, so upload and exec as separate steps.
  * `sandbox exec` waits for standard input, so append `</dev/null` in scripts.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `not a member of workspace 'runai-<project>'` | The user is not a member of that OpenShell workspace | A Platform Admin or a Workspace Admin of that OpenShell workspace adds the user ([Step 4](#step-4-adding-users-to-the-openshell-workspace)) |
| `role 'openshell-users' required` | The user's token has no `groups` claim with `openshell-users` | Add the user to the IdP group; check the OIDC client's `groups` claim mapping |
| `role 'openshell-admins' required` | A non-admin tried a platform operation (workspace create, member add) | Sign in as a user in `openshell-admins` |
| `Invalid parameter: redirect_uri` at sign-in | The OIDC client does not allow the CLI's local redirect | Add `http://127.0.0.1:*` to the client's valid redirect URIs |
| `unauthorized_client` at sign-in | The OIDC client is confidential | Make the client public (no secret), as in [Step 2](#step-2-signing-in-to-openshell) |
| Provider refresh shows `oauth_token_endpoint_unavailable` | The gateway cannot connect to the model's token endpoint, typically a TLS certificate it does not trust | Check network access; make the gateway trust the endpoint's CA ([Step 5](#step-5-giving-agents-model-access)) |
| `workspace '...' is not in the operator namespace allowlist` | The namespace has no `runai/queue` label, or the OpenShell workspace name differs from the namespace name | Name the OpenShell workspace exactly `runai-<project>` |
| Sandbox stuck before starting; the gateway logs a Secret or Role error in the NVIDIA Run:ai project namespace | The `openshell-workspace` chart is missing in that namespace | Install it ([Step 1](#step-1-installing-openshell)) |
| `504 Gateway Time-out` or a hang when connecting to `<gateway-host>`; the ingress controller log shows `upstream timed out ... while connecting to upstream` | A `NetworkPolicy` on the gateway admits only supervisors, not the ingress controller | Add a rule that admits the ingress controller to the gateway's `NetworkPolicy` |
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
| `workspace member add` succeeds, but the user sees no OpenShell workspaces | The member was added by email, but the token's subject is an opaque ID | Add the `Subject` shown by `openshell whoami` ([Step 2](#step-2-signing-in-to-openshell)) |
| Sandbox in `Error`, `connect` fails with `canonical main process already finished` | An earlier `connect` session was exited, which ended the sandbox's main shell | `openshell sandbox stop <name> && openshell sandbox start <name>`; use `exec --tty -- bash` for interactive work |
