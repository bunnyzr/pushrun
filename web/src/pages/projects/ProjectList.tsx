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
import { primarySummary } from "./primarySummary";

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
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
        <div className="divide-y divide-border/70">
          <Label htmlFor="create-name">{t("projects.create.name")}</Label>
          <Input
            id="create-name"
            value={name}
            placeholder={t("projects.create.namePlaceholder")}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div className="divide-y divide-border/70">
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
    <div className="space-y-2">
      <div className="flex gap-2 px-1">
        <Button size="sm" className="flex-1" onClick={() => setCreateOpen(true)}>
          {t("projects.new")}
        </Button>
        <Button
          size="sm"
          variant="outline"
          className="flex-1"
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
        <p role="alert" className="px-1 text-sm text-destructive">
          {importError}
        </p>
      )}
      {projects.length === 0 ? (
        <p className="p-2 text-sm text-muted-foreground">{t("projects.empty")}</p>
      ) : (
        <div className="divide-y divide-border/70">
          {projects.map((project) => {
            const summary = primarySummary(project);
            const selected = selectedName === project.name;
            const subtitle = [project.display_name ? project.name : null, summary]
              .filter(Boolean)
              .join(" · ");
            return (
              <button
                key={project.name}
                type="button"
                onClick={() => onSelect(project.name)}
                className={cn(
                  "w-full px-3 py-2.5 text-left transition-colors hover:bg-secondary",
                  selected && "bg-accent",
                )}
              >
                <span
                  className={cn(
                    "block truncate font-semibold",
                    selected && "text-accent-foreground",
                  )}
                >
                  {project.display_name ?? project.name}
                </span>
                {subtitle && (
                  <span className="mt-0.5 block truncate font-mono text-xs text-muted-foreground">
                    {subtitle}
                  </span>
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
