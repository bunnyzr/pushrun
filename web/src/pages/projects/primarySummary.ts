import type { Project } from "@/lib/types";

// primarySummary describes the mount that receives pushes: the git repo when
// the primary mount is a git mount, otherwise "path · provider".
export function primarySummary(project: Project): string | null {
  const primary = project.tree.find((n) => n.mount?.primary);
  if (!primary?.mount) return null;
  const repo = primary.mount.params?.repo;
  if (primary.mount.provider === "git" && repo) return repo;
  return `${primary.path} · ${primary.mount.provider}`;
}
