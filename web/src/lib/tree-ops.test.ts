import { describe, expect, it } from "vitest";
import type { TreeNode } from "./types";
import {
  addNode,
  canAcceptChild,
  childrenOf,
  deleteNode,
  moveNode,
  nodeName,
  parentPathOf,
  renameNode,
  reorderNode,
  validatePipeline,
  validateTree,
} from "./tree-ops";

function paths(nodes: TreeNode[]): string[] {
  return nodes.map((n) => n.path);
}

const mounted = (primary = false): TreeNode["mount"] => ({ provider: "git", primary });

describe("path helpers", () => {
  it("splits names and parents", () => {
    expect(nodeName("a/b/c")).toBe("c");
    expect(nodeName("a")).toBe("a");
    expect(parentPathOf("a/b/c")).toBe("a/b");
    expect(parentPathOf("a")).toBeNull();
  });

  it("lists direct children in order", () => {
    const nodes: TreeNode[] = [
      { path: "a" },
      { path: "a/x" },
      { path: "a/x/deep" },
      { path: "a/y" },
      { path: "b" },
    ];
    expect(paths(childrenOf(nodes, null))).toEqual(["a", "b"]);
    expect(paths(childrenOf(nodes, "a"))).toEqual(["a/x", "a/y"]);
    expect(paths(childrenOf(nodes, "a/x"))).toEqual(["a/x/deep"]);
  });
});

describe("addNode", () => {
  it("adds a root node and a child node", () => {
    let nodes: TreeNode[] = [];
    nodes = addNode(nodes, null, "app");
    nodes = addNode(nodes, "app", "src");
    expect(paths(nodes)).toEqual(["app", "app/src"]);
  });

  it("refuses a duplicate path", () => {
    const nodes: TreeNode[] = [{ path: "app" }];
    expect(() => addNode(nodes, null, "app")).toThrowError(/duplicate/);
  });

  it("refuses invalid names", () => {
    const nodes: TreeNode[] = [{ path: "app" }];
    for (const bad of ["", "a/b", ".", "..", "  "]) {
      expect(() => addNode(nodes, "app", bad)).toThrowError(/name/);
    }
  });

  it("refuses to add under a mounted node", () => {
    const nodes: TreeNode[] = [{ path: "app", mount: mounted() }];
    expect(() => addNode(nodes, "app", "src")).toThrowError(/mount/);
  });

  it("refuses an unknown parent", () => {
    expect(() => addNode([], "ghost", "x")).toThrowError(/parent/);
  });
});

describe("renameNode", () => {
  it("rewrites descendant prefixes", () => {
    const nodes: TreeNode[] = [
      { path: "a" },
      { path: "a/x" },
      { path: "a/x/y", mount: mounted() },
      { path: "c" },
    ];
    const renamed = renameNode(nodes, "a", "b");
    expect(paths(renamed)).toEqual(["b", "b/x", "b/x/y", "c"]);
    expect(renamed[2]?.mount).toEqual(mounted());
  });

  it("renames a nested node", () => {
    const nodes: TreeNode[] = [{ path: "a" }, { path: "a/x" }, { path: "a/x/y" }];
    expect(paths(renameNode(nodes, "a/x", "z"))).toEqual(["a", "a/z", "a/z/y"]);
  });

  it("refuses a conflicting sibling name", () => {
    const nodes: TreeNode[] = [{ path: "a" }, { path: "b" }];
    expect(() => renameNode(nodes, "a", "b")).toThrowError(/duplicate/);
  });

  it("refuses invalid names", () => {
    const nodes: TreeNode[] = [{ path: "a" }];
    expect(() => renameNode(nodes, "a", "x/y")).toThrowError(/name/);
  });
});

describe("deleteNode", () => {
  it("drops the whole subtree", () => {
    const nodes: TreeNode[] = [
      { path: "a" },
      { path: "a/x" },
      { path: "a/x/y" },
      { path: "b" },
    ];
    expect(paths(deleteNode(nodes, "a"))).toEqual(["b"]);
    expect(paths(deleteNode(nodes, "a/x"))).toEqual(["a", "b"]);
  });
});

