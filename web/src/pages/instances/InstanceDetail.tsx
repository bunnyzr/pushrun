import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  CircleAlert,
  CircleCheck,
  CircleDashed,
  FileText,
  GitBranch,
  Network,
  Play,
  RotateCcw,
  RotateCw,
  Square,
  Terminal,
  Trash2,
} from "lucide-react";
import CopyButton from "@/components/CopyButton";
import EmptyState from "@/components/EmptyState";
import KV from "@/components/KV";
import LogViewer from "@/components/LogViewer";
import RelativeTime from "@/components/RelativeTime";
import StatusBadge from "@/components/StatusBadge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Menu } from "@/components/ui/menu";
import { ApiError, apiFetch, downloadApiFile } from "@/lib/api";
import { streamSSE } from "@/lib/sse";
import { toast } from "@/lib/toast";
import type { InstanceState, LogFileInfo, RunRecord } from "@/lib/types";
import { cn } from "@/lib/utils";
import LogFileTree from "./LogFileTree";

type Action = "run" | "stop" | "restart" | "rerun" | "reset-port";
type Tab = "build" | "business";

const MAX_LOG_LINES = 5000;

function appendLogLines(prev: string[], next: string[]): string[] {
  const merged = [...prev, ...next];
  return merged.length > MAX_LOG_LINES ? merged.slice(-MAX_LOG_LINES) : merged;
}

// RunStatusIcon is the space-light status marker for run rows: the icon
// alone carries the tone, the label rides the tooltip.
function RunStatusIcon({ status }: { status: string }) {
  if (status === "FAILED") {
    return <CircleAlert className="h-4 w-4 text-destructive" aria-hidden="true" />;
  }
  if (status === "SUCCESS") {
    return <CircleCheck className="h-4 w-4 text-success" aria-hidden="true" />;
  }
  return <CircleDashed className="h-4 w-4 text-muted-foreground" aria-hidden="true" />;
}

