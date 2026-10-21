import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { CircleAlert, CircleCheck, CircleDashed, FileText, Trash2 } from "lucide-react";
import CopyButton from "@/components/CopyButton";
import KV from "@/components/KV";
import LogViewer from "@/components/LogViewer";
import RelativeTime from "@/components/RelativeTime";
import StatusBadge from "@/components/StatusBadge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { ApiError, apiFetch } from "@/lib/api";
import { streamSSE } from "@/lib/sse";
import { toast } from "@/lib/toast";
import type { InstanceState, LogFileInfo, RunRecord } from "@/lib/types";
import { formatSize } from "@/lib/format";
import { cn } from "@/lib/utils";

type Action = "run" | "stop" | "restart" | "rerun";
type Tab = "build" | "business";

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
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const id = `${instance.project}/${instance.instance}`;
  const base = `/instances/${instance.project}/${instance.instance}`;

  const [tab, setTab] = useState<Tab>("build");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [selectedRunId, setSelectedRunId] = useState<string | null>(instance.run_id ?? null);
  const [buildLines, setBuildLines] = useState<string[]>([]);
  const [buildStatus, setBuildStatus] = useState<string | null>(null);
  const [selectedLogFile, setSelectedLogFile] = useState<string | null>(null);
  const [follow, setFollow] = useState(false);
  const [logLines, setLogLines] = useState<string[]>([]);
  const [fileSearch, setFileSearch] = useState("");
  const [tailLines, setTailLines] = useState(500);
  const [pendingDeleteRun, setPendingDeleteRun] = useState<string | null>(null);
  const [pendingDeleteFile, setPendingDeleteFile] = useState<string | null>(null);

  const selectRun = (runId: string | null) => {
    setSelectedRunId(runId);
    setBuildLines([]);
    setBuildStatus(null);
  };
  const selectLogFile = (file: string | null) => {
    setSelectedLogFile(file);
    setLogLines([]);
  };
  const toggleFollow = () => {
    setFollow((v) => !v);
    setLogLines([]);
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
          setBuildLines((prev) => [...prev, ...ev.data]);
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
  }, [selectedRunId]);

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
            setLogLines((prev) => [...prev, ...ev.data]);
          }
        },
      }).catch((err: unknown) => {
        if (!isAbortError(err)) {
          toast(err instanceof Error ? err.message : String(err), "error");
        }
      });
    } else {
      apiFetch<{ path: string; content: string }>(`${base}/logs/file?${query}${tail}`, {
        signal: controller.signal,
      })
        .then((res) => {
          setLogLines(res.content === "" ? [] : res.content.replace(/\n$/, "").split("\n"));
        })
        .catch((err: unknown) => {
          if (!isAbortError(err)) {
            toast(err instanceof Error ? err.message : String(err), "error");
          }
        });
    }
    return () => controller.abort();
  }, [base, selectedLogFile, follow, tailLines]);

  const actionMutation = useMutation({
    mutationFn: (action: Action) => apiFetch<ActionResult>(`${base}/${action}`, { method: "POST" }),
    onSuccess: (res, action) => {
      toast(
        t("instances.toast.actionDone", {
          action: t(`instances.actions.${action}`),
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
  const files = treeQuery.data?.files ?? [];
  const running = instance.status === "RUNNING";

  return (
    <div className="space-y-5">
      <div>
        <div className="flex flex-wrap items-center gap-3">
          <h2 className="text-xl font-bold tracking-tight">{id}</h2>
          <StatusBadge status={instance.status} />
          <span className="ml-auto flex items-center gap-1.5 text-xs text-muted-foreground">
            {t("instances.updated")}
            <RelativeTime ts={instance.updated_at} />
          </span>
        </div>

        <div className="mt-4 grid grid-cols-2 gap-x-6 gap-y-3 rounded-lg border bg-card p-4 sm:grid-cols-4">
          <KV label={t("instances.detail.branch")}>
            {instance.branch && <span className="truncate">{instance.branch}</span>}
          </KV>
          <KV label={t("instances.detail.port")}>
            {typeof instance.port === "number" && instance.port > 0 && (
              <span className="font-mono">:{instance.port}</span>
            )}
          </KV>
          <KV label={t("instances.detail.commit")}>
            {instance.commit && (
              <>
                <span className="font-mono">{instance.commit.slice(0, 12)}</span>
                <CopyButton text={instance.commit} />
              </>
            )}
          </KV>
          <KV label={t("instances.detail.runId")}>
            {instance.run_id && (
              <>
                <span className="truncate font-mono">{instance.run_id}</span>
                <CopyButton text={instance.run_id} />
              </>
            )}
          </KV>
          <KV label={t("instances.detail.serviceUrl")}>
            {instance.url && (
              <>
                <a
                  href={instance.url}
                  target="_blank"
                  rel="noreferrer"
                  className="truncate text-primary hover:underline"
                >
                  {instance.url}
                </a>
                <CopyButton text={instance.url} />
              </>
            )}
          </KV>
          {Object.keys(instance.ports ?? {}).length > 0 && (
            <KV label={t("instances.detail.ports")}>
              <span className="flex flex-col items-start gap-0.5">
                {Object.entries(instance.ports ?? {}).map(([name, port]) => (
                  <span key={name} className="flex items-center gap-1">
                    <span className="font-mono text-xs">
                      {name} → :{port}
                    </span>
                    <CopyButton text={String(port)} />
                  </span>
                ))}
              </span>
            </KV>
          )}
        </div>

        <div className="mt-3 flex flex-wrap gap-2">
          <Button
            size="sm"
            variant={running ? "destructive" : "default"}
            disabled={actionMutation.isPending || deleteMutation.isPending}
            onClick={() => actionMutation.mutate(running ? "stop" : "run")}
          >
            {running ? t("instances.actions.stop") : t("instances.actions.run")}
          </Button>
          <Button
            size="sm"
            variant="outline"
            title={t("instances.actions.rerunHint")}
            disabled={actionMutation.isPending || deleteMutation.isPending}
            onClick={() => actionMutation.mutate("rerun")}
          >
            {t("instances.actions.rerun")}
          </Button>
          <Button
            size="sm"
            variant="outline"
            title={t("instances.actions.restartHint")}
            disabled={actionMutation.isPending || deleteMutation.isPending}
            onClick={() => actionMutation.mutate("restart")}
          >
            {t("instances.actions.restart")}
          </Button>
          <div className="ml-auto flex gap-2">
            {confirmDelete ? (
              <>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={deleteMutation.isPending}
                  onClick={() => deleteMutation.mutate()}
                >
                  {t("instances.actions.confirmDelete")}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(false)}>
                  {t("common.cancel")}
                </Button>
              </>
            ) : (
              <Button size="sm" variant="destructive" onClick={() => setConfirmDelete(true)}>
                {t("instances.actions.delete")}
              </Button>
            )}
          </div>
        </div>
      </div>

      <div className="flex gap-1 border-b">
        {(["build", "business"] as const).map((key) => (
          <button
            key={key}
            type="button"
            onClick={() => setTab(key)}
            className={cn(
              "-mb-px border-b-2 border-transparent px-3 py-2 text-sm font-medium text-muted-foreground hover:text-foreground",
              tab === key && "border-primary text-primary-hover",
            )}
          >
            {t(key === "build" ? "instances.detail.buildLog" : "instances.detail.businessLogs")}
          </button>
        ))}
      </div>

      {tab === "build" && (
        <section role="region" aria-label={t("instances.detail.buildLog")}>
          {runs.length === 0 && !selectedRunId ? (
            <p className="text-sm text-muted-foreground">{t("instances.detail.noRuns")}</p>
          ) : (
            <div className="flex items-start overflow-hidden rounded-md border bg-card">
              {/* The history sidebar collapses when the only run is one we
                  just triggered (history not yet refetched). */}
              {runs.length > 0 && (
              <div className="w-64 flex-none border-r">
                <div className="max-h-[460px] divide-y divide-border/70 overflow-y-auto">
                  {runs.map((run) => (
                    <div key={run.id} className="group flex items-center">
                      <button
                        type="button"
                        onClick={() => selectRun(run.id)}
                        className={cn(
                          "flex min-w-0 flex-1 items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-secondary",
                          selectedRunId === run.id && "bg-accent",
                        )}
                      >
                        <span title={t(`status.${run.status.toLowerCase()}`)} className="flex-none">
                          <RunStatusIcon status={run.status} />
                        </span>
                        <span className="truncate font-mono text-xs">{run.id}</span>
                        <RelativeTime ts={run.started_at} className="ml-auto flex-none text-xs" />
                      </button>
                      <button
                        type="button"
                        aria-label={`${t("instances.detail.deleteRun")} ${run.id}`}
                        disabled={deleteRunMutation.isPending}
                        onClick={() => setPendingDeleteRun(run.id)}
                        className="ml-0.5 flex-none rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-secondary hover:text-destructive focus-visible:opacity-100 group-hover:opacity-100"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  ))}
                </div>
              </div>
              )}
              <div className="min-w-0 flex-1">
                {!selectedRunId ? (
                  <div className="grid h-[460px] place-items-center rounded-md border border-dashed text-sm text-muted-foreground">
                    {t("instances.detail.selectRun")}
                  </div>
                ) : (
                  <LogViewer
                    bare
                    lines={buildLines}
                    status={buildStatus}
                    downloadName={`${selectedRunId}.log`}
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
            <p className="text-sm text-muted-foreground">{t("instances.detail.noLogFiles")}</p>
          ) : (
            <div className="flex overflow-hidden rounded-md border bg-card">
              <div className="flex w-52 flex-none flex-col border-r bg-card">
                <div className="border-b p-2">
                  <input
                    value={fileSearch}
                    onChange={(e) => setFileSearch(e.target.value)}
                    placeholder={t("instances.detail.filterFiles")}
                    className="h-7 w-full rounded border bg-background px-2 text-xs outline-none placeholder:text-muted-foreground"
                  />
                </div>
                <div className="max-h-[466px] divide-y divide-border/70 overflow-y-auto">
                  {files
                    .filter((f) =>
                      f.path.toLowerCase().includes(fileSearch.trim().toLowerCase()),
                    )
                    .map((file) => (
                      <div
                        key={file.path}
                        className={cn(
                          "group flex items-center gap-1.5 px-2 py-1.5",
                          selectedLogFile === file.path && "bg-accent",
                        )}
                      >
                        <FileText
                          className="h-3.5 w-3.5 flex-none text-muted-foreground"
                          aria-hidden="true"
                        />
                        <button
                          type="button"
                          onClick={() => selectLogFile(file.path)}
                          className={cn(
                            "min-w-0 flex-1 truncate rounded text-left font-mono text-xs hover:text-foreground",
                            selectedLogFile === file.path
                              ? "font-semibold text-accent-foreground"
                              : "text-muted-foreground",
                          )}
                        >
                          {file.path}
                        </button>
                        <span className="flex-none text-[10px] text-muted-foreground/70">
                          {formatSize(file.size)}
                        </span>
                        <button
                          type="button"
                          aria-label={`${t("instances.detail.deleteLogFile")} ${file.path}`}
                          disabled={deleteLogFileMutation.isPending}
                          onClick={() => setPendingDeleteFile(file.path)}
                          className="flex-none rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-secondary hover:text-destructive focus-visible:opacity-100 group-hover:opacity-100"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </button>
                      </div>
                    ))}
                </div>
              </div>
              <div className="min-w-0 flex-1">
                {selectedLogFile ? (
                  <LogViewer
                    bare
                    lines={logLines}
                    follow={{ active: follow, onToggle: toggleFollow }}
                    tail={{ value: tailLines, onChange: setTailLines }}
                    downloadName={selectedLogFile.split("/").pop() ?? "log.txt"}
                  />
                ) : (
                  <div className="grid h-[466px] place-items-center text-sm text-terminal-muted">
                    {t("instances.detail.selectLogFile")}
                  </div>
                )}
              </div>
            </div>
          )}
        </section>
      )}

      <Dialog
        open={pendingDeleteRun !== null}
        title={t("instances.detail.deleteRunConfirm.title")}
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
