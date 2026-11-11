import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight, FileText, Folder, FolderOpen, Trash2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { formatSize } from "@/lib/format";
import type { LogFileInfo } from "@/lib/types";
import { cn } from "@/lib/utils";

interface DirNode {
  name: string;
  path: string;
  dirs: Map<string, DirNode>;
  files: LogFileInfo[];
}

function buildTree(files: LogFileInfo[]): DirNode {
  const root: DirNode = { name: "", path: "", dirs: new Map(), files: [] };
  for (const file of files) {
    const parts = file.path.split("/").filter(Boolean);
    let node = root;
    for (const part of parts.slice(0, -1)) {
      let next = node.dirs.get(part);
      if (!next) {
        next = {
          name: part,
          path: node.path ? `${node.path}/${part}` : part,
          dirs: new Map(),
          files: [],
        };
        node.dirs.set(part, next);
      }
      node = next;
    }
    node.files.push(file);
  }
  return root;
}

interface LogFileTreeProps {
  files: LogFileInfo[];
  selectedFile: string | null;
  filter: string;
  onFilterChange: (value: string) => void;
  onSelect: (path: string) => void;
  onDelete: (path: string) => void;
}

// LogFileTree gives the flat logs/tree response a directory shape. Filtering
// flattens the result so every match is visible without expanding ancestors.
export default function LogFileTree({
  files,
  selectedFile,
  filter,
  onFilterChange,
  onSelect,
  onDelete,
}: LogFileTreeProps) {
  const { t } = useTranslation();
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const tree = useMemo(() => buildTree(files), [files]);
  const normalized = filter.trim().toLowerCase();
  const matches = normalized
    ? files.filter((file) => file.path.toLowerCase().includes(normalized))
    : [];

  const toggle = (path: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  };

  const fileRow = (file: LogFileInfo, depth: number) => {
    const selected = selectedFile === file.path;
    return (
      <div
        key={file.path}
        className={cn(
          "group flex items-center gap-1.5 border-l-2 pr-1",
          selected
            ? "border-primary bg-accent"
            : "border-transparent hover:bg-secondary",
        )}
        style={{ paddingLeft: 8 + depth * 14 }}
      >
        <FileText className="h-3.5 w-3.5 flex-none text-muted-foreground" aria-hidden="true" />
        <button
          type="button"
          onClick={() => onSelect(file.path)}
          className={cn(
            "min-w-0 flex-1 truncate py-1.5 text-left font-mono text-[11px]",
            selected ? "font-semibold text-foreground" : "text-muted-foreground",
          )}
          title={file.path}
        >
          {normalized ? file.path : file.path.split("/").pop()}
        </button>
        <span className="flex-none text-[11px] text-muted-foreground">
          {formatSize(file.size)}
        </span>
        <button
          type="button"
          aria-label={`${t("instances.detail.deleteLogFile")} ${file.path}`}
          onClick={() => onDelete(file.path)}
          className="flex-none rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-secondary hover:text-destructive focus-visible:opacity-100 group-hover:opacity-100 max-sm:opacity-100"
        >
          <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
        </button>
      </div>
    );
  };

  const renderDir = (node: DirNode, depth: number) => {
    const dirs = [...node.dirs.values()];
    const rows = [
      ...dirs.map((dir) => {
        const isCollapsed = collapsed.has(dir.path);
        const Icon = isCollapsed ? Folder : FolderOpen;
        return (
          <div key={`dir-${dir.path}`}>
            <button
              type="button"
              onClick={() => toggle(dir.path)}
              className="flex w-full items-center gap-1.5 rounded-md py-1.5 text-left text-xs text-muted-foreground hover:bg-secondary/70"
              style={{ paddingLeft: 8 + depth * 14 }}
            >
              {isCollapsed ? (
                <ChevronRight className="h-3.5 w-3.5 flex-none" aria-hidden="true" />
              ) : (
                <ChevronDown className="h-3.5 w-3.5 flex-none" aria-hidden="true" />
              )}
              <Icon className="h-3.5 w-3.5 flex-none" aria-hidden="true" />
              <span className="truncate font-medium">{dir.name}</span>
            </button>
            {!isCollapsed && renderDir(dir, depth + 1)}
          </div>
        );
      }),
      ...node.files.map((file) => fileRow(file, depth)),
    ];
    return rows;
  };

  return (
    <div className="flex w-full flex-none flex-col border-b bg-secondary/40 xl:w-60 xl:border-r xl:border-b-0">
      <div className="border-b p-2">
        <input
          value={filter}
          onChange={(e) => onFilterChange(e.target.value)}
          placeholder={t("instances.detail.filterFiles")}
          className="h-8 w-full rounded-md border bg-card px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring"
        />
      </div>
      <div className="max-h-48 min-h-0 flex-1 overflow-y-auto p-1 xl:max-h-[466px]">
        {files.length === 0 ? (
          <p className="p-2 text-xs text-muted-foreground">
            {t("instances.detail.noLogFiles")}
          </p>
        ) : normalized ? (
          matches.length === 0 ? (
            <p className="p-2 text-xs text-muted-foreground">{t("logViewer.noMatches")}</p>
          ) : (
            matches.map((file) => fileRow(file, 0))
          )
        ) : (
          renderDir(tree, 0)
        )}
      </div>
    </div>
  );
}
