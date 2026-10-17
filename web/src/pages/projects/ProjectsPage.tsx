import { useCallback, useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";
import PageShell from "@/components/PageShell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { API_BASE, ApiError, apiFetch, parseErrorBody } from "@/lib/api";
import { tokenStore } from "@/lib/auth";
import { missingRequiredParams } from "@/lib/params";
import { isValidLogDir, nextCopyName, validatePortNames, type EnvIssue } from "@/lib/project-edit";
import { toast } from "@/lib/toast";
import { validatePipeline, validateTree, type TreeIssue } from "@/lib/tree-ops";
import type { MountStatus, Project, Provider, Step, TreeNode } from "@/lib/types";
import EnvEditor from "./EnvEditor";
import LogDirEditor from "./LogDirEditor";
import NodeInspector from "./NodeInspector";
import PipelineEditor from "./PipelineEditor";
import PortsEditor from "./PortsEditor";
import ProjectList from "./ProjectList";
import TestStageEditor from "./TestStageEditor";
import TreeEditor from "./TreeEditor";
import { primarySummary } from "./primarySummary";

interface WarmupResult {
  status: string;
  results: { provider: string; path: string; status: string; error?: string }[];
}

// Export is a binary download, so it bypasses apiFetch (which parses JSON).
// The token travels in the Authorization header, never in the URL.
async function downloadProject(name: string): Promise<void> {
  const headers = new Headers();
  const token = tokenStore.get();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const res = await fetch(`${API_BASE}/projects/${encodeURIComponent(name)}/export`, { headers });
  if (!res.ok) throw await parseErrorBody(res);
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = url;
    a.download = `${name}.tar.gz`;
    a.click();
  } finally {
    URL.revokeObjectURL(url);
  }
}

