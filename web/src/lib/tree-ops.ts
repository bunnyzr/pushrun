import type { Step, TreeNode } from "./types";

export class TreeOpError extends Error {
  code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "TreeOpError";
    this.code = code;
  }
}

export interface TreeIssue {
  code:
    | "path_not_clean"
    | "duplicate_path"
    | "mount_has_child"
    | "multiple_primary"
    | "background_not_last"
    | "multiple_background";
  path?: string;
}

export function nodeName(path: string): string {
  const idx = path.lastIndexOf("/");
  return idx === -1 ? path : path.slice(idx + 1);
}

export function parentPathOf(path: string): string | null {
  const idx = path.lastIndexOf("/");
  return idx === -1 ? null : path.slice(0, idx);
}

export function findNode(nodes: TreeNode[], path: string): TreeNode | undefined {
  return nodes.find((n) => n.path === path);
}

function isDescendant(path: string, ancestor: string): boolean {
  return path.startsWith(ancestor + "/");
}

function isValidName(name: string): boolean {
  return name !== "" && name.trim() !== "" && !name.includes("/") && name !== "." && name !== "..";
}

function requireValidName(name: string): void {
  if (!isValidName(name)) {
    throw new TreeOpError("invalid_name", `invalid node name ${JSON.stringify(name)}`);
  }
}

// cleanPath mirrors Go's path.Clean closely enough for the editor's
// validation: resolving "." and ".." segments and collapsing slashes.
function cleanPath(path: string): string {
  const out: string[] = [];
  for (const seg of path.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") {
      if (out.length > 0 && out[out.length - 1] !== "..") {
        out.pop();
      } else {
        out.push("..");
      }
      continue;
    }
    out.push(seg);
  }
  return out.join("/");
}

// A block is a node plus its whole subtree, in array order.
function extractBlock(nodes: TreeNode[], path: string): { block: TreeNode[]; rest: TreeNode[] } {
  const start = nodes.findIndex((n) => n.path === path);
  if (start === -1) {
    throw new TreeOpError("not_found", `node ${JSON.stringify(path)} not found`);
  }
  let end = start + 1;
  while (end < nodes.length && isDescendant(nodes[end]!.path, path)) {
    end++;
  }
  return { block: nodes.slice(start, end), rest: [...nodes.slice(0, start), ...nodes.slice(end)] };
}

function rewritePrefix(nodes: TreeNode[], oldPath: string, newPath: string): TreeNode[] {
  return nodes.map((n) => {
    if (n.path === oldPath) return { ...n, path: newPath };
    if (isDescendant(n.path, oldPath)) return { ...n, path: newPath + n.path.slice(oldPath.length) };
    return n;
  });
}

export function canAcceptChild(nodes: TreeNode[], path: string): boolean {
  const node = findNode(nodes, path);
  return node !== undefined && node.mount === undefined;
}

export function childrenOf(nodes: TreeNode[], parentPath: string | null): TreeNode[] {
  return nodes.filter((n) => parentPathOf(n.path) === parentPath);
}

export function addNode(nodes: TreeNode[], parentPath: string | null, name: string): TreeNode[] {
  requireValidName(name);
  const path = parentPath === null ? name : `${parentPath}/${name}`;
  if (findNode(nodes, path)) {
    throw new TreeOpError("duplicate", `duplicate node path ${JSON.stringify(path)}`);
  }
  if (parentPath !== null) {
    if (!findNode(nodes, parentPath)) {
      throw new TreeOpError("parent_not_found", `parent node ${JSON.stringify(parentPath)} not found`);
    }
    if (!canAcceptChild(nodes, parentPath)) {
      throw new TreeOpError("parent_mounted", `parent ${JSON.stringify(parentPath)} is mounted`);
    }
    // Insert at the end of the parent's subtree so siblings stay grouped.
    const idx = nodes.findIndex((n) => n.path === parentPath);
    let end = idx + 1;
    while (end < nodes.length && isDescendant(nodes[end]!.path, parentPath)) {
      end++;
    }
    return [...nodes.slice(0, end), { path }, ...nodes.slice(end)];
  }
  return [...nodes, { path }];
}

export function renameNode(nodes: TreeNode[], path: string, newName: string): TreeNode[] {
  requireValidName(newName);
  if (!findNode(nodes, path)) {
    throw new TreeOpError("not_found", `node ${JSON.stringify(path)} not found`);
  }
  const parent = parentPathOf(path);
  const newPath = parent === null ? newName : `${parent}/${newName}`;
  if (newPath === path) return nodes;
  if (findNode(nodes, newPath)) {
    throw new TreeOpError("duplicate", `duplicate node path ${JSON.stringify(newPath)}`);
  }
  return rewritePrefix(nodes, path, newPath);
}

