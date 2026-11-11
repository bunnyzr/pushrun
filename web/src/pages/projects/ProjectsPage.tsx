import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useBlocker, useSearchParams } from "react-router";
import {
  Copy,
  Download,
  FolderGit2,
  FolderTree,
  LayoutDashboard,
  Play,
  Settings2,
  Trash2,
  Workflow,
} from "lucide-react";
import EmptyState from "@/components/EmptyState";
import PageShell from "@/components/PageShell";
import SectionCard from "@/components/SectionCard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Menu } from "@/components/ui/menu";
import { API_BASE, ApiError, apiFetch, parseErrorBody } from "@/lib/api";
import { tokenStore } from "@/lib/auth";
import { missingRequiredParams } from "@/lib/params";
import { useIsMobile } from "@/lib/useMediaQuery";
import { isValidLogDir, nextCopyName, validatePortNames, type EnvIssue } from "@/lib/project-edit";
import { toast } from "@/lib/toast";
import { cn } from "@/lib/utils";
import { validatePipeline, validateTree, type TreeIssue } from "@/lib/tree-ops";
import type {
  InstanceState,
  MountStatus,
  Project,
  Provider,
  RunRecord,
  Step,
  TreeNode,
} from "@/lib/types";
import CreateProjectDialog from "./CreateProjectDialog";
import EnvEditor from "./EnvEditor";
import EnvHelpDialog from "./EnvHelpDialog";
import LogDirEditor from "./LogDirEditor";
import NodeInspector from "./NodeInspector";
import PipelineEditor from "./PipelineEditor";
import PortsEditor from "./PortsEditor";
import ProjectList from "./ProjectList";
import ProjectOverview from "./ProjectOverview";
import TestStageEditor from "./TestStageEditor";
import TreeEditor from "./TreeEditor";
import { primarySummary } from "./primarySummary";

interface WarmupResult {
  status: string;
  results: { provider: string; path: string; status: string; error?: string }[];
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// Export is a binary download, so it bypasses apiFetch (which parses JSON).
// The token travels in the Authorization header, never in the URL.
async function downloadArchive(path: string, filename: string): Promise<void> {
  const headers = new Headers();
  const token = tokenStore.get();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const res = await fetch(`${API_BASE}${path}`, { headers });
  if (!res.ok) throw await parseErrorBody(res);
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.click();
  } finally {
    URL.revokeObjectURL(url);
  }
}

function downloadProject(name: string): Promise<void> {
  return downloadArchive(`/projects/${encodeURIComponent(name)}/export`, `${name}.tar.gz`);
}

