import { describe, expect, it } from "vitest";
import {
  isValidEnvKey,
  isValidLogDir,
  nextCopyName,
  validateEnvRows,
  validatePortNames,
  type EnvRow,
} from "./project-edit";

describe("isValidEnvKey", () => {
  it("accepts letters, underscore-leading, and mixed names", () => {
    expect(isValidEnvKey("PATH")).toBe(true);
    expect(isValidEnvKey("_PRIVATE")).toBe(true);
    expect(isValidEnvKey("a1_B2")).toBe(true);
  });

  it("rejects digits first, spaces, dashes, and empty", () => {
    expect(isValidEnvKey("1ABC")).toBe(false);
    expect(isValidEnvKey("HAS SPACE")).toBe(false);
    expect(isValidEnvKey("HAS-DASH")).toBe(false);
    expect(isValidEnvKey("")).toBe(false);
  });
});

describe("validateEnvRows", () => {
  it("flags invalid keys with their row index", () => {
    const rows: EnvRow[] = [
      { key: "OK", value: "1" },
      { key: "bad-key", value: "2" },
      { key: "1ST", value: "3" },
    ];
    expect(validateEnvRows(rows)).toEqual([
      { code: "invalid_env_key", index: 1, key: "bad-key" },
      { code: "invalid_env_key", index: 2, key: "1ST" },
    ]);
  });

  it("flags duplicate keys case-sensitively but ignores empty keys", () => {
    const rows: EnvRow[] = [
      { key: "A", value: "1" },
      { key: "A", value: "2" },
      { key: "a", value: "3" },
      { key: "", value: "draft" },
      { key: "", value: "" },
    ];
    expect(validateEnvRows(rows)).toEqual([{ code: "duplicate_env_key", index: 1, key: "A" }]);
  });
});

describe("validatePortNames", () => {
  it("flags names not matching [A-Za-z][A-Za-z0-9]*", () => {
    expect(validatePortNames(["_web", "ok", "x y"])).toEqual([
      { code: "invalid_port_name", index: 0, name: "_web" },
      { code: "invalid_port_name", index: 2, name: "x y" },
    ]);
  });

  it("flags duplicates case-insensitively and ignores empty names", () => {
    expect(validatePortNames(["web", "Web", "WEB2", "", ""])).toEqual([
      { code: "duplicate_port_name", index: 1, name: "Web" },
    ]);
  });
});

describe("isValidLogDir", () => {
  it("accepts empty (default) and clean relative paths", () => {
    expect(isValidLogDir("")).toBe(true);
    expect(isValidLogDir("logs")).toBe(true);
    expect(isValidLogDir("var/log/app")).toBe(true);
  });

  it("rejects absolute paths and traversal", () => {
    expect(isValidLogDir("/var/log")).toBe(false);
    expect(isValidLogDir("..")).toBe(false);
    expect(isValidLogDir("../x")).toBe(false);
    expect(isValidLogDir("a/../b")).toBe(false);
    expect(isValidLogDir("a//b")).toBe(false);
  });
});

describe("nextCopyName", () => {
  it("returns <name>-copy when free", () => {
    expect(nextCopyName("web", ["web", "api"])).toBe("web-copy");
  });

  it("increments past taken copy names", () => {
    expect(nextCopyName("web", ["web", "web-copy"])).toBe("web-copy-2");
    expect(nextCopyName("web", ["web", "web-copy", "web-copy-2", "web-copy-3"])).toBe(
      "web-copy-4",
    );
  });

  it("is case-insensitive about existing names", () => {
    expect(nextCopyName("web", ["Web-Copy"])).toBe("web-copy-2");
  });
});
