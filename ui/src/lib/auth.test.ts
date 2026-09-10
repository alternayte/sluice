import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import "@/api-client";
import { getMeOptions } from "@/api/@tanstack/react-query.gen";
import { meQueryKey } from "./auth";

describe("meQueryKey", () => {
  it("matches the key useMe builds, once api-client has set the base URL", () => {
    expect(meQueryKey()).toEqual(getMeOptions().queryKey);
  });

  it("invalidates a seeded me query", () => {
    const qc = new QueryClient();
    const key = meQueryKey();
    qc.setQueryData(key, { id: "1" });

    void qc.invalidateQueries({ queryKey: meQueryKey() });

    expect(qc.getQueryState(key)?.isInvalidated).toBe(true);
  });
});