function runDuration(run: RunRecord): string {
  const ms = new Date(run.finished_at).getTime() - new Date(run.started_at).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "—";
  const seconds = Math.max(1, Math.round(ms / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

function runDay(iso: string, locale: string): string {
  const date = new Date(iso);
  if (!Number.isFinite(date.getTime())) return "—";
  const sameYear = date.getFullYear() === new Date().getFullYear();
  return date.toLocaleDateString(locale, {
    month: "short",
    day: "numeric",
    ...(sameYear ? {} : { year: "numeric" }),
  });
}

function groupRuns(runs: RunRecord[], locale: string): { day: string; runs: RunRecord[] }[] {
  const groups: { day: string; runs: RunRecord[] }[] = [];
  for (const run of runs) {
    const day = runDay(run.started_at, locale);
    const last = groups[groups.length - 1];
    if (last?.day === day) last.runs.push(run);
    else groups.push({ day, runs: [run] });
  }
  return groups;
}

interface ActionResult {
  run_id?: string;
  status?: string;
  port?: number;
  ports?: Record<string, number>;
  url?: string;
}

function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}


interface InstanceDetailProps {
  instance: InstanceState;
  onDeleted: () => void;
}

export default function InstanceDetail({ instance, onDeleted }: InstanceDetailProps) {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const id = `${instance.project}/${instance.instance}`;
  const base = `/instances/${instance.project}/${instance.instance}`;

  const [tab, setTab] = useState<Tab>("build");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [selectedRunId, setSelectedRunId] = useState<string | null>(instance.run_id ?? null);
  const [buildLines, setBuildLines] = useState<string[]>([]);
  const [buildStatus, setBuildStatus] = useState<string | null>(null);
  const [buildTruncated, setBuildTruncated] = useState(false);
  const [selectedLogFile, setSelectedLogFile] = useState<string | null>(null);
  const [follow, setFollow] = useState(false);
  const [logLines, setLogLines] = useState<string[]>([]);
  const [logTruncated, setLogTruncated] = useState(false);
  const [logLoading, setLogLoading] = useState(false);
  const [fileSearch, setFileSearch] = useState("");
  const [tailLines, setTailLines] = useState(500);
  const [pendingDeleteRun, setPendingDeleteRun] = useState<string | null>(null);
  const [pendingDeleteFile, setPendingDeleteFile] = useState<string | null>(null);
  const businessViewerRef = useRef<HTMLDivElement>(null);

  const selectRun = (runId: string | null) => {
    setSelectedRunId(runId);
    setBuildLines([]);
    setBuildStatus(null);
    setBuildTruncated(false);
  };
  const selectLogFile = (file: string | null) => {
    setSelectedLogFile(file);
    setLogLines([]);
    setLogTruncated(false);
    setLogLoading(file !== null);
    // On a narrow screen the file tree sits above the viewer; bring the
    // selected log into view instead of leaving it below the fold.
    if (
      file &&
      typeof window.matchMedia === "function" &&
      window.matchMedia("(max-width: 640px)").matches
    ) {
      window.requestAnimationFrame(() => {
        businessViewerRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
      });
    }
  };
  const toggleFollow = () => {
    setFollow((v) => !v);
    setLogLines([]);
    setLogTruncated(false);
    setLogLoading(true);
  };
  const changeTail = (next: number) => {
    setTailLines(next);
    setLogLines([]);
    setLogTruncated(false);
    setLogLoading(true);
  };

  const runsQuery = useQuery({
    queryKey: ["runs", instance.project, instance.instance],
    queryFn: ({ signal }) => apiFetch<{ runs: RunRecord[] }>(`${base}/runs`, { signal }),
  });
  const treeQuery = useQuery({
    queryKey: ["logs-tree", instance.project, instance.instance],
    queryFn: ({ signal }) => apiFetch<{ files: LogFileInfo[] }>(`${base}/logs/tree`, { signal }),
  });

  // Streams the selected run's build log. The cleanup aborts the stream when
  // the run changes, the instance switches (this component remounts per
  // instance), or the page unmounts. Line state is cleared by selectRun, not
  // here — synchronous setState in an effect is a lint error.
  useEffect(() => {
    if (!selectedRunId) return;
    const controller = new AbortController();
    streamSSE(`/runs/${selectedRunId}/output`, {
      signal: controller.signal,
      onEvent: (ev) => {
        if (ev.event === "log") {
          setBuildLines((prev) => {
            const next = appendLogLines(prev, ev.data);
            if (next.length !== prev.length + ev.data.length) setBuildTruncated(true);
            return next;
          });
        } else if (ev.event === "status") {
          try {
            const payload = JSON.parse(ev.data[0] ?? "{}") as { status?: string };
            setBuildStatus(payload.status ?? null);
          } catch {
            setBuildStatus(null);
          }
          // A finished run may have written business logs; refresh the tree.
          void queryClient.invalidateQueries({
            queryKey: ["logs-tree", instance.project, instance.instance],
          });
        }
      },
    }).catch((err: unknown) => {
      if (!isAbortError(err)) {
        toast(err instanceof Error ? err.message : String(err), "error");
      }
    });
    return () => controller.abort();
  }, [instance.project, instance.instance, queryClient, selectedRunId]);

  // Loads the selected business-log file, or follows it over SSE when the
  // follow toggle is on. The cleanup aborts either form on switch/unmount.
  // Line state is cleared by the selection handlers, not here (a synchronous
  // setState in an effect is a lint error).
  useEffect(() => {
    if (!selectedLogFile) return;
    const controller = new AbortController();
    const tail = tailLines > 0 ? `&tail_lines=${tailLines}` : "";
    const query = `path=${encodeURIComponent(selectedLogFile)}`;
    if (follow) {
      streamSSE(`${base}/logs/file?${query}${tail}&follow=1`, {
        signal: controller.signal,
        onEvent: (ev) => {
          if (ev.event === "log") {
            setLogLoading(false);
            setLogLines((prev) => {
              const next = appendLogLines(prev, ev.data);
              if (next.length !== prev.length + ev.data.length) setLogTruncated(true);
              return next;
            });
          }
        },
      })
        .catch((err: unknown) => {
          if (!isAbortError(err)) {
            toast(err instanceof Error ? err.message : String(err), "error");
          }
        })
        .finally(() => {
          if (!controller.signal.aborted) setLogLoading(false);
        });
    } else {
      apiFetch<{ path: string; content: string }>(`${base}/logs/file?${query}${tail}`, {
        signal: controller.signal,
      })
        .then((res) => {
          const all = res.content === "" ? [] : res.content.replace(/\n$/, "").split("\n");
          if (all.length > MAX_LOG_LINES) setLogTruncated(true);
          setLogLines(all.length > MAX_LOG_LINES ? all.slice(-MAX_LOG_LINES) : all);
        })
        .catch((err: unknown) => {
          if (!isAbortError(err)) {
            toast(err instanceof Error ? err.message : String(err), "error");
          }
        })
        .finally(() => {
          if (!controller.signal.aborted) setLogLoading(false);
        });
    }
    return () => controller.abort();
  }, [
    base,
    instance.project,
    instance.instance,
    queryClient,
    selectedLogFile,
    follow,
    tailLines,
  ]);

  const actionMutation = useMutation({
    mutationFn: (action: Action) => apiFetch<ActionResult>(`${base}/${action}`, { method: "POST" }),
    onSuccess: (res, action) => {
      const actionKey = action === "reset-port" ? "resetPort" : action;
      toast(
        t("instances.toast.actionDone", {
          action: t(`instances.actions.${actionKey}`),
          status: res.status ?? "?",
        }),
      );
      // Follow the just-triggered run in the build log immediately instead
      // of waiting for the user to pick it from the history.
      if (res.run_id) {
        setTab("build");
        selectRun(res.run_id);
      }
      void queryClient.invalidateQueries({ queryKey: ["instances"] });
      void queryClient.invalidateQueries({
        queryKey: ["runs", instance.project, instance.instance],
      });
      void queryClient.invalidateQueries({
        queryKey: ["logs-tree", instance.project, instance.instance],
      });
    },
    onError: (err) => {
      if (err instanceof ApiError && err.code === "busy") {
        toast(t("instances.toast.busy"), "error");
      } else {
        toast(err instanceof Error ? err.message : String(err), "error");
      }
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () => apiFetch<{ status: string }>(base, { method: "DELETE" }),
    onSuccess: () => {
      toast(t("instances.toast.deleted"));
      void queryClient.invalidateQueries({ queryKey: ["instances"] });
      onDeleted();
    },
    onError: (err) => {
      setConfirmDelete(false);
      if (err instanceof ApiError && err.code === "busy") {
        toast(t("instances.toast.busy"), "error");
      } else {
        toast(err instanceof Error ? err.message : String(err), "error");
      }
    },
  });

  const deleteRunMutation = useMutation({
    mutationFn: (runId: string) => apiFetch<{ status: string }>(`/runs/${runId}`, {
      method: "DELETE",
    }),
    onSuccess: (_res, runId) => {
      toast(t("instances.toast.runDeleted"));
      void queryClient.invalidateQueries({
        queryKey: ["runs", instance.project, instance.instance],
      });
      if (selectedRunId === runId) selectRun(null);
    },
    onError: (err) => {
      toast(err instanceof Error ? err.message : String(err), "error");
    },
  });

  const deleteLogFileMutation = useMutation({
    mutationFn: (path: string) =>
      apiFetch<{ status: string }>(`${base}/logs/file?path=${encodeURIComponent(path)}`, {
        method: "DELETE",
      }),
    onSuccess: (_res, path) => {
      toast(t("instances.toast.logFileDeleted"));
      void queryClient.invalidateQueries({
        queryKey: ["logs-tree", instance.project, instance.instance],
      });
      if (selectedLogFile === path) selectLogFile(null);
    },
    onError: (err) => {
      toast(err instanceof Error ? err.message : String(err), "error");
    },
  });

  const runs = runsQuery.data?.runs ?? [];
  const runGroups = groupRuns(runs, i18n.language);
  const files = treeQuery.data?.files ?? [];
  const running = instance.status === "RUNNING";

  return (
    <div className="space-y-4">
      <div>
        <header className="flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0">
            <div className="eyebrow">{t("instances.detail.eyebrow")}</div>
            <div className="mt-2 flex flex-wrap items-center gap-2.5">
              <h2 className="truncate font-mono text-[22px] font-semibold tracking-[-0.02em]">
                {id}
              </h2>
              <StatusBadge status={instance.status} />
            </div>
            <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[12px] text-muted-foreground">
              {instance.branch && (
                <span className="inline-flex items-center gap-1">
                  <GitBranch className="h-3 w-3" aria-hidden="true" />
                  {instance.branch}
                </span>
              )}
              <span>
                {t("instances.updated")} <RelativeTime ts={instance.updated_at} />
              </span>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              size="sm"
              variant={running ? "destructive" : "default"}
              disabled={actionMutation.isPending || deleteMutation.isPending}
              onClick={() => actionMutation.mutate(running ? "stop" : "run")}
            >
              {running ? (
                <Square className="h-3.5 w-3.5 fill-current" aria-hidden="true" />
              ) : (
                <Play className="h-4 w-4" aria-hidden="true" />
              )}
              {running ? t("instances.actions.stop") : t("instances.actions.run")}
            </Button>
            <Button
              size="sm"
              variant="outline"
              title={t("instances.actions.restartHint")}
              disabled={actionMutation.isPending || deleteMutation.isPending}
              onClick={() => actionMutation.mutate("restart")}
            >
              <RotateCw className="h-3.5 w-3.5" aria-hidden="true" />
              {t("instances.actions.restart")}
            </Button>
            <Menu
              label={t("instances.actions.more")}
              items={[
                {
                  key: "rerun",
                  label: t("instances.actions.rerun"),
                  icon: <RotateCcw size={14} aria-hidden="true" />,
                  disabled: actionMutation.isPending || deleteMutation.isPending,
                  onSelect: () => actionMutation.mutate("rerun"),
                },
                {
                  key: "reset-port",
                  label: t("instances.actions.resetPort"),
                  icon: <Network size={14} aria-hidden="true" />,
                  disabled: actionMutation.isPending || deleteMutation.isPending,
                  onSelect: () => actionMutation.mutate("reset-port"),
                },
                {
                  key: "delete",
                  label: t("instances.actions.delete"),
                  icon: <Trash2 size={14} aria-hidden="true" />,
                  danger: true,
                  disabled: deleteMutation.isPending,
                  onSelect: () => setConfirmDelete(true),
                },
              ]}
            />
          </div>
        </header>

        <div className="mt-4 flex flex-wrap overflow-hidden rounded-md border bg-card">
          <div className="min-w-[150px] flex-1 basis-1/4 border-r border-b border-border/60 px-4 py-3.5">
            <KV label={t("instances.detail.branch")}>
              {instance.branch && <span className="truncate">{instance.branch}</span>}
            </KV>
          </div>
          <div className="min-w-[150px] flex-1 basis-1/4 border-r border-b border-border/60 px-4 py-3.5">
            <KV label={t("instances.detail.port")}>
              {typeof instance.port === "number" && instance.port > 0 && (
                <span className="font-mono">:{instance.port}</span>
              )}
            </KV>
          </div>
          <div className="min-w-[150px] flex-1 basis-1/4 border-r border-b border-border/60 px-4 py-3.5">
            <KV label={t("instances.detail.commit")}>
              {instance.commit && (
                <>
                  <span className="font-mono">{instance.commit.slice(0, 12)}</span>
                  <CopyButton text={instance.commit} />
                </>
              )}
            </KV>
          </div>
          <div className="min-w-[150px] flex-1 basis-1/4 border-b border-border/60 px-4 py-3.5">
            <KV label={t("instances.detail.runId")}>
              {instance.run_id && (
                <>
                  <span className="truncate font-mono">{instance.run_id}</span>
                  <CopyButton text={instance.run_id} />
                </>
              )}
            </KV>
          </div>
          <div className="min-w-[150px] flex-1 basis-1/4 border-r border-b border-border/60 px-4 py-3.5 sm:border-b-0">
            <KV label={t("instances.detail.serviceUrl")}>
              {instance.url && (
                <>
                  <a
                    href={instance.url}
                    target="_blank"
                    rel="noreferrer"
                    className="truncate underline decoration-line-strong underline-offset-2 hover:decoration-ring"
                  >
                    {instance.url}
                  </a>
                  <CopyButton text={instance.url} />
                </>
              )}
            </KV>
          </div>
          {Object.keys(instance.ports ?? {}).length > 0 && (
            <div className="min-w-[150px] flex-1 basis-1/4 border-r border-border/60 px-4 py-3.5 sm:border-b-0">
              <KV label={t("instances.detail.ports")}>
                <span className="flex flex-col items-start gap-0.5">
                  {Object.entries(instance.ports ?? {}).map(([name, port]) => (
                    <span key={name} className="flex items-center gap-1">
                      <span className="font-mono text-[12px]">
                        {name} → :{port}
                      </span>
                      <CopyButton text={String(port)} />
                    </span>
                  ))}
                </span>
              </KV>
            </div>
          )}
          {instance.workspace && (
            <div className="min-w-[150px] flex-1 basis-1/4 border-r border-border/60 px-4 py-3.5 sm:border-b-0">
              <KV label={t("instances.detail.workspace")}>
                <span className="truncate font-mono text-[12px]">{instance.workspace}</span>
                <CopyButton text={instance.workspace} />
              </KV>
            </div>
          )}
        </div>

        {(instance.members ?? []).length > 0 && (
          <section className="panel mt-4 overflow-hidden">
            <div className="border-b px-4 py-3.5">
              <div className="flex items-center gap-1.5">
                <Network className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />
                <div className="eyebrow">{t("instances.detail.members")}</div>
              </div>
              <p className="mt-1 text-[12px] text-muted-foreground">
                {t("instances.detail.membersDescription")}
              </p>
            </div>
            <div
              className="overflow-x-auto"
              role="region"
              aria-label={t("instances.detail.members")}
              tabIndex={0}
            >
              <table className="w-full min-w-[640px] border-collapse text-[12.5px]">
                <thead>
                  <tr className="border-b text-left">
                    {[
                      t("instances.detail.memberPath"),
                      t("instances.detail.memberRepo"),
                      t("instances.detail.memberBranch"),
                      t("instances.detail.memberCommit"),
                    ].map((label) => (
                      <th
                        key={label}
                        className="px-4 py-2.5 text-[11.5px] font-semibold text-muted-foreground"
                      >
                        {label}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {(instance.members ?? []).map((member) => (
                    <tr key={member.path} className="border-b last:border-b-0 hover:bg-secondary/60">
                      <td className="px-4 py-3 font-mono text-[12px]">{member.path}</td>
                      <td
                        className="max-w-[320px] truncate px-4 py-3 font-mono text-[12px] text-muted-foreground"
                        title={member.repo}
                      >
                        {member.repo || "—"}
                      </td>
                      <td className="px-4 py-3 font-mono text-[12px] text-muted-foreground">
                        {member.branch || "—"}
                      </td>
                      <td className="px-4 py-3">
                        {member.commit ? (
                          <span className="flex items-center gap-1 font-mono text-[12px]">
                            {member.commit.slice(0, 12)}
                            <CopyButton text={member.commit} />
                          </span>
                        ) : (
                          "—"
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        )}
      </div>

      <div
        className="flex gap-1 overflow-x-auto border-b"
        role="tablist"
        aria-label={t("instances.detail.logs")}
      >
        {(["build", "business"] as const).map((key) => {
          const Icon = key === "build" ? Terminal : FileText;
          return (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={tab === key}
              onClick={() => setTab(key)}
              className={cn(
                "-mb-px inline-flex items-center gap-1.5 border-b-2 border-transparent px-2.5 py-2.5 text-[13px] font-medium text-muted-foreground hover:text-foreground sm:px-3 sm:text-sm",
                tab === key && "border-ring text-foreground",
              )}
            >
              <Icon className="h-4 w-4 max-sm:hidden" aria-hidden="true" />
              {t(key === "build" ? "instances.detail.buildLog" : "instances.detail.businessLogs")}
            </button>
          );
        })}
      </div>

      {tab === "build" && (
        <section role="region" aria-label={t("instances.detail.buildLog")}>
          {runs.length === 0 && !selectedRunId ? (
            <div className="panel">
              <EmptyState
                compact
                icon={<Terminal className="h-5 w-5" aria-hidden="true" />}
                title={t("instances.detail.noRuns")}
                description={t("instances.detail.noRunsDescription")}
              />
            </div>
          ) : (
            <div className="flex flex-col overflow-hidden rounded-md border bg-card xl:flex-row xl:items-stretch">
              {/* The history sidebar collapses when the only run is one we
                  just triggered (history not yet refetched). */}
              {runs.length > 0 && (
                <div className="w-full flex-none border-b bg-secondary/40 xl:w-[258px] xl:border-r xl:border-b-0">
                  <div className="flex items-center justify-between border-b px-3 py-2.5">
                    <span className="eyebrow">{t("instances.detail.runHistory")}</span>
                    <span className="font-mono text-[10.5px] text-muted-foreground">
                      {runs.length}
                    </span>
                  </div>
                  <div className="max-h-56 overflow-y-auto xl:max-h-[520px]">
                    {runGroups.map((group) => (
                      <div key={group.day}>
                        <div className="sticky top-0 z-10 bg-secondary/95 px-3 py-1.5 text-[11px] font-semibold text-muted-foreground backdrop-blur">
                          {group.day}
                        </div>
                        {group.runs.map((run) => (
                          <div
                            key={run.id}
                            className={cn(
                              "group flex items-center border-l-2",
                              selectedRunId === run.id
                                ? "border-primary bg-accent"
                                : "border-transparent hover:bg-secondary/70",
                            )}
                          >
                            <button
                              type="button"
                              onClick={() => selectRun(run.id)}
                              className="flex min-w-0 flex-1 items-center gap-2 px-2.5 py-2 text-left"
                            >
                              <span
                                title={t(`status.${run.status.toLowerCase()}`)}
                                className="flex-none"
                              >
                                <RunStatusIcon status={run.status} />
                              </span>
                              <span className="min-w-0 flex-1">
                                <span className="block truncate font-mono text-[12px]">
                                  {run.id}
                                </span>
                                <span className="mt-0.5 flex items-center gap-1.5 font-mono text-[10.5px] text-muted-foreground">
                                  <RelativeTime ts={run.started_at} />
                                  <span aria-hidden="true">·</span>
                                  {runDuration(run)}
                                </span>
                              </span>
                            </button>
                            <button
                              type="button"
                              aria-label={`${t("instances.detail.deleteRun")} ${run.id}`}
                              disabled={deleteRunMutation.isPending}
                              onClick={() => setPendingDeleteRun(run.id)}
                              className="mr-1 flex-none rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-secondary hover:text-destructive focus-visible:opacity-100 group-hover:opacity-100 max-sm:opacity-100"
                            >
                              <Trash2 className="h-3.5 w-3.5" />
                            </button>
                          </div>
                        ))}
                      </div>
                    ))}
                  </div>
                </div>
              )}
              <div className="min-w-0 flex-1">
                {!selectedRunId ? (
                  <div className="grid min-h-[320px] place-items-center sm:min-h-[466px]">
                    <EmptyState
                      compact
                      icon={<Terminal className="h-5 w-5" aria-hidden="true" />}
                      title={t("instances.detail.selectRunTitle")}
                      description={t("instances.detail.selectRun")}
                    />
                  </div>
                ) : (
                  <LogViewer
                    bare
                    lines={buildLines}
                    status={buildStatus}
                    title={t("instances.detail.buildLogTitle", { id: selectedRunId })}
                    downloadName={`${selectedRunId}.log`}
                    onDownload={() =>
                      downloadApiFile(
                        `/runs/${encodeURIComponent(selectedRunId!)}/output?download=1`,
                        `${selectedRunId}.log`,
                      ).catch((err: unknown) =>
                        toast(err instanceof Error ? err.message : String(err), "error"),
                      )
                    }
                    notice={
                      buildTruncated ? t("logViewer.truncated", { count: MAX_LOG_LINES }) : undefined
                    }
                  />
                )}
              </div>
            </div>
          )}
        </section>
      )}

      {tab === "business" && (
        <section role="region" aria-label={t("instances.detail.businessLogs")} className="space-y-3">
          {files.length === 0 ? (
            <div className="panel">
              <EmptyState
                compact
                icon={<FileText className="h-5 w-5" aria-hidden="true" />}
                title={t("instances.detail.noLogFiles")}
                description={t("instances.detail.noLogFilesDescription")}
              />
            </div>
          ) : (
            <div className="flex flex-col overflow-hidden rounded-md border bg-card xl:flex-row">
              <LogFileTree
                files={files}
                selectedFile={selectedLogFile}
                filter={fileSearch}
                onFilterChange={setFileSearch}
                onSelect={selectLogFile}
                onDelete={setPendingDeleteFile}
              />
              <div ref={businessViewerRef} className="min-w-0 flex-1 scroll-mt-20">
                {selectedLogFile ? (
                  <LogViewer
                    bare
                    lines={logLines}
                    title={selectedLogFile}
                    follow={{ active: follow, onToggle: toggleFollow }}
                    tail={{ value: tailLines, onChange: changeTail }}
                    emptyText={
                      logLoading ? t("instances.detail.loadingLog") : t("logViewer.empty")
                    }
                    downloadName={selectedLogFile.split("/").pop() ?? "log.txt"}
                    onDownload={() =>
                      downloadApiFile(
                        `${base}/logs/file?path=${encodeURIComponent(selectedLogFile)}&download=1`,
                        selectedLogFile.split("/").pop() ?? "log.txt",
                      ).catch((err: unknown) =>
                        toast(err instanceof Error ? err.message : String(err), "error"),
                      )
                    }
                    notice={
                      logTruncated ? t("logViewer.truncated", { count: MAX_LOG_LINES }) : undefined
                    }
                  />
                ) : (
                  <div className="grid min-h-[320px] place-items-center sm:min-h-[466px]">
                    <EmptyState
                      compact
                      icon={<FileText className="h-5 w-5" aria-hidden="true" />}
                      title={t("instances.detail.noFileSelectedTitle")}
                      description={t("instances.detail.selectLogFile")}
                    />
                  </div>
                )}
              </div>
            </div>
          )}
        </section>
      )}

      <Dialog
        open={confirmDelete}
        title={t("instances.detail.deleteInstanceConfirm.title")}
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
              onClick={() => deleteMutation.mutate()}
            >
              {deleteMutation.isPending
                ? t("instances.actions.deleting")
                : t("instances.actions.delete")}
            </Button>
          </>
        }
      >
        {t("instances.detail.deleteInstanceConfirm.body", { id })}
      </Dialog>

      <Dialog
        open={pendingDeleteRun !== null}
        title={t("instances.detail.deleteRunConfirm.title")}
        onClose={() => setPendingDeleteRun(null)}
        closeLabel={t("common.close")}
        footer={
          <>
            <Button variant="ghost" onClick={() => setPendingDeleteRun(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="destructive"
              disabled={deleteRunMutation.isPending}
              onClick={() => {
                if (pendingDeleteRun) deleteRunMutation.mutate(pendingDeleteRun);
                setPendingDeleteRun(null);
              }}
            >
              {t("instances.detail.deleteRun")}
            </Button>
          </>
        }
      >
        {t("instances.detail.deleteRunConfirm.body", { id: pendingDeleteRun ?? "" })}
      </Dialog>

      <Dialog
        open={pendingDeleteFile !== null}
        title={t("instances.detail.deleteLogFileConfirm.title")}
        onClose={() => setPendingDeleteFile(null)}
        closeLabel={t("common.close")}
        footer={
          <>
            <Button variant="ghost" onClick={() => setPendingDeleteFile(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="destructive"
              disabled={deleteLogFileMutation.isPending}
              onClick={() => {
                if (pendingDeleteFile) deleteLogFileMutation.mutate(pendingDeleteFile);
                setPendingDeleteFile(null);
              }}
            >
              {t("instances.detail.deleteLogFile")}
            </Button>
          </>
        }
      >
        {t("instances.detail.deleteLogFileConfirm.body", { file: pendingDeleteFile ?? "" })}
      </Dialog>
    </div>
  );
}
