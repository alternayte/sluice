#!/usr/bin/env bash
# Runs the [K] scenarios: a kind cluster with the images, Postgres, Vault and the Helm
# chart, then the Go tests with the k8s tag. The cluster is deleted at the end unless
# SLUICE_KIND_KEEP=1. `just build-images` must run first.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
cluster="${SLUICE_KIND_CLUSTER:-sluice-e2e}"
ctx="kind-$cluster"
ns=sluice
junit="$root/build/reports/junit"
mkdir -p "$junit"

for img in sluice:dev sluice-uv:dev; do
  docker image inspect "$img" >/dev/null 2>&1 || { echo "image $img is missing: run just build-images" >&2; exit 1; }
done

kind create cluster --name "$cluster" --wait 180s

# At exit, save the logs of all server pods (current and previous containers) as test
# evidence, then delete the cluster unless SLUICE_KIND_KEEP=1.
finish() {
  local log="$root/build/reports/k8s-server.log"
  {
    kubectl --context "$ctx" -n sluice logs -l app.kubernetes.io/instance=sluice --all-containers --prefix --tail=-1 || true
    echo "==== previous containers"
    kubectl --context "$ctx" -n sluice logs -l app.kubernetes.io/instance=sluice --all-containers --prefix --tail=-1 --previous || true
  } > "$log" 2>&1
  echo "server logs: $log"
  if [ "${SLUICE_KIND_KEEP:-}" != "1" ]; then
    kind delete cluster --name "$cluster"
  fi
}
trap finish EXIT
kind load docker-image sluice:dev sluice-uv:dev --name "$cluster"

k() { kubectl --context "$ctx" -n "$ns" "$@"; }
kubectl --context "$ctx" create namespace "$ns"
k apply -f tests/k8s/postgres.yaml -f tests/k8s/vault.yaml
k rollout status deploy/postgres --timeout=180s
k rollout status deploy/vault --timeout=180s

# Vault Kubernetes auth for the server service account "sluice" (SCN-SEC-012).
k exec deploy/vault -- sh -ec '
export VAULT_ADDR=http://127.0.0.1:8200 VAULT_TOKEN=root
vault auth enable kubernetes
vault write auth/kubernetes/config kubernetes_host=https://kubernetes.default.svc:443
printf "path \"secret/data/k8s-auth\" { capabilities = [\"read\"] }\n" | vault policy write sluice-read -
vault write auth/kubernetes/role/sluice bound_service_account_names=sluice bound_service_account_namespaces=sluice token_policies=sluice-read ttl=1h
vault kv put secret/k8s-auth password=vault-k8s-value
'

k create secret generic sluice-db --from-literal=url="postgres://sluice:sluice@postgres:5432/sluice?sslmode=disable"
k create secret generic sluice-master-keys --from-literal=keys="k1:$(head -c 32 /dev/urandom | base64)"
k create secret generic sluice-admin --from-literal=email=admin@example.com --from-literal=password=admin-password-1
# A Secret for the kubernetes secret provider (SCN-SEC-012).
k create secret generic app-credentials --from-literal=api-key=k8s-secret-value

helm lint deploy/helm/sluice
helm --kube-context "$ctx" -n "$ns" install sluice deploy/helm/sluice --wait --timeout 5m \
  --set image.repository=sluice --set image.tag=dev --set image.pullPolicy=Never \
  --set runnerImage=sluice:dev --set replicas=2 \
  --set masterKeys.existingSecret=sluice-master-keys --set bootstrapAdmin.existingSecret=sluice-admin \
  --set executors=kubernetes --set kubernetesSecretProvider.enabled=true \
  --set kubernetes.pendingTimeout=20s \
  --set-json 'extraEnv=[{"name":"SLUICE_VAULT_ADDR","value":"http://vault:8200"},{"name":"SLUICE_VAULT_K8S_ROLE","value":"sluice"}]'

go build -o "$root/bin/sluice-k8s" ./cmd/sluice
kind get kubeconfig --name "$cluster" > "$root/build/kind-kubeconfig"
export SLUICE_K8S_CONTEXT="$ctx" SLUICE_K8S_TEST_NAMESPACE="$ns" SLUICE_K8S_RELEASE=sluice
export SLUICE_E2E_BINARY="$root/bin/sluice-k8s" SLUICE_KIND_KUBECONFIG="$root/build/kind-kubeconfig"
go tool gotestsum --format pkgname-and-test-fails --junitfile "$junit/k8s.xml" -- -tags k8s -count=1 -timeout 60m ./tests/k8s/...
