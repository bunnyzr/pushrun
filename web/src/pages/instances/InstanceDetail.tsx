import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import LogViewer from "@/components/LogViewer";
import StatusBadge from "@/components/StatusBadge";
import { Button } from "@/components/ui/button";
import { ApiError, apiFetch } from "@/lib/api";
import { streamSSE } from "@/lib/sse";
import { toast } from "@/lib/toast";
import type { InstanceState, RunRecord } from "@/lib/types";
import { cn } from "@/lib/utils";

const ACTIONS = ["run", "stop", "restart", "rerun"] as const;
type Action = (typeof ACTIONS)[number];

interface ActionResult {
  run_id?: string;
  status?: string;
  port?: number;
  url?: string;
}

function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}

function formatTime(ts?: string): string {
  if (!ts) return "";
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? ts : d.toLocaleString();
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

  const [confirmDelete, setConfirmDelete] = useState(false);
  const [selectedRunId, setSelectedRunId] = useState<string | null>(instance.run_id ?? null);
  const [buildLines, setBuildLines] = useState<string[]>([]);
  const [buildStatus, setBuildStatus] = useState<string | null>(null);
  const [selectedLogFile, setSelectedLogFile] = useState<string | null>(null);
  const [follow, setFollow] = useState(false);
  const [logLines, setLogLines] = useState<string[]>([]);

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
    queryFn: ({ signal }) => apiFetch<{ files: string[] }>(`${base}/logs/tree`, { signal }),
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
    const query = `path=${encodeURIComponent(selectedLogFile)}`;
    if (follow) {
      streamSSE(`${base}/logs/file?${query}&tail_lines=200&follow=1`, {
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
      apiFetch<{ path: string; content: string }>(`${base}/logs/file?${query}&tail_lines=500`, {
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
  }, [base, selectedLogFile, follow]);

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
      if (res.run_id) selectRun(res.run_id);
      void queryClient.invalidateQueries({ queryKey: ["instances"] });
      void queryClient.invalidateQueries({
        queryKey: ["runs", instance.project, instance.instance],
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

  const runs = runsQuery.data?.runs ?? [];
  const files = treeQuery.data?.files ?? [];

  return (
    <div className="space-y-6">
      <div className="rounded-lg border p-4">
        <div className="flex flex-wrap items-center gap-3">
          <h2 className="text-lg font-semibold">{id}</h2>
          <StatusBadge status={instance.status} />
          <div className="ml-auto flex flex-wrap gap-2">
            {ACTIONS.map((action) => (
              <Button
                key={action}
                size="sm"
                variant={action === "stop" ? "secondary" : "outline"}
                disabled={actionMutation.isPending || deleteMutation.isPending}
                onClick={() => actionMutation.mutate(action)}
              >
                {t(`instances.actions.${action}`)}
              </Button>
            ))}
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
        <div className="mt-2 flex flex-wrap items-center gap-x-4 text-xs text-muted-foreground">
          {instance.branch && <span>{instance.branch}</span>}
          {instance.commit && <span className="font-mono">{instance.commit.slice(0, 7)}</span>}
          {typeof instance.port === "number" && instance.port > 0 && <span>:{instance.port}</span>}
          {instance.url && (
            <a href={instance.url} target="_blank" rel="noreferrer" className="text-primary underline">
              {instance.url}
            </a>
          )}
          <span>{t("instances.updatedAt", { time: formatTime(instance.updated_at) })}</span>
        </div>
      </div>

      <section aria-label={t("instances.detail.runHistory")}>
        <h3 className="mb-2 text-sm font-semibold">{t("instances.detail.runHistory")}</h3>
        {runs.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("instances.detail.noRuns")}</p>
        ) : (
          <div className="space-y-1">
            {runs.map((run) => (
              <button
                key={run.id}
                type="button"
                onClick={() => selectRun(run.id)}
                className={cn(
                  "flex w-full items-center gap-3 rounded-md border px-3 py-2 text-left text-sm hover:bg-accent/50",
                  selectedRunId === run.id && "border-primary bg-accent/40",
                )}
              >
                <span className="font-mono">{run.id}</span>
                <StatusBadge status={run.status} />
                {run.branch && <span className="text-xs text-muted-foreground">{run.branch}</span>}
                <span className="ml-auto text-xs text-muted-foreground">
                  {formatTime(run.started_at)}
                </span>
              </button>
            ))}
          </div>
        )}
      </section>

      <section aria-label={t("instances.detail.buildLog")}>
        <h3 className="mb-2 text-sm font-semibold">{t("instances.detail.buildLog")}</h3>
        {!selectedRunId ? (
          <p className="text-sm text-muted-foreground">{t("instances.detail.selectRun")}</p>
        ) : (
          <LogViewer
            lines={buildLines}
            status={buildStatus}
            downloadName={`${selectedRunId}.log`}
          />
        )}
      </section>

      <section aria-label={t("instances.detail.businessLogs")}>
        <div className="mb-2 flex items-center gap-2">
          <h3 className="text-sm font-semibold">{t("instances.detail.businessLogs")}</h3>
          {selectedLogFile && (
            <Button
              size="sm"
              variant={follow ? "secondary" : "outline"}
              aria-pressed={follow}
              onClick={toggleFollow}
            >
              {t("instances.detail.follow")}
            </Button>
          )}
        </div>
        {files.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("instances.detail.noLogFiles")}</p>
        ) : (
          <div className="mb-2 flex flex-wrap gap-1">
            {files.map((file) => (
              <button
                key={file}
                type="button"
                onClick={() => selectLogFile(file)}
                className={cn(
                  "rounded border px-2 py-1 font-mono text-xs hover:bg-accent",
                  selectedLogFile === file && "border-primary bg-accent",
                )}
              >
                {file}
              </button>
            ))}
          </div>
        )}
        {selectedLogFile && (
          <LogViewer
            lines={logLines}
            downloadName={selectedLogFile.split("/").pop() ?? "log.txt"}
          />
        )}
      </section>
    </div>
  );
}
