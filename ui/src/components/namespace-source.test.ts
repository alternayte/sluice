import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { listNamespacesQueryKey } from "@/api/@tanstack/react-query.gen";
import { invalidateNamespace } from "./namespace-source";

function isInvalidated(qc: QueryClient, key: unknown[]): boolean {
  return qc.getQueryState(key)?.isInvalidated ?? false;
}

describe("invalidateNamespace", () => {
  it("refreshes namespace-scoped queries, flow queries and the hand-written content key", () => {
    const qc = new QueryClient();

    const namespaceInfo = [{ _id: "getNamespace", baseUrl: "", path: { namespace: "a" } }];
    const otherNamespaceInfo = [{ _id: "getNamespace", baseUrl: "", path: { namespace: "b" } }];
    const files = [{ _id: "listFiles", baseUrl: "", path: { namespace: "a" } }];
    const versions = [{ _id: "listVersions", baseUrl: "", path: { namespace: "a" } }];
    const versionDiff = [{ _id: "diffVersions", baseUrl: "", path: { namespace: "a" }, query: { from: 1, to: 2 } }];
    const namespaces = listNamespacesQueryKey();
    const flows = [{ _id: "listFlows", baseUrl: "" }];
    const flow = [{ _id: "getFlow", baseUrl: "", path: { namespace: "a", flowId: "f" } }];
    const flowRevisions = [{ _id: "listFlowRevisions", baseUrl: "", path: { namespace: "a", flowId: "f" } }];
    const flowDiff = [{ _id: "diffFlowRevisions", baseUrl: "", path: { namespace: "a", flowId: "f" } }];
    const content = ["namespace", "a", "file", "x.yaml"];

    for (const key of [
      namespaceInfo,
      otherNamespaceInfo,
      files,
      versions,
      versionDiff,
      namespaces,
      flows,
      flow,
      flowRevisions,
      flowDiff,
      content,
    ]) {
      qc.setQueryData(key, "seed");
    }

    invalidateNamespace(qc, "a");

    expect(isInvalidated(qc, namespaceInfo)).toBe(true);
    expect(isInvalidated(qc, otherNamespaceInfo)).toBe(false);
    expect(isInvalidated(qc, files)).toBe(true);
    expect(isInvalidated(qc, versions)).toBe(true);
    expect(isInvalidated(qc, versionDiff)).toBe(true);
    expect(isInvalidated(qc, namespaces)).toBe(true);
    expect(isInvalidated(qc, flows)).toBe(true);
    expect(isInvalidated(qc, flow)).toBe(true);
    expect(isInvalidated(qc, flowRevisions)).toBe(true);
    expect(isInvalidated(qc, flowDiff)).toBe(true);
    expect(isInvalidated(qc, content)).toBe(true);
  });
});