describe("moveNode", () => {
  const nodes: TreeNode[] = [
    { path: "a" },
    { path: "a/x" },
    { path: "a/x/y", mount: mounted() },
    { path: "b" },
  ];

  it("reparents with prefix rewrite", () => {
    const moved = moveNode(nodes, "a/x", "b");
    expect(paths(moved)).toEqual(["a", "b", "b/x", "b/x/y"]);
    expect(moved.find((n) => n.path === "b/x/y")?.mount).toEqual(mounted());
  });

  it("moves a node to the root", () => {
    const moved = moveNode(nodes, "a/x", null);
    expect(paths(moved)).toContain("x");
    expect(paths(moved)).toContain("x/y");
    expect(paths(moved)).not.toContain("a/x");
  });

  it("refuses to move into its own subtree", () => {
    expect(() => moveNode(nodes, "a", "a/x")).toThrowError(/subtree/);
    expect(() => moveNode(nodes, "a", "a")).toThrowError(/subtree/);
  });

  it("refuses to move onto a mounted node", () => {
    expect(() => moveNode(nodes, "b", "a/x/y")).toThrowError(/mount/);
  });

  it("refuses a path collision at the target", () => {
    const clash: TreeNode[] = [{ path: "a" }, { path: "b" }, { path: "b/a" }];
    expect(() => moveNode(clash, "a", "b")).toThrowError(/duplicate/);
  });
});

describe("canAcceptChild", () => {
  it("is false for mounted nodes and unknown paths, true for plain dirs", () => {
    const nodes: TreeNode[] = [{ path: "a" }, { path: "m", mount: mounted() }];
    expect(canAcceptChild(nodes, "a")).toBe(true);
    expect(canAcceptChild(nodes, "m")).toBe(false);
    expect(canAcceptChild(nodes, "ghost")).toBe(false);
  });
});

describe("reorderNode", () => {
  const nodes: TreeNode[] = [
    { path: "a" },
    { path: "a/x" },
    { path: "b" },
    { path: "c" },
  ];

  it("moves a node within its sibling level, keeping its subtree attached", () => {
    expect(paths(reorderNode(nodes, "a", 1))).toEqual(["b", "a", "a/x", "c"]);
    expect(paths(reorderNode(nodes, "c", 0))).toEqual(["c", "a", "a/x", "b"]);
  });

  it("clamps out-of-range indexes to the ends", () => {
    expect(paths(reorderNode(nodes, "a", 99))).toEqual(["b", "c", "a", "a/x"]);
  });
});

describe("validateTree", () => {
  it("accepts a clean tree", () => {
    const nodes: TreeNode[] = [
      { path: "app", mount: { provider: "git", primary: true } },
      { path: "data" },
      { path: "data/static" },
    ];
    expect(validateTree(nodes)).toEqual([]);
  });

  it("flags unclean or absolute paths", () => {
    const bad = ["../x", "/abs", "a//b", "a/./b", "..", ""];
    for (const path of bad) {
      const issues = validateTree([{ path }]);
      expect(issues.some((i) => i.code === "path_not_clean" && i.path === path)).toBe(true);
    }
  });

  it("flags a mount node with children", () => {
    const nodes: TreeNode[] = [{ path: "app", mount: mounted() }, { path: "app/src" }];
    const issues = validateTree(nodes);
    expect(issues.some((i) => i.code === "mount_has_child" && i.path === "app")).toBe(true);
  });

  it("flags two primary mounts", () => {
    const nodes: TreeNode[] = [
      { path: "a", mount: mounted(true) },
      { path: "b", mount: mounted(true) },
    ];
    const issues = validateTree(nodes);
    expect(issues.some((i) => i.code === "multiple_primary")).toBe(true);
  });

  it("allows a single primary mount", () => {
    const nodes: TreeNode[] = [
      { path: "a", mount: mounted(true) },
      { path: "b", mount: mounted(false) },
    ];
    expect(validateTree(nodes)).toEqual([]);
  });

  it("flags duplicate paths", () => {
    const nodes: TreeNode[] = [{ path: "a" }, { path: "a" }];
    expect(validateTree(nodes).some((i) => i.code === "duplicate_path")).toBe(true);
  });
});

describe("validatePipeline", () => {
  it("accepts foreground steps followed by one background step", () => {
    expect(
      validatePipeline([
        { name: "build", run: "make" },
        { name: "serve", run: "./serve", background: true },
      ]),
    ).toEqual([]);
  });

  it("flags a background step that is not last", () => {
    const issues = validatePipeline([
      { name: "serve", run: "./serve", background: true },
      { name: "tail", run: "echo hi" },
    ]);
    expect(issues.some((i) => i.code === "background_not_last")).toBe(true);
  });

  it("flags more than one background step", () => {
    const issues = validatePipeline([
      { name: "one", run: "a", background: true },
      { name: "two", run: "b", background: true },
    ]);
    expect(issues.some((i) => i.code === "multiple_background")).toBe(true);
  });
});
