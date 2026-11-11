import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { GitBranch } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ApiError, apiFetch } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { Project, TreeNode } from "@/lib/types";

interface CreateProjectDialogProps {
  open: boolean;
  onClose: () => void;
  onCreated: (name: string) => void;
}

function repoNodePath(repo: string): string {
  const last = repo.trim().split(/[/:]/).filter(Boolean).pop() ?? "";
  return last.replace(/\.git$/i, "").replace(/[^A-Za-z0-9_.-]+/g, "-") || "project";
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// CreateProjectDialog is the first-run shortcut: a project name is optional
// when a repository is supplied, because the Git node and its primary mount
// are derived from the repo. Users can still create an empty project and
// build the tree by hand.
export default function CreateProjectDialog({
  open,
  onClose,
  onCreated,
}: CreateProjectDialogProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [repo, setRepo] = useState("");
  const [branch, setBranch] = useState("main");
  const [error, setError] = useState<string | null>(null);

  const hasRepo = repo.trim() !== "";
  const derivedName = useMemo(
    () => name.trim() || (hasRepo ? repoNodePath(repo) : ""),
    [hasRepo, name, repo],
  );

  const createMutation = useMutation({
    mutationFn: () => {
      const tree: TreeNode[] = hasRepo
        ? [
            {
              path: repoNodePath(repo),
              mount: {
                provider: "git",
                primary: true,
                params: {
                  repo: repo.trim(),
                  branch: branch.trim() || "main",
                },
              },
            },
          ]
        : [];
      return apiFetch<Project>("/projects", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          schema: "pushrun.project/v1",
          name: derivedName,
          ...(displayName.trim() ? { display_name: displayName.trim() } : {}),
          tree,
          pipeline: [],
        }),
      });
    },
    onSuccess: (project) => {
      setName("");
      setDisplayName("");
      setRepo("");
      setBranch("main");
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

  const canSubmit = derivedName !== "" && !createMutation.isPending;

  const close = () => {
    setError(null);
    onClose();
  };

  return (
    <Dialog
      open={open}
      title={t("projects.create.title")}
      onClose={close}
      closeLabel={t("common.close")}
      className="max-w-xl"
      footer={
        <>
          <Button variant="ghost" onClick={close}>
            {t("common.cancel")}
          </Button>
          <Button disabled={!canSubmit} onClick={() => createMutation.mutate()}>
            {t("projects.create.submit")}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="create-name">{t("projects.create.name")}</Label>
          <Input
            id="create-name"
            value={name}
            placeholder={t("projects.create.namePlaceholder")}
            onChange={(e) => setName(e.target.value)}
          />
          <p className="text-xs text-muted-foreground">
            {hasRepo ? t("projects.create.nameDerived") : t("projects.create.nameHint")}
          </p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="create-display-name">{t("projects.create.displayName")}</Label>
          <Input
            id="create-display-name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </div>
        <div className="rounded-md border bg-secondary/40 p-3">
          <div className="flex items-center gap-2 text-sm font-medium">
            <GitBranch className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
            {t("projects.create.gitSection")}
          </div>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">
            {t("projects.create.gitHint")}
          </p>
          <div className="mt-3 grid gap-3 sm:grid-cols-[minmax(0,1fr)_140px]">
            <div className="space-y-1.5">
              <Label htmlFor="create-repo">{t("projects.create.repo")}</Label>
              <Input
                id="create-repo"
                value={repo}
                placeholder="git.example.com/team/service"
                className="font-mono"
                onChange={(e) => setRepo(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="create-branch">{t("projects.create.branch")}</Label>
              <Input
                id="create-branch"
                value={branch}
                placeholder="main"
                className="font-mono"
                onChange={(e) => setBranch(e.target.value)}
              />
            </div>
          </div>
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
