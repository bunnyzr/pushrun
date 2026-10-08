import { useState } from "react";
import {
  DndContext,
  PointerSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import { ChevronDown, ChevronRight, GripVertical, Pencil, Plus, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Badge, type BadgeProps } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { toast } from "@/lib/toast";
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
} from "@/lib/tree-ops";
import type { MountStatus, TreeNode } from "@/lib/types";
import { cn } from "@/lib/utils";

const STATUS_VARIANTS: Record<MountStatus["status"], BadgeProps["variant"]> = {
  cold: "muted",
  warming: "running",
  warm: "success",
};

interface TreeRowProps {
  node: TreeNode;
  depth: number;
  hasKids: boolean;
  isCollapsed: boolean;
  selected: boolean;
  renaming: boolean;
  status?: MountStatus;
  canAddChild: boolean;
  onToggle: () => void;
  onSelect: () => void;
  onRenameStart: () => void;
  onRenameCancel: () => void;
  onRenameCommit: (value: string) => void;
  onDelete: () => void;
  onAddChild: () => void;
}

function TreeRow({
  node,
  depth,
  hasKids,
  isCollapsed,
  selected,
  renaming,
  status,
  canAddChild,
  onToggle,
  onSelect,
  onRenameStart,
  onRenameCancel,
  onRenameCommit,
  onDelete,
  onAddChild,
}: TreeRowProps) {
  const { t } = useTranslation();
  const {
    attributes,
    listeners,
    setNodeRef: setDragRef,
    isDragging,
  } = useDraggable({ id: node.path });
  const { setNodeRef: setDropRef, isOver } = useDroppable({ id: node.path });
  const name = nodeName(node.path);

  return (
    <div
      ref={setDropRef}
      className={cn(
        "flex items-center gap-1 rounded-md py-0.5 pr-1",
        isOver && "bg-accent/60 ring-1 ring-primary",
        isDragging && "opacity-40",
      )}
      style={{ paddingLeft: depth * 14 }}
    >
      <span
        ref={setDragRef}
        {...listeners}
        {...attributes}
        aria-label={t("projects.tree.drag")}
        className="cursor-grab touch-none text-muted-foreground"
      >
        <GripVertical className="h-3.5 w-3.5" />
      </span>
      {hasKids ? (
        <button
          type="button"
          onClick={onToggle}
          aria-label={isCollapsed ? t("projects.tree.collapse") : t("projects.tree.expand")}
          className="text-muted-foreground hover:text-foreground"
        >
          {isCollapsed ? (
            <ChevronRight className="h-3.5 w-3.5" />
          ) : (
            <ChevronDown className="h-3.5 w-3.5" />
          )}
        </button>
      ) : (
        <span className="w-3.5" />
      )}
      {renaming ? (
        <Input
          autoFocus
          defaultValue={name}
          className="h-7 w-36 px-1 text-sm"
          onKeyDown={(e) => {
            if (e.key === "Enter") onRenameCommit(e.currentTarget.value);
            if (e.key === "Escape") onRenameCancel();
          }}
          onBlur={(e) => onRenameCommit(e.target.value)}
        />
      ) : (
        <button
          type="button"
          onClick={onSelect}
          className={cn(
            "rounded px-1.5 py-0.5 text-sm hover:bg-accent",
            selected && "bg-accent font-medium text-primary",
          )}
        >
          {name}
        </button>
      )}
      {node.mount && <Badge variant="secondary">{node.mount.provider}</Badge>}
      {node.mount?.primary && <Badge variant="outline">{t("projects.tree.primary")}</Badge>}
      {node.mount && status && (
        <Badge variant={STATUS_VARIANTS[status.status]}>
          {t(`projects.warmupStatus.${status.status}`)}
        </Badge>
      )}
      <span className="ml-auto flex items-center gap-0.5">
        <button
          type="button"
          aria-label={`${t("projects.tree.rename")} ${name}`}
          onClick={onRenameStart}
          className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <Pencil className="h-3.5 w-3.5" />
        </button>
        {canAddChild && (
          <button
            type="button"
            aria-label={`${t("projects.tree.addChild")} ${name}`}
            onClick={onAddChild}
            className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <Plus className="h-3.5 w-3.5" />
          </button>
        )}
        <button
          type="button"
          aria-label={`${t("projects.tree.delete")} ${name}`}
          onClick={onDelete}
          className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-destructive"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </span>
    </div>
  );
}

