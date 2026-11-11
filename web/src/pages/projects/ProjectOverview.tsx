import { ArrowUpRight, Play } from "lucide-react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import RelativeTime from "@/components/RelativeTime";
import ShellCode from "@/components/ShellCode";
import StatusBadge from "@/components/StatusBadge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { InstanceState, MountStatus, Project, RunRecord } from "@/lib/types";
import { cn } from "@/lib/utils";

interface ProjectOverviewProps {
  project: Project;
  instances: InstanceState[];
  runs: RunRecord[];
  warmup: MountStatus[];
  running: boolean;
  onRun: () => void;
  onEditPipeline: () => void;
}

function formatDuration(run: RunRecord): string {
  const ms = new Date(run.finished_at).getTime() - new Date(run.started_at).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "—";
  const seconds = Math.max(1, Math.round(ms / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  return `${minutes}m ${seconds % 60}s`;
}

function stepStatus(run: RunRecord | undefined, name: string): string | undefined {
  return run?.steps?.find((step) => step.name === name)?.status;
}

export default function ProjectOverview({
  project,
  instances,
  runs,
  warmup,
  running,
  onRun,
  onEditPipeline,
}: ProjectOverviewProps) {
  const { t } = useTranslation();
  const instance = instances.find((i) => i.status === "RUNNING") ?? instances[0];
  const lastRun = runs[0];
  const warm = warmup.filter((mount) => mount.status === "warm").length;
  const allWarm = warmup.length > 0 && warm === warmup.length;
  const instanceID = instance ? `${project.name}/${instance.instance}` : null;

  return (
    <div className="space-y-4">
      <section
        className="grid grid-cols-2 overflow-hidden rounded-md border bg-card sm:grid-cols-4"
        aria-label={t("projects.tabs.overview")}
      >
        <div className="border-r border-b p-4 sm:border-b-0">
          <div className="eyebrow">{t("projects.overview.lastRun")}</div>
          <div className="mt-2 flex min-h-7 items-center gap-2">
            {lastRun ? (
              <>
                <StatusBadge status={lastRun.status} />
                <span className="text-[11px] text-muted-foreground">
                  <RelativeTime ts={lastRun.started_at} />
                </span>
              </>
            ) : (
              <span className="text-sm text-muted-foreground">{t("projects.overview.notRun")}</span>
            )}
          </div>
        </div>
        <div className="border-b p-4 sm:border-r sm:border-b-0">
          <div className="eyebrow">{t("projects.overview.instance")}</div>
          <div className="mt-2 flex min-h-7 items-center gap-2">
            {instance ? (
              <>
                <span className="font-mono text-lg tracking-[-0.02em]">{instance.instance}</span>
                <span className="h-1.5 w-1.5 rounded-full bg-success" aria-hidden="true" />
              </>
            ) : (
              <span className="text-sm text-muted-foreground">—</span>
            )}
          </div>
        </div>
        <div className="border-r p-4">
          <div className="eyebrow">{t("projects.overview.endpoint")}</div>
          <div className="mt-2 flex min-h-7 items-baseline gap-1.5 font-mono text-lg tracking-[-0.02em]">
            {instance?.port ? `:${instance.port}` : "—"}
            {instance?.url && (
              <span className="truncate text-[10.5px] font-normal text-muted-foreground">
                {instance.url.replace(/^https?:\/\//, "").replace(/:\d+$/, "")}
              </span>
            )}
          </div>
        </div>
        <div className="p-4">
          <div className="eyebrow">{t("projects.overview.pipeline")}</div>
          <div className="mt-2 flex min-h-7 items-baseline gap-1.5">
            <span className="font-mono text-lg tracking-[-0.02em]">{project.pipeline.length}</span>
            <span className="text-[11px] text-muted-foreground">
              {t("projects.overview.stepsLabel")}
            </span>
          </div>
        </div>
      </section>

      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1.55fr)_minmax(280px,0.8fr)]">
        <section className="panel min-w-0">
          <div className="flex items-start justify-between gap-3 border-b px-4 py-3.5">
            <div>
              <div className="eyebrow">{t("projects.overview.flowLabel")}</div>
              <h3 className="mt-1 text-[15px] font-semibold tracking-[-0.01em]">
                {t("projects.tabs.pipeline")}
              </h3>
            </div>
            <button
              type="button"
              onClick={onEditPipeline}
              className="text-[12px] font-medium text-muted-foreground hover:text-foreground"
            >
              {t("projects.overview.editPipeline")} →
            </button>
          </div>
          {project.pipeline.length === 0 ? (
            <p className="p-4 text-sm text-muted-foreground">{t("projects.overview.noPipeline")}</p>
          ) : (
            <div className="px-4 pt-1 pb-4">
              {project.pipeline.map((step, index) => {
                const status = stepStatus(lastRun, step.name);
                const preview = step.run.split("\n").slice(0, 3).join("\n");
                return (
                  <div
                    key={`${step.name}-${index}`}
                    className="relative grid grid-cols-[27px_minmax(0,1fr)] gap-3 pt-3.5"
                  >
                    {index < project.pipeline.length - 1 && (
                      <span
                        aria-hidden="true"
                        className="absolute top-7 bottom-[-6px] left-[13px] w-px bg-border"
                      />
                    )}
                    <span
                      className={cn(
                        "relative z-10 grid h-7 w-7 place-items-center rounded-full border bg-secondary font-mono text-[10.5px]",
                        status === "SUCCESS" && "border-success bg-success-bg text-success",
                        status === "FAILED" && "border-danger-border bg-danger-bg text-destructive",
                        !status && "text-muted-foreground",
                      )}
                    >
                      {String(index + 1).padStart(2, "0")}
                    </span>
                    <div className="min-w-0 pb-1">
                      <div className="flex flex-wrap items-baseline gap-2">
                        <strong className="text-[14px] font-semibold">{step.name}</strong>
                        {status ? (
                          <span
                            className={cn(
                              "font-mono text-[11px]",
                              status === "SUCCESS" && "text-success",
                              status === "FAILED" && "text-destructive",
                              status !== "SUCCESS" && status !== "FAILED" && "text-muted-foreground",
                            )}
                          >
                            {t(`status.${status.toLowerCase()}`)}
                          </span>
                        ) : (
                          <span className="font-mono text-[11px] text-muted-foreground">
                            {t("projects.overview.configured")}
                          </span>
                        )}
                        {step.background && (
                          <Badge variant="muted">{t("projects.overview.background")}</Badge>
                        )}
                      </div>
                      <ShellCode
                        code={preview}
                        className="mt-2 max-h-32 rounded-[2px] leading-[1.6]"
                      />
                    </div>
                  </div>
                );
              })}
              <button
                type="button"
                onClick={onEditPipeline}
                className="mt-2 ml-[39px] h-8 w-[calc(100%-39px)] rounded-[2px] border border-dashed border-input text-[12px] font-medium text-muted-foreground hover:border-ring hover:bg-accent hover:text-accent-foreground"
              >
                + {t("projects.tabs.pipeline")}
              </button>
            </div>
          )}
        </section>

        <aside className="space-y-4">
          <section className="panel pb-3">
            <div className="px-4 pt-3.5">
              <div className="eyebrow">{t("projects.overview.runtimeLabel")}</div>
              <h3 className="mt-1 text-[15px] font-semibold tracking-[-0.01em]">
                {instance?.instance ?? "—"}
              </h3>
            </div>
            {instance ? (
              <>
                <div className="flex items-center gap-2 px-4 pt-1.5 font-mono text-[12px]">
                  <span className="h-1.5 w-1.5 rounded-full bg-success" aria-hidden="true" />
                  {t(`status.${instance.status.toLowerCase()}`)}
                  <span className="text-muted-foreground">· :{instance.port ?? "—"}</span>
                </div>
                <dl className="grid grid-cols-[72px_minmax(0,1fr)] gap-x-2 gap-y-1.5 px-4 pt-3 text-[12px]">
                  <dt className="text-[11px] font-medium text-muted-foreground">
                    {t("instances.detail.branch")}
                  </dt>
                  <dd className="truncate font-mono text-[11.5px]">{instance.branch ?? "—"}</dd>
                  <dt className="text-[11px] font-medium text-muted-foreground">
                    {t("instances.detail.commit")}
                  </dt>
                  <dd className="truncate font-mono text-[11.5px]">{instance.commit?.slice(0, 12) ?? "—"}</dd>
                  <dt className="text-[11px] font-medium text-muted-foreground">
                    {t("instances.detail.workspace")}
                  </dt>
                  <dd className="truncate font-mono text-[11.5px]">{instance.workspace ?? "—"}</dd>
                </dl>
                <Link
                  to={`/instances?sel=${encodeURIComponent(instanceID ?? "")}`}
                  className="mx-4 mt-3 flex h-8 items-center justify-center rounded-md border bg-card text-xs font-medium hover:bg-accent"
                >
                  {t("projects.overview.openInstance")}
                  <ArrowUpRight size={13} className="ml-1" aria-hidden="true" />
                </Link>
              </>
            ) : (
              <div className="px-4 pb-1">
                <p className="text-[13px] leading-5 text-muted-foreground">
                  {t("projects.overview.noInstance")}
                </p>
                <Button size="sm" className="mt-2 w-full" disabled={running} onClick={onRun}>
                  <Play size={13} aria-hidden="true" />
                  {t("projects.overview.runAction")}
                </Button>
              </div>
            )}
          </section>

          <section className="panel p-4">
            <div className="flex items-center gap-2">
              <span
                aria-hidden="true"
                className={cn("h-1.5 w-1.5 rounded-full", allWarm ? "bg-success" : "bg-warning")}
              />
              <div className="eyebrow">{t("projects.overview.readiness")}</div>
            </div>
            <p className="mt-1.5 text-[13px] leading-5 text-foreground/80">
              {warmup.length > 0
                ? t("projects.overview.warmCount", { warm, total: warmup.length })
                : t("projects.overview.ready")}
            </p>
            {allWarm && <p className="mt-1 text-[12px] text-success">{t("projects.overview.ready")}</p>}
          </section>
        </aside>
      </div>

      <section className="panel overflow-hidden">
        <div className="flex items-start justify-between gap-3 border-b px-4 py-3.5">
          <div>
            <div className="eyebrow">{t("projects.overview.historyLabel")}</div>
            <h3 className="mt-1 text-[15px] font-semibold tracking-[-0.01em]">
              {t("projects.overview.recentRuns")}
            </h3>
          </div>
          {instanceID && (
            <Link
              to={`/instances?sel=${encodeURIComponent(instanceID)}`}
              className="text-[12px] font-medium text-muted-foreground hover:text-foreground"
            >
              {t("projects.overview.viewAll")} →
            </Link>
          )}
        </div>
        {runs.length === 0 ? (
          <p className="p-4 text-sm text-muted-foreground">{t("projects.overview.noRuns")}</p>
        ) : (
          <div
            className="overflow-x-auto"
            role="region"
            aria-label={t("projects.overview.recentRuns")}
            tabIndex={0}
          >
            <table className="w-full min-w-[620px] border-collapse text-[12.5px]">
              <thead>
                <tr className="border-b">
                  {[
                    t("projects.overview.status"),
                    t("instances.detail.runId"),
                    t("projects.overview.commit"),
                    t("projects.overview.duration"),
                    t("projects.overview.started"),
                  ].map((label) => (
                    <th
                      key={label}
                      className="px-4 py-2.5 text-left text-[11px] font-semibold text-muted-foreground"
                    >
                      {label}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {runs.slice(0, 5).map((run) => (
                  <tr key={run.id} className="border-b last:border-b-0 hover:bg-secondary/40">
                    <td className="px-4 py-2.5">
                      <span className="inline-flex items-center gap-2">
                        <span
                          className={cn(
                            "h-1.5 w-1.5 rounded-full",
                            run.status === "SUCCESS" && "bg-success",
                            run.status === "FAILED" && "bg-destructive",
                            run.status !== "SUCCESS" && run.status !== "FAILED" && "bg-muted-foreground/50",
                          )}
                          aria-hidden="true"
                        />
                        <span className="font-mono text-[11px]">
                          {t(`status.${run.status.toLowerCase()}`)}
                        </span>
                      </span>
                    </td>
                    <td className="px-4 py-2.5 font-mono text-[11.5px]">{run.id}</td>
                    <td className="px-4 py-2.5 font-mono text-[11.5px] text-muted-foreground">
                      {run.commit?.slice(0, 12) ?? "—"}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-[11.5px] text-muted-foreground">
                      {formatDuration(run)}
                    </td>
                    <td className="px-4 py-2.5 font-mono text-[11.5px] text-muted-foreground">
                      <RelativeTime ts={run.started_at} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