export function deleteNode(nodes: TreeNode[], path: string): TreeNode[] {
  const { rest } = extractBlock(nodes, path);
  return rest;
}

export function moveNode(
  nodes: TreeNode[],
  path: string,
  newParentPath: string | null,
): TreeNode[] {
  if (newParentPath !== null) {
    if (newParentPath === path || isDescendant(newParentPath, path)) {
      throw new TreeOpError("own_subtree", `cannot move ${JSON.stringify(path)} into its own subtree`);
    }
    const target = findNode(nodes, newParentPath);
    if (!target) {
      throw new TreeOpError("parent_not_found", `parent node ${JSON.stringify(newParentPath)} not found`);
    }
    if (target.mount !== undefined) {
      throw new TreeOpError("parent_mounted", `target ${JSON.stringify(newParentPath)} is mounted`);
    }
  }
  const name = nodeName(path);
  const newPath = newParentPath === null ? name : `${newParentPath}/${name}`;
  if (newPath !== path && findNode(nodes, newPath)) {
    throw new TreeOpError("duplicate", `duplicate node path ${JSON.stringify(newPath)}`);
  }
  if (newPath === path) return nodes;
  const { block, rest } = extractBlock(nodes, path);
  const moved = rewritePrefix(block, path, newPath);
  if (newParentPath === null) {
    return [...rest, ...moved];
  }
  const idx = rest.findIndex((n) => n.path === newParentPath);
  let end = idx + 1;
  while (end < rest.length && isDescendant(rest[end]!.path, newParentPath)) {
    end++;
  }
  return [...rest.slice(0, end), ...moved, ...rest.slice(end)];
}

// reorderNode moves a node (with its subtree) to the given index within its
// sibling group. The index addresses the sibling list without the moved node.
export function reorderNode(nodes: TreeNode[], path: string, targetIndex: number): TreeNode[] {
  const parent = parentPathOf(path);
  const { block, rest } = extractBlock(nodes, path);
  const siblings = childrenOf(rest, parent);
  const clamped = Math.max(0, Math.min(targetIndex, siblings.length));
  const anchor = siblings[clamped];
  if (anchor === undefined) {
    // Append after the last remaining sibling (or the parent, or the list end).
    if (siblings.length > 0) {
      const last = siblings[siblings.length - 1]!;
      let end = rest.findIndex((n) => n.path === last.path) + 1;
      while (end < rest.length && isDescendant(rest[end]!.path, last.path)) {
        end++;
      }
      return [...rest.slice(0, end), ...block, ...rest.slice(end)];
    }
    if (parent !== null) {
      const idx = rest.findIndex((n) => n.path === parent);
      let end = idx + 1;
      while (end < rest.length && isDescendant(rest[end]!.path, parent)) {
        end++;
      }
      return [...rest.slice(0, end), ...block, ...rest.slice(end)];
    }
    return [...rest, ...block];
  }
  const idx = rest.findIndex((n) => n.path === anchor.path);
  return [...rest.slice(0, idx), ...block, ...rest.slice(idx)];
}

// validateTree mirrors the server invariants in internal/project: node paths
// are relative and clean, a mount node is a leaf, and at most one mount is
// primary. Duplicate paths are flagged too; the UI ops never produce them.
export function validateTree(nodes: TreeNode[]): TreeIssue[] {
  const issues: TreeIssue[] = [];
  const seen = new Set<string>();
  let primary = 0;
  for (const n of nodes) {
    if (
      n.path === "" ||
      n.path.startsWith("/") ||
      cleanPath(n.path) !== n.path ||
      n.path.startsWith("..")
    ) {
      issues.push({ code: "path_not_clean", path: n.path });
    }
    if (seen.has(n.path)) {
      issues.push({ code: "duplicate_path", path: n.path });
    }
    seen.add(n.path);
    if (n.mount === undefined) continue;
    if (n.mount.primary) primary++;
    for (const other of nodes) {
      if (other.path !== n.path && isDescendant(other.path, n.path)) {
        issues.push({ code: "mount_has_child", path: n.path });
        break;
      }
    }
  }
  if (primary > 1) {
    issues.push({ code: "multiple_primary" });
  }
  return issues;
}

// validatePipeline mirrors the server invariants: at most one background step
// and it must be the last step.
export function validatePipeline(steps: Step[]): TreeIssue[] {
  const issues: TreeIssue[] = [];
  let background = 0;
  steps.forEach((s, i) => {
    if (!s.background) return;
    background++;
    if (i !== steps.length - 1) {
      issues.push({ code: "background_not_last", path: s.name });
    }
  });
  if (background > 1) {
    issues.push({ code: "multiple_background" });
  }
  return issues;
}
