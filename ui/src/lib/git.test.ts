import { describe, expect, it } from "vitest";
import { commitUrl } from "./git";

describe("commitUrl", () => {
  it("links commits of the known hosts from https, ssh and scp-like URLs", () => {
    expect(commitUrl("https://github.com/acme/flows.git", "abc")).toBe("https://github.com/acme/flows/commit/abc");
    expect(commitUrl("git@github.com:acme/flows.git", "abc")).toBe("https://github.com/acme/flows/commit/abc");
    expect(commitUrl("ssh://git@gitlab.com/group/sub/flows.git", "abc")).toBe(
      "https://gitlab.com/group/sub/flows/-/commit/abc",
    );
    expect(commitUrl("https://x-token@bitbucket.org/acme/flows", "abc")).toBe(
      "https://bitbucket.org/acme/flows/commits/abc",
    );
  });

  it("gives no link for other hosts or without a SHA", () => {
    expect(commitUrl("https://git.example.com/acme/flows.git", "abc")).toBeUndefined();
    expect(commitUrl("https://github.com/acme/flows.git", "")).toBeUndefined();
    expect(commitUrl("not a url", "abc")).toBeUndefined();
  });
});