function downloadBundle(): Promise<void> {
  return downloadArchive("/bundle/export", "pushrun-bundle.tar.gz");
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
    setImportError(null);
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
  const [createOpen, setCreateOpen] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);
  const [detailTab, setDetailTab] = useState<"overview" | "structure" | "pipeline" | "settings">(
    "overview",
  );
  const [envHelpOpen, setEnvHelpOpen] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const bundleFileRef = useRef<HTMLInputElement>(null);
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
  const instancesQuery = useQuery({
    queryKey: ["instances"],
    queryFn: ({ signal }) => apiFetch<{ instances: InstanceState[] }>("/instances", { signal }),
    refetchInterval: 5000,
  });
  const allInstances = useMemo(
    () => instancesQuery.data?.instances ?? [],
    [instancesQuery.data],
  );
  const projectInstances = useMemo(
    () => (selectedName ? allInstances.filter((i) => i.project === selectedName) : []),
    [allInstances, selectedName],
  );
  const overviewInstance =
    projectInstances.find((i) => i.status === "RUNNING") ?? projectInstances[0];
  const runsQuery = useQuery({
    queryKey: ["runs", selectedName, overviewInstance?.instance],
    queryFn: ({ signal }) =>
      apiFetch<{ runs: RunRecord[] }>(
        `/instances/${encodeURIComponent(selectedName ?? "")}/${encodeURIComponent(overviewInstance?.instance ?? "")}/runs`,
        { signal },
      ),
    enabled: selectedName !== null && overviewInstance !== undefined,
  });
  const statusByName = useMemo(() => {
    const next: Record<string, string | undefined> = {};
    for (const project of listQuery.data?.projects ?? []) {
      const instances = allInstances.filter((i) => i.project === project.name);
      next[project.name] =
        instances.find((i) => i.status === "RUNNING")?.status ??
        instances.find((i) => i.status === "FAILED")?.status ??
        instances[0]?.status;
    }
    return next;
  }, [allInstances, listQuery.data]);

  const draft = edited ?? detailQuery.data ?? null;
  const dirty = edited !== null;
  const providers = providersQuery.data?.providers ?? [];
  const projects = useMemo(() => listQuery.data?.projects ?? [], [listQuery.data]);
  const isMobile = useIsMobile();

  // Desktop lands on the first project; mobile keeps the list until the user
  // picks one. A dirty draft is never replaced.
  useEffect(() => {
    if (isMobile || dirty || listQuery.isLoading || selectedName) return;
    if (projects.length > 0) {
      setSearchParams({ sel: projects[0]!.name }, { replace: true });
    }
  }, [dirty, isMobile, listQuery.isLoading, projects, selectedName, setSearchParams]);

  // Losing a draft through the top navigation is as destructive as switching
  // projects, so both paths share the same confirmation.
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      dirty && currentLocation.pathname !== nextLocation.pathname,
  );
  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);

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
      applySelect(res.project.name);
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
  const bundleImportMutation = useMutation({
    mutationFn: (file: File) =>
      apiFetch<{ status: string; providers: string[]; projects: string[] }>("/bundle/import", {
        method: "POST",
        headers: { "Content-Type": "application/gzip" },
        body: file,
      }),
    onSuccess: (res) => {
      setImportError(null);
      toast(
        t("projects.bundleImported", {
          providers: res.providers.length,
          projects: res.projects.length,
        }),
      );
      void queryClient.invalidateQueries({ queryKey: ["projects"] });
      void queryClient.invalidateQueries({ queryKey: ["project"] });
      void queryClient.invalidateQueries({ queryKey: ["providers"] });
      void queryClient.invalidateQueries({ queryKey: ["warmup"] });
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === "missing_providers") {
        const raw = err.extra?.missing_providers;
        const missing = Array.isArray(raw) ? raw.join(", ") : "";
        setImportError(t("projects.missingProviders", { providers: missing }));
      } else {
        setImportError(t("projects.bundleImportFailed", { message: errorMessage(err) }));
      }
    },
  });
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
    setDetailTab("structure");
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
      : null;
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

  const runMutation = useMutation({
    mutationFn: ({ project, instance }: { project: string; instance: string }) =>
      apiFetch<{ status?: string; port?: number; url?: string }>(
        `/instances/${encodeURIComponent(project)}/${encodeURIComponent(instance)}/run`,
        { method: "POST" },
      ),
    onSuccess: (res, { project }) => {
      toast(t("projects.editor.runStarted", { status: res.status ?? "SUCCESS" }));
      void queryClient.invalidateQueries({ queryKey: ["instances"] });
      void queryClient.invalidateQueries({ queryKey: ["runs", project] });
      void queryClient.invalidateQueries({ queryKey: ["warmup", project] });
    },
    onError: (err, { project }) => {
      if (selectedNameRef.current !== project) return;
      toast(
        t("projects.editor.runFailed", {
          message: err instanceof Error ? err.message : String(err),
        }),
        "error",
      );
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (name: string) =>
      apiFetch<{ status: string }>(`/projects/${encodeURIComponent(name)}`, { method: "DELETE" }),
    onSuccess: (_res, name) => {
      setConfirmDelete(false);
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

  const handleBundleExport = () => {
    setImportError(null);
    downloadBundle().catch((err: unknown) => {
      toast(t("projects.bundleExportFailed", { message: String(err) }), "error");
    });
  };

  const handleWarmup = () => {
    if (selectedName === null) return;
    warmupMutation.mutate(selectedName);
  };

  const handleRun = () => {
    if (selectedName === null) return;
    runMutation.mutate({
      project: selectedName,
      instance: overviewInstance?.instance ?? "default",
    });
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
      detailActive={selectedName !== null}
      onBack={() => applySelect(null)}
      sidebar={
        listQuery.isLoading ? (
          <p className="p-2 text-sm text-muted-foreground">{t("common.loading")}</p>
        ) : listQuery.isError ? (
          <p role="alert" className="p-2 text-sm text-destructive">
            {t("projects.loadError")}
          </p>
        ) : (
          <ProjectList
            projects={filtered}
            selectedName={selectedName}
            importing={importMutation.isPending}
            bundleImporting={bundleImportMutation.isPending}
            statusByName={statusByName}
            onSelect={selectProject}
            onCreate={() => setCreateOpen(true)}
            onImport={() => fileRef.current?.click()}
            onImportBundle={() => {
              setImportError(null);
              bundleFileRef.current?.click();
            }}
            onExportBundle={handleBundleExport}
          />
        )
      }
    >
      {importError && (
        <p role="alert" className="mb-3 rounded-md border border-danger-border bg-danger-bg px-3 py-2 text-sm text-destructive">
          {importError}
        </p>
      )}
      {selectedName === null ? (
        listQuery.isError ? (
          <p role="alert" className="text-sm text-destructive">
            {t("projects.loadError")}
          </p>
        ) : projects.length === 0 && !listQuery.isLoading ? (
          <EmptyState
            icon={<FolderGit2 className="h-5 w-5" aria-hidden="true" />}
            title={t("projects.emptyTitle")}
            description={t("projects.emptyDescription")}
            action={
              <>
                <Button onClick={() => setCreateOpen(true)}>{t("projects.new")}</Button>
                <Button variant="outline" onClick={() => fileRef.current?.click()}>
                  {t("projects.import")}
                </Button>
              </>
            }
          />
        ) : (
          <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
        )
      ) : detailQuery.isLoading ? (
        <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
      ) : detailQuery.isError || !draft ? (
        <p className="text-sm text-destructive">{t("projects.loadError")}</p>
      ) : (
        <div className="space-y-4">
          <div>
            <div className="eyebrow">
              {t("projects.editor.eyebrow")} / {draft.name}
            </div>
            <div className="mt-2 flex flex-wrap items-start justify-between gap-4">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2.5">
                  <h2 className="truncate text-[24px] font-semibold tracking-[-0.02em]">
                    {draft.display_name || draft.name}
                  </h2>
                  {dirty && <Badge variant="warning">{t("projects.editor.unsaved")}</Badge>}
                </div>
                <div className="mt-2 truncate font-mono text-[12px] text-muted-foreground">
                  {draft.name}
                  {primarySummary(draft) ? ` · ${primarySummary(draft)}` : ""}
                </div>
              </div>
              <div className="flex flex-wrap items-center justify-end gap-2">
                {dirty ? (
                  <Button size="sm" disabled={saveMutation.isPending} onClick={handleSave}>
                    {saveMutation.isPending
                      ? t("projects.editor.saving")
                      : t("projects.editor.save")}
                  </Button>
                ) : (
                  <Button
                    size="sm"
                    disabled={runMutation.isPending}
                    onClick={handleRun}
                  >
                    <Play className="h-4 w-4" aria-hidden="true" />
                    {runMutation.isPending
                      ? t("projects.editor.running")
                      : t("projects.editor.run")}
                  </Button>
                )}
                <Button
                  size="sm"
                  variant="outline"
                  disabled={dirty || warmupMutation.isPending}
                  onClick={handleWarmup}
                >
                  {warmupMutation.isPending
                    ? t("projects.editor.warming")
                    : t("projects.editor.warmup")}
                </Button>
                <Menu
                  label={t("projects.moreActions")}
                  items={[
                    {
                      key: "export",
                      label: t("projects.editor.export"),
                      icon: <Download size={14} aria-hidden="true" />,
                      onSelect: handleExport,
                    },
                    {
                      key: "duplicate",
                      label: duplicateMutation.isPending
                        ? t("projects.editor.duplicating")
                        : t("projects.editor.duplicate"),
                      icon: <Copy size={14} aria-hidden="true" />,
                      disabled: dirty || duplicateMutation.isPending,
                      onSelect: handleDuplicate,
                    },
                    {
                      key: "delete",
                      label: t("projects.editor.delete"),
                      icon: <Trash2 size={14} aria-hidden="true" />,
                      danger: true,
                      onSelect: () => setConfirmDelete(true),
                    },
                  ]}
                />
              </div>
            </div>
          </div>
          {saveError && (
            <p
              role="alert"
              className="rounded-md border border-danger-border bg-danger-bg px-3 py-2 text-sm whitespace-pre-wrap text-destructive"
            >
              {saveError}
            </p>
          )}
          <div
            className="flex gap-1 overflow-x-auto border-b"
            role="tablist"
            aria-label={t("projects.tabs.label")}
          >
            {(
              [
                ["overview", LayoutDashboard],
                ["structure", FolderTree],
                ["pipeline", Workflow],
                ["settings", Settings2],
              ] as const
            ).map(([tab, Icon]) => (
              <button
                key={tab}
                type="button"
                role="tab"
                aria-selected={detailTab === tab}
                onClick={() => setDetailTab(tab)}
                className={cn(
                  "-mb-px inline-flex items-center gap-1.5 border-b-2 border-transparent px-2.5 py-2.5 text-[13px] font-medium text-muted-foreground hover:text-foreground sm:px-3 sm:text-sm",
                  detailTab === tab && "border-ring text-foreground",
                )}
              >
                <Icon className="h-4 w-4 max-sm:hidden" aria-hidden="true" />
                {t(`projects.tabs.${tab}`)}
              </button>
            ))}
          </div>
          {detailTab === "overview" && (
            <ProjectOverview
              project={draft}
              instances={projectInstances}
              runs={runsQuery.data?.runs ?? []}
              warmup={warmupQuery.data?.mounts ?? []}
              running={runMutation.isPending}
              onRun={handleRun}
              onEditPipeline={() => setDetailTab("pipeline")}
            />
          )}

          {detailTab === "pipeline" && (
            <PipelineEditor
              steps={draft.pipeline}
              onChange={changePipeline}
              onShowEnvHelp={() => setEnvHelpOpen(true)}
            />
          )}

          {detailTab === "settings" && (
            <div className="grid items-start gap-4 xl:grid-cols-2">
              <SectionCard
                title={t("projects.editor.infoTitle")}
                description={t("projects.editor.infoDescription")}
              >
                <div className="grid gap-3 sm:grid-cols-2">
                  <div className="space-y-1.5">
                    <Label htmlFor="project-name">{t("projects.editor.identifier")}</Label>
                    <Input id="project-name" value={draft.name} disabled className="font-mono" />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="project-display-name">
                      {t("projects.editor.displayName")}
                    </Label>
                    <Input
                      id="project-display-name"
                      value={draft.display_name ?? ""}
                      onChange={(e) => setEdited({ ...draft, display_name: e.target.value })}
                    />
                  </div>
                </div>
              </SectionCard>
              <LogDirEditor
                value={draft.log_dir}
                onChange={(logDir) => setEdited({ ...draft, log_dir: logDir })}
              />
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
              <div className="lg:col-span-2">
                <TestStageEditor
                  key={`${selectedName}:test`}
                  value={draft.test}
                  onChange={(test) => setEdited({ ...draft, test })}
                />
              </div>
            </div>
          )}

          {detailTab === "structure" && (
          <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1.55fr)_minmax(320px,1fr)]">
            <div className="space-y-4">
              <TreeEditor
                nodes={draft.tree}
                selectedPath={activePath}
                statusByPath={statusByPath}
                onSelect={setSelectedPath}
                onChange={changeTree}
              />
            </div>
            <div className="lg:sticky lg:top-6">
              {activeNode ? (
                <NodeInspector
                  node={activeNode}
                  providers={providers}
                  status={statusByPath[activeNode.path]}
                  showErrors={showParamErrors}
                  onChange={(node) => changeNode(activeNode.path, node)}
                />
              ) : (
                <SectionCard title={t("projects.inspector.title")}>
                  <p className="text-sm text-muted-foreground">
                    {t("projects.inspector.selectHint")}
                  </p>
                </SectionCard>
              )}
            </div>
          </div>
          )}
        </div>
      )}
      <EnvHelpDialog open={envHelpOpen} onClose={() => setEnvHelpOpen(false)} />
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
      <input
        ref={bundleFileRef}
        type="file"
        accept=".tar.gz,.tgz,application/gzip"
        aria-label={t("projects.bundleImport")}
        className="hidden"
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = "";
          if (file) bundleImportMutation.mutate(file);
        }}
      />
      <CreateProjectDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={(name) => applySelect(name)}
      />
      <Dialog
        open={confirmDelete}
        title={t("projects.editor.deleteTitle")}
        onClose={() => setConfirmDelete(false)}
        closeLabel={t("common.close")}
        footer={
          <>
            <Button variant="ghost" onClick={() => setConfirmDelete(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="destructive"
              disabled={deleteMutation.isPending}
              onClick={handleDelete}
            >
              {deleteMutation.isPending
                ? t("projects.editor.deleting")
                : t("projects.editor.confirmDelete")}
            </Button>
          </>
        }
      >
        {t("projects.editor.deleteBody", { name: selectedName ?? "" })}
      </Dialog>
      <Dialog
        open={pendingSwitch !== null}
        title={t("projects.unsaved.title")}
        onClose={() => setPendingSwitch(null)}
        closeLabel={t("common.close")}
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
      <Dialog
        open={blocker.state === "blocked"}
        title={t("projects.unsaved.title")}
        onClose={() => blocker.reset?.()}
        closeLabel={t("common.close")}
        footer={
          <>
            <Button variant="ghost" onClick={() => blocker.reset?.()}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" onClick={() => blocker.proceed?.()}>
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