type Row =
  | { kind: "node"; node: TreeNode; depth: number; hasKids: boolean }
  | { kind: "add"; depth: number };

// maskedSecretPaths returns the paths of nodes in path's subtree whose mount
// carries a masked ("***") param value — secrets the server can no longer
// restore once the path changes, so the save would be rejected.
function maskedSecretPaths(nodes: TreeNode[], path: string): string[] {
  return nodes
    .filter(
      (n) =>
        (n.path === path || n.path.startsWith(`${path}/`)) &&
        n.mount?.params &&
        Object.values(n.mount.params).includes("***"),
    )
    .map((n) => n.path);
}

function buildRows(
  nodes: TreeNode[],
  collapsed: Set<string>,
  adding: { parent: string | null } | null,
): Row[] {
  const pathSet = new Set(nodes.map((n) => n.path));
  // Nodes whose parent is absent from the tree are hoisted to the root level
  // so hand-written projects with gaps still render.
  const roots = nodes.filter((n) => {
    const parent = parentPathOf(n.path);
    return parent === null || !pathSet.has(parent);
  });
  const rows: Row[] = [];
  const walk = (list: TreeNode[], depth: number) => {
    for (const n of list) {
      const kids = childrenOf(nodes, n.path);
      rows.push({ kind: "node", node: n, depth, hasKids: kids.length > 0 });
      if (kids.length > 0 && !collapsed.has(n.path)) walk(kids, depth + 1);
      if (adding?.parent === n.path) rows.push({ kind: "add", depth: depth + 1 });
    }
  };
  walk(roots, 0);
  if (adding?.parent === null) rows.push({ kind: "add", depth: 0 });
  return rows;
}

export interface TreeEditorProps {
  nodes: TreeNode[];
  selectedPath: string | null;
  statusByPath: Record<string, MountStatus>;
  onSelect: (path: string) => void;
  onChange: (nodes: TreeNode[]) => void;
}

