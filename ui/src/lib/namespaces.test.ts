import { describe, expect, it } from "vitest";
import { namespaceTree } from "./namespaces";

describe("namespaceTree", () => {
  it("puts children after their parent and counts depth", () => {
    const rows = namespaceTree([{ name: "a-b" }, { name: "a.c.d" }, { name: "a" }, { name: "a.c" }, { name: "b" }]);
    expect(rows.map((r) => [r.item.name, r.depth])).toEqual([
      ["a", 0],
      ["a.c", 1],
      ["a.c.d", 2],
      ["a-b", 0],
      ["b", 0],
    ]);
  });

  it("counts only ancestors that are in the list", () => {
    expect(namespaceTree([{ name: "x.y.z" }, { name: "x" }]).map((r) => r.depth)).toEqual([0, 1]);
  });
});
