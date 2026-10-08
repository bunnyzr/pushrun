import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError, apiFetch } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { Project } from "@/lib/types";
import { cn } from "@/lib/utils";

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// primarySummary describes the mount that receives pushes: the git repo when
// the primary mount is a git mount, otherwise "path · provider".
function primarySummary(project: Project): string | null {
  const primary = project.tree.find((n) => n.mount?.primary);
  if (!primary?.mount) return null;
  const repo = primary.mount.params?.repo;
  if (primary.mount.provider === "git" && repo) return repo;
  return `${primary.path} · ${primary.mount.provider}`;
}

interface CreateDialogProps {
  open: boolean;
  onClose: () => void;
  onCreated: (name: string) => void;
}

function CreateDialog({ open, onClose, onCreated }: CreateDialogProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const createMutation = useMutation({
    mutationFn: () =>
      apiFetch<Project>("/projects", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          schema: "pushrun.project/v1",
          name: name.trim(),
          ...(displayName.trim() ? { display_name: displayName.trim() } : {}),
          tree: [],
          pipeline: [],
        }),
      }),
    onSuccess: (project) => {
      setName("");
      setDisplayName("");
      setError(null);
      onClose();
      toast(t("projects.create.created", { name: project.name }));
      void queryClient.invalidateQueries({ queryKey: ["projects"] });
      onCreated(project.name);
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === "conflict") {
        setError(t("projects.create.conflict"));
      } else {
        setError(t("projects.create.failed", { message: errorMessage(err) }));
      }
    },
  });

  const close = () => {
    setError(null);
    onClose();
  };

  return (
    <Dialog
      open={open}
      title={t("projects.create.title")}
      footer={
        <>
          <Button variant="ghost" onClick={close}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={name.trim() === "" || createMutation.isPending}
            onClick={() => createMutation.mutate()}
          >
            {t("projects.create.submit")}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <div className="space-y-1">
          <Label htmlFor="create-name">{t("projects.create.name")}</Label>
          <Input
            id="create-name"
            value={name}
            placeholder={t("projects.create.namePlaceholder")}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="create-display-name">{t("projects.create.displayName")}</Label>
          <Input
            id="create-display-name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </div>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  );
}

export interface ProjectListProps {
  projects: Project[];
  selectedName: string | null;
  onSelect: (name: string) => void;
}

export default function ProjectList({ projects, selectedName, onSelect }: ProjectListProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const fileRef = useRef<HTMLInputElement>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);

  const importMutation = useMutation({
    mutationFn: (file: File) =>
      apiFetch<{ status: string; project: Project }>("/projects/import", {
        method: "POST",
        headers: { "Content-Type": "application/gzip" },
        body: file,
      }),
    onSuccess: (res) => {
      setImportError(null);
      toast(t("projects.imported", { name: res.project.name }));
      void queryClient.invalidateQueries({ queryKey: ["projects"] });
      onSelect(res.project.name);
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === "missing_providers") {
        const raw = err.extra?.missing_providers;
        const missing = Array.isArray(raw) ? raw.join(", ") : "";
        setImportError(t("projects.missingProviders", { providers: missing }));
      } else {
        setImportError(t("projects.importFailed", { message: errorMessage(err) }));
      }
    },
  });

  return (
    <div className="space-y-3">
      <div className="flex gap-2">
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          {t("projects.new")}
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={importMutation.isPending}
          onClick={() => fileRef.current?.click()}
        >
          {t("projects.import")}
        </Button>
        <input
          ref={fileRef}
          type="file"
          accept=".tar.gz,.tgz,application/gzip"
          aria-label={t("projects.import")}
          className="hidden"
          onChange={(e) => {
            const file = e.target.files?.[0];
            e.target.value = "";
            if (file) importMutation.mutate(file);
          }}
        />
      </div>
      {importError && (
        <p role="alert" className="text-sm text-destructive">
          {importError}
        </p>
      )}
      {projects.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("projects.empty")}</p>
      ) : (
        <div className="space-y-2">
          {projects.map((project) => {
            const summary = primarySummary(project);
            return (
              <button
                key={project.name}
                type="button"
                onClick={() => onSelect(project.name)}
                className={cn(
                  "w-full rounded-lg border p-3 text-left transition-colors hover:bg-accent/40",
                  selectedName === project.name && "border-primary bg-accent/50",
                )}
              >
                <div className="font-medium">{project.display_name ?? project.name}</div>
                {project.display_name && (
                  <div className="text-xs text-muted-foreground">{project.name}</div>
                )}
                {summary && (
                  <div className="mt-1 truncate font-mono text-xs text-muted-foreground">
                    {summary}
                  </div>
                )}
              </button>
            );
          })}
        </div>
      )}
      <CreateDialog open={createOpen} onClose={() => setCreateOpen(false)} onCreated={onSelect} />
    </div>
  );
}