export default function TreeEditor({
  nodes,
  selectedPath,
  statusByPath,
  onSelect,
  onChange,
}: TreeEditorProps) {
  const { t } = useTranslation();
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [renaming, setRenaming] = useState<string | null>(null);
  const [adding, setAdding] = useState<{ parent: string | null } | null>(null);

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));

  const reportError = (err: unknown) => {
    toast(err instanceof Error ? err.message : String(err), "error");
  };

  const toggleCollapsed = (path: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  };

  // Warns when a rename/move detached masked secret params from their stored
  // values (the server keys them by path and rejects the save otherwise).
  const warnMaskedSecrets = (path: string, next: TreeNode[]) => {
    const orphaned = maskedSecretPaths(nodes, path).filter(
      (p) => !next.some((n) => n.path === p),
    );
    if (orphaned.length > 0) {
      toast(t("projects.tree.secretReenter", { paths: orphaned.join(", ") }), "error");
    }
  };

  const commitRename = (path: string, value: string) => {
    setRenaming(null);
    const name = value.trim();
    if (!name || name === nodeName(path)) return;
    try {
      const next = renameNode(nodes, path, name);
      onChange(next);
      warnMaskedSecrets(path, next);
      const parent = parentPathOf(path);
      onSelect(parent === null ? name : `${parent}/${name}`);
    } catch (err) {
      reportError(err);
    }
  };

  const commitAdd = (value: string) => {
    const target = adding;
    setAdding(null);
    if (!target) return;
    const name = value.trim();
    if (!name) return;
    try {
      onChange(addNode(nodes, target.parent, name));
      onSelect(target.parent === null ? name : `${target.parent}/${name}`);
    } catch (err) {
      reportError(err);
    }
  };

  const handleDelete = (path: string) => {
    try {
      onChange(deleteNode(nodes, path));
    } catch (err) {
      reportError(err);
    }
  };

  // Drop semantics: onto a sibling → reorder before it; onto an unmounted
  // folder in another level → reparent into it; onto a mounted node → drop as
  // its sibling. tree-ops owns the invariants and throws on illegal moves.
  const handleDragEnd = (event: DragEndEvent) => {
    const activePath = String(event.active.id);
    const overPath = event.over ? String(event.over.id) : null;
    if (!overPath || overPath === activePath) return;
    try {
      const activeParent = parentPathOf(activePath);
      const overParent = parentPathOf(overPath);
      if (activeParent === overParent) {
        const siblings = childrenOf(nodes, activeParent).filter((n) => n.path !== activePath);
        const idx = siblings.findIndex((n) => n.path === overPath);
        if (idx !== -1) onChange(reorderNode(nodes, activePath, idx));
        return;
      }
      let next: TreeNode[];
      if (canAcceptChild(nodes, overPath)) {
        next = moveNode(nodes, activePath, overPath);
      } else {
        const moved = moveNode(nodes, activePath, overParent);
        const movedPath =
          overParent === null ? nodeName(activePath) : `${overParent}/${nodeName(activePath)}`;
        const siblings = childrenOf(moved, overParent).filter((n) => n.path !== movedPath);
        const idx = siblings.findIndex((n) => n.path === overPath);
        next = idx === -1 ? moved : reorderNode(moved, movedPath, idx);
      }
      onChange(next);
      warnMaskedSecrets(activePath, next);
    } catch (err) {
      reportError(err);
    }
  };

  const rows = buildRows(nodes, collapsed, adding);

  return (
    <section aria-label={t("projects.tree.title")} className="rounded-lg border p-4">
      <div className="mb-2 flex items-center gap-2">
        <h3 className="text-sm font-semibold">{t("projects.tree.title")}</h3>
        <Button
          size="sm"
          variant="outline"
          className="ml-auto"
          onClick={() => setAdding({ parent: null })}
        >
          {t("projects.tree.addRoot")}
        </Button>
      </div>
      {nodes.length === 0 && !adding ? (
        <p className="text-sm text-muted-foreground">{t("projects.tree.empty")}</p>
      ) : (
        <DndContext sensors={sensors} onDragEnd={handleDragEnd}>
          <div className="space-y-0.5">
            {rows.map((row) =>
              row.kind === "add" ? (
                <div key={`add-${adding?.parent ?? "root"}`} style={{ paddingLeft: row.depth * 14 }}>
                  <Input
                    autoFocus
                    placeholder={t("projects.tree.newNodePlaceholder")}
                    className="h-7 w-44 px-1 text-sm"
                    onKeyDown={(e) => {
                      if (e.key === "Enter") commitAdd(e.currentTarget.value);
                      if (e.key === "Escape") setAdding(null);
                    }}
                    onBlur={(e) => commitAdd(e.target.value)}
                  />
                </div>
              ) : (
                <TreeRow
                  key={row.node.path}
                  node={row.node}
                  depth={row.depth}
                  hasKids={row.hasKids}
                  isCollapsed={collapsed.has(row.node.path)}
                  selected={selectedPath === row.node.path}
                  renaming={renaming === row.node.path}
                  status={statusByPath[row.node.path]}
                  canAddChild={canAcceptChild(nodes, row.node.path)}
                  onToggle={() => toggleCollapsed(row.node.path)}
                  onSelect={() => onSelect(row.node.path)}
                  onRenameStart={() => setRenaming(row.node.path)}
                  onRenameCancel={() => setRenaming(null)}
                  onRenameCommit={(value) => commitRename(row.node.path, value)}
                  onDelete={() => handleDelete(row.node.path)}
                  onAddChild={() => setAdding({ parent: row.node.path })}
                />
              ),
            )}
          </div>
        </DndContext>
      )}
    </section>
  );
}
