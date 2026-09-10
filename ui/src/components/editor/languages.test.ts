import { describe, expect, it } from "vitest";
import { isValidatedPath, languageForPath } from "./languages";

describe("languageForPath", () => {
  it("maps extensions to languages", () => {
    expect(languageForPath("flows/etl.flow.yaml")).toBe("yaml");
    expect(languageForPath("a.yml")).toBe("yaml");
    expect(languageForPath("scripts/run.py")).toBe("python");
    expect(languageForPath("run.sh")).toBe("shell");
    expect(languageForPath("x.bash")).toBe("shell");
    expect(languageForPath("main.ts")).toBe("typescript");
    expect(languageForPath("App.TSX")).toBe("tsx");
    expect(languageForPath("index.js")).toBe("javascript");
    expect(languageForPath("q/report.sql")).toBe("sql");
  });

  it("returns plain for unknown or missing extensions", () => {
    expect(languageForPath("README")).toBe("plain");
    expect(languageForPath(".env")).toBe("plain");
    expect(languageForPath("notes.txt")).toBe("plain");
    expect(languageForPath("dir.py/file")).toBe("plain");
  });
});

describe("isValidatedPath", () => {
  it("accepts flow files and namespace.yaml", () => {
    expect(isValidatedPath("a/b.flow.yaml")).toBe(true);
    expect(isValidatedPath("b.flow.yml")).toBe(true);
    expect(isValidatedPath("namespace.yaml")).toBe(true);
    expect(isValidatedPath("sub/namespace.yaml")).toBe(true);
  });

  it("rejects other files", () => {
    expect(isValidatedPath("config.yaml")).toBe(false);
    expect(isValidatedPath("flow.yaml")).toBe(false);
    expect(isValidatedPath("run.py")).toBe(false);
  });
});