export default function ProjectsPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();

  // Selection lives in the URL (?sel=name) so palette jumps and shared
  // links land on the same project; applySelect is the only writer.
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedName = searchParams.get("sel");
  const applySelect = (name: string | null) => {
    resetEditor();
    setSearchParams(name ? { sel: name } : {}, { replace: true });
  };
  const [search, setSearch] = useState("");
  // The draft is null until the first edit; draft = edited ?? server copy.
  const [edited, setEdited] = useState<Project | null>(null);
  const [pendingSwitch, setPendingSwitch] = useState<string | null>(null);
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [showParamErrors, setShowParamErrors] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  // Row-level env issues live in EnvEditor (the draft record cannot hold
  // duplicate keys); the editor reports them up so saves can be blocked.
  const [envIssues, setEnvIssues] = useState<EnvIssue[]>([]);
  const handleEnvIssues = useCallback((issues: EnvIssue[]) => {
    setEnvIssues((prev) =>
      prev.length === issues.length &&
      prev.every(
        (p, i) => p.code === issues[i]!.code && p.index === issues[i]!.index && p.key === issues[i]!.key,
      )
        ? prev
        : issues,
    );
  }, []);

  const encName = selectedName === null ? null : encodeURIComponent(selectedName);

  // Mutation callbacks fire after renders, so they compare the project they
  // were started for (their variables) against the live selection via this
  // ref; a callback whose project is no longer selected drops all state
  // updates and toasts.
  const selectedNameRef = useRef<string | null>(selectedName);
  useEffect(() => {
    selectedNameRef.current = selectedName;
  }, [selectedName]);

  const listQuery = useQuery({
    queryKey: ["projects"],
    queryFn: ({ signal }) => apiFetch<{ projects: Project[] }>("/projects", { signal }),
  });
  const detailQuery = useQuery({
    queryKey: ["project", selectedName],
    queryFn: ({ signal }) => apiFetch<Project>(`/projects/${encName}`, { signal }),
    enabled: encName !== null,
  });
  const providersQuery = useQuery({
    queryKey: ["providers"],
    queryFn: ({ signal }) => apiFetch<{ providers: Provider[] }>("/providers", { signal }),
  });
  const warmupQuery = useQuery({
    queryKey: ["warmup", selectedName],
    queryFn: ({ signal }) => apiFetch<{ mounts: MountStatus[] }>(`/projects/${encName}/warmup`, { signal }),
    enabled: encName !== null,
    refetchInterval: (query) =>
      query.state.data?.mounts.some((m) => m.status === "warming") ? 2000 : false,
  });

  const draft = edited ?? detailQuery.data ?? null;
  const dirty = edited !== null;
  const providers = providersQuery.data?.providers ?? [];
  const projects = listQuery.data?.projects ?? [];
  const q = search.trim().toLowerCase();
  const filtered = q
    ? projects.filter((p) =>
        `${p.name} ${p.display_name ?? ""} ${primarySummary(p) ?? ""}`.toLowerCase().includes(q),
      )
    : projects;

  // Drops every piece of selection/detail/in-flight state. Called by the
  // switch handlers, never by an effect.
  const resetEditor = () => {
    setEdited(null);
    setSelectedPath(null);
    setSaveError(null);
    setShowParamErrors(false);
    setConfirmDelete(false);
    setEnvIssues([]);
  };

  const selectProject = (name: string) => {
    if (name === selectedName) return;
    if (dirty) {
      setPendingSwitch(name);
      return;
    }
    applySelect(name);
  };

  const confirmSwitch = () => {
    if (pendingSwitch === null) return;
    applySelect(pendingSwitch);
    setPendingSwitch(null);
  };

  const activePath =
    selectedPath !== null && draft?.tree.some((n) => n.path === selectedPath)
      ? selectedPath
      : (draft?.tree[0]?.path ?? null);
  const activeNode = draft?.tree.find((n) => n.path === activePath) ?? null;

  const statusByPath = Object.fromEntries(
    (warmupQuery.data?.mounts ?? []).map((m) => [m.path, m]),
  );

  const changeTree = (tree: TreeNode[]) => {
    if (draft) setEdited({ ...draft, tree });
  };
  const changePipeline = (pipeline: Step[]) => {
    if (draft) setEdited({ ...draft, pipeline });
  };
  const changeNode = (path: string, node: TreeNode) => {
    if (draft) setEdited({ ...draft, tree: draft.tree.map((n) => (n.path === path ? node : n)) });
  };

  const saveMutation = useMutation({
    mutationFn: (project: Project) =>
      apiFetch<Project>(`/projects/${encodeURIComponent(project.name)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(project),
      }),
    onSuccess: (saved, variables) => {
      // Keyed caches are safe to update regardless of the live selection.
      queryClient.setQueryData(["project", saved.name], saved);
      void queryClient.invalidateQueries({ queryKey: ["projects"] });
      if (selectedNameRef.current !== variables.name) return;
      setEdited(null);
      setSaveError(null);
      setShowParamErrors(false);
      toast(t("projects.editor.saved"));
    },
    onError: (err, variables) => {
      if (selectedNameRef.current !== variables.name) return;
      const detail = err instanceof ApiError ? `${err.code}: ${err.message}` : String(err);
      setSaveError(t("projects.editor.saveFailed", { message: detail }));
    },
  });

  const warmupMutation = useMutation({
    mutationFn: (name: string) =>
      apiFetch<WarmupResult>(`/projects/${encodeURIComponent(name)}/warmup`, { method: "POST" }),
    onSuccess: (res, name) => {
      void queryClient.invalidateQueries({ queryKey: ["warmup", name] });
      if (selectedNameRef.current !== name) return;
      toast(t("projects.editor.warmupResult", { status: res.status }));
    },
    onError: (err, name) => {
      if (selectedNameRef.current !== name) return;
      toast(t("projects.editor.warmupFailed", { message: String(err) }), "error");
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (name: string) =>
      apiFetch<{ status: string }>(`/projects/${encodeURIComponent(name)}`, { method: "DELETE" }),
    onSuccess: (_res, name) => {
      toast(t("projects.editor.deleted"));
      void queryClient.invalidateQueries({ queryKey: ["projects"] });
      if (selectedNameRef.current !== name) return;
      resetEditor();
      setSearchParams({}, { replace: true });
    },
    onError: (err, name) => {
      if (selectedNameRef.current !== name) return;
      setConfirmDelete(false);
      toast(err instanceof Error ? err.message : String(err), "error");
    },
  });

  const duplicateMutation = useMutation({
    mutationFn: ({ from, to }: { from: string; to: string }) =>
      apiFetch<Project>(`/projects/${encodeURIComponent(from)}/duplicate`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: to }),
      }),
    onSuccess: (created, { from }) => {
      toast(t("projects.editor.duplicated", { name: created.name }));
      void queryClient.invalidateQueries({ queryKey: ["projects"] });
      if (selectedNameRef.current !== from) return;
      applySelect(created.name);
    },
    onError: (err, { from }) => {
      if (selectedNameRef.current !== from) return;
      toast(
        t("projects.editor.duplicateFailed", {
          message: err instanceof Error ? err.message : String(err),
        }),
        "error",
      );
    },
  });

  const handleDuplicate = () => {
    if (selectedName === null) return;
    const to = nextCopyName(
      selectedName,
      projects.map((p) => p.name),
    );
    duplicateMutation.mutate({ from: selectedName, to });
  };

  const handleSave = () => {
    if (!draft) return;
    const issues: TreeIssue[] = [...validateTree(draft.tree), ...validatePipeline(draft.pipeline)];
    const missing = draft.tree.flatMap((n) => {
      if (!n.mount) return [];
      const provider = providers.find((p) => p.id === n.mount?.provider);
      return missingRequiredParams(provider?.parameters ?? [], n.mount.params ?? {}).map(
        (id) => `${n.path}: ${id}`,
      );
    });
    const portIssues = validatePortNames(draft.ports ?? []);
    const logDirBad = !isValidLogDir(draft.log_dir ?? "");
    if (
      issues.length > 0 ||
      missing.length > 0 ||
      envIssues.length > 0 ||
      portIssues.length > 0 ||
      logDirBad
    ) {
      setShowParamErrors(true);
      const messages = issues.map((i) =>
        t(`projects.issues.${i.code}`, { path: i.path ?? "" }),
      );
      if (missing.length > 0) {
        messages.push(t("projects.issues.missing_params", { fields: missing.join(", ") }));
      }
      for (const issue of envIssues) {
        messages.push(t(`projects.issues.${issue.code}`, { key: issue.key }));
      }
      for (const issue of portIssues) {
        messages.push(t(`projects.issues.${issue.code}`, { name: issue.name }));
      }
      if (logDirBad) {
        messages.push(t("projects.issues.invalid_log_dir"));
      }
      setSaveError(messages.join(" "));
      return;
    }
    setSaveError(null);
    // A whitespace-only test script means no test stage.
    const payload =
      draft.test && draft.test.run.trim() === "" ? { ...draft, test: undefined } : draft;
    saveMutation.mutate(payload);
  };

  const handleExport = () => {
    if (selectedName === null) return;
    downloadProject(selectedName).catch((err: unknown) => {
      toast(t("projects.editor.exportFailed", { message: String(err) }), "error");
    });
  };

  const handleWarmup = () => {
    if (selectedName === null) return;
    warmupMutation.mutate(selectedName);
  };

  const handleDelete = () => {
    if (selectedName === null) return;
    deleteMutation.mutate(selectedName);
  };

  return (
    <PageShell
      id="projects"
      title={t("pages.projects")}
      count={projects.length}
      onRefresh={() => void listQuery.refetch()}
      refreshing={listQuery.isFetching}
      searchValue={search}
      onSearchChange={setSearch}
      searchPlaceholder={t("projects.searchPlaceholder")}
      sidebar={
        listQuery.isLoading ? (
          <p className="p-2 text-sm text-muted-foreground">{t("common.loading")}</p>
        ) : listQuery.isError ? (
          <p role="alert" className="p-2 text-sm text-destructive">
            {t("projects.loadError")}
          </p>
        ) : (
          <ProjectList projects={filtered} selectedName={selectedName} onSelect={selectProject} />
        )
      }
    >
      {selectedName === null ? (
        <div className="grid h-full min-h-[240px] place-items-center rounded-lg border border-dashed text-sm text-muted-foreground">
          {t("projects.selectHint")}
        </div>
      ) : detailQuery.isLoading ? (
        <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
      ) : detailQuery.isError || !draft ? (
        <p className="text-sm text-destructive">{t("projects.loadError")}</p>
      ) : (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <h2 className="text-xl font-bold tracking-tight">{draft.name}</h2>
            {dirty && <Badge variant="outline">{t("projects.editor.unsaved")}</Badge>}
            <div className="flex items-center gap-2">
              <Label htmlFor="project-display-name" className="text-xs text-muted-foreground">
                {t("projects.editor.displayName")}
              </Label>
              <Input
                id="project-display-name"
                className="h-8 w-52"
                value={draft.display_name ?? ""}
                onChange={(e) => setEdited({ ...draft, display_name: e.target.value })}
              />
            </div>
            <div className="ml-auto flex flex-wrap gap-2">
              <Button size="sm" disabled={saveMutation.isPending} onClick={handleSave}>
                {saveMutation.isPending ? t("projects.editor.saving") : t("projects.editor.save")}
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={warmupMutation.isPending}
                onClick={handleWarmup}
              >
                {warmupMutation.isPending
                  ? t("projects.editor.warming")
                  : t("projects.editor.warmup")}
              </Button>
              <Button size="sm" variant="outline" onClick={handleExport}>
                {t("projects.editor.export")}
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={dirty || duplicateMutation.isPending}
                onClick={handleDuplicate}
              >
                {duplicateMutation.isPending
                  ? t("projects.editor.duplicating")
                  : t("projects.editor.duplicate")}
              </Button>
              {confirmDelete ? (
                <>
                  <Button
                    size="sm"
                    variant="destructive"
                    disabled={deleteMutation.isPending}
                    onClick={handleDelete}
                  >
                    {t("projects.editor.confirmDelete")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(false)}>
                    {t("common.cancel")}
                  </Button>
                </>
              ) : (
                <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>
                  {t("projects.editor.delete")}
                </Button>
              )}
            </div>
          </div>
          {saveError && (
            <p role="alert" className="text-sm whitespace-pre-wrap text-destructive">
              {saveError}
            </p>
          )}
          <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
            <div className="space-y-4">
              <TreeEditor
                nodes={draft.tree}
                selectedPath={activePath}
                statusByPath={statusByPath}
                onSelect={setSelectedPath}
                onChange={changeTree}
              />
              <PipelineEditor steps={draft.pipeline} onChange={changePipeline} />
              {/* Env/ports rows are component-local state, so these cards
                  remount per project selection to reset them. */}
              <EnvEditor
                key={`${selectedName}:env`}
                value={draft.env}
                onChange={(env) => setEdited({ ...draft, env })}
                onIssuesChange={handleEnvIssues}
              />
              <PortsEditor
                key={`${selectedName}:ports`}
                value={draft.ports}
                onChange={(ports) => setEdited({ ...draft, ports })}
              />
              <LogDirEditor
                value={draft.log_dir}
                onChange={(logDir) => setEdited({ ...draft, log_dir: logDir })}
              />
              <TestStageEditor
                key={`${selectedName}:test`}
                value={draft.test}
                onChange={(test) => setEdited({ ...draft, test })}
              />
            </div>
            <div className="sticky top-5">
              {activeNode ? (
                <NodeInspector
                  node={activeNode}
                  providers={providers}
                  status={statusByPath[activeNode.path]}
                  showErrors={showParamErrors}
                  onChange={(node) => changeNode(activeNode.path, node)}
                />
              ) : (
                <div className="rounded-lg border bg-card p-6 text-sm text-muted-foreground">
                  {t("projects.inspector.selectHint")}
                </div>
              )}
            </div>
          </div>
        </div>
      )}
      <Dialog
        open={pendingSwitch !== null}
        title={t("projects.unsaved.title")}
        footer={
          <>
            <Button variant="ghost" onClick={() => setPendingSwitch(null)}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" onClick={confirmSwitch}>
              {t("projects.unsaved.discard")}
            </Button>
          </>
        }
      >
        {t("projects.unsaved.body", { name: selectedName ?? "" })}
      </Dialog>
    </PageShell>
  );
}
