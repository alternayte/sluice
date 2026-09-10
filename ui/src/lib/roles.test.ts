import { describe, expect, it } from "vitest";
import { can, isRole, roles, rolesUpTo } from "./roles";

describe("roles", () => {
  it("lists roles from lowest to highest", () => {
    expect(roles).toEqual(["viewer", "operator", "editor", "admin"]);
  });

  it("isRole accepts only known roles", () => {
    expect(isRole("editor")).toBe(true);
    expect(isRole("root")).toBe(false);
    expect(isRole(undefined)).toBe(false);
  });
});

describe("can", () => {
  it("allows the same and lower minimum roles", () => {
    expect(can("admin", "viewer")).toBe(true);
    expect(can("admin", "admin")).toBe(true);
    expect(can("editor", "operator")).toBe(true);
    expect(can("operator", "operator")).toBe(true);
  });

  it("denies higher minimum roles", () => {
    expect(can("viewer", "operator")).toBe(false);
    expect(can("operator", "editor")).toBe(false);
    expect(can("editor", "admin")).toBe(false);
  });

  it("denies unknown and missing roles", () => {
    expect(can(undefined, "viewer")).toBe(false);
    expect(can(null, "viewer")).toBe(false);
    expect(can("root", "viewer")).toBe(false);
  });
});

describe("rolesUpTo", () => {
  it("returns roles up to and including the given role", () => {
    expect(rolesUpTo("viewer")).toEqual(["viewer"]);
    expect(rolesUpTo("editor")).toEqual(["viewer", "operator", "editor"]);
    expect(rolesUpTo("admin")).toEqual([...roles]);
    expect(rolesUpTo(undefined)).toEqual([]);
  });
});
