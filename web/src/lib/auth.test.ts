import { beforeEach, describe, expect, it } from "vitest";
import { tokenStore } from "./auth";

describe("tokenStore", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("returns null when no token is stored", () => {
    expect(tokenStore.get()).toBeNull();
  });

  it("round-trips get/set under the pushrun.token key", () => {
    tokenStore.set("tok-abc");
    expect(tokenStore.get()).toBe("tok-abc");
    expect(localStorage.getItem("pushrun.token")).toBe("tok-abc");
  });

  it("clear removes the token", () => {
    tokenStore.set("tok-abc");
    tokenStore.clear();
    expect(tokenStore.get()).toBeNull();
    expect(localStorage.getItem("pushrun.token")).toBeNull();
  });
});
