import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Download, Pencil, Trash2 } from "lucide-react";
import CopyButton from "@/components/CopyButton";
import ParamForm from "@/components/ParamForm";
import RelativeTime from "@/components/RelativeTime";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Menu } from "@/components/ui/menu";
import { API_BASE, ApiError, apiFetch, parseErrorBody } from "@/lib/api";
import { tokenStore } from "@/lib/auth";
import { missingRequiredParams } from "@/lib/params";
import { toast } from "@/lib/toast";
import type { Provider } from "@/lib/types";
import LifecycleBadge from "./LifecycleBadge";
import { lifecycleStatus } from "./lifecycle";

interface WarmupResponse {
  provider: string;
  status: string;
  error?: string;
}

function formatTime(ts: string, locale: string): string {
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? ts : d.toLocaleString(locale);
}

function detail(err: unknown): string {
  return err instanceof ApiError ? `${err.code}: ${err.message}` : String(err);
}

// Export is a binary download, so it bypasses apiFetch (which parses JSON).
// The token travels in the Authorization header, never in the URL.
async function downloadProvider(id: string): Promise<void> {
  const headers = new Headers();
  const token = tokenStore.get();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const res = await fetch(`${API_BASE}/providers/${encodeURIComponent(id)}/export`, { headers });
  if (!res.ok) throw await parseErrorBody(res);
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = url;
    a.download = `${id}.tar.gz`;
    a.click();
  } finally {
    URL.revokeObjectURL(url);
  }
}

export interface ProviderDetailProps {
  provider: Provider;
  onEdit: (provider: Provider) => void;
  onDeleted: () => void;
}

export default function ProviderDetail({ provider, onEdit, onDeleted }: ProviderDetailProps) {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [referencedBy, setReferencedBy] = useState<string[] | null>(null);
  const [warmError, setWarmError] = useState<string | null>(null);
  const [showWarmErrors, setShowWarmErrors] = useState(false);
  const [warmValues, setWarmValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      (provider.parameters ?? [])
        .filter((p) => p.scope === "warmup" && p.default !== undefined)
        .map((p) => [p.id, p.default as string]),
    ),
  );

  const status = lifecycleStatus(provider);
  const warmups = provider.warmups ?? [];
  const latest = warmups
    .filter((w) => !w.warming)
    .map((w) => w.warmed_at)
    .sort()
    .at(-1);
  const warmupParams = (provider.parameters ?? []).filter((p) => p.scope === "warmup");
  const declaredParams = provider.parameters ?? [];

  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ["providers"] });

  const warmupMutation = useMutation({
    mutationFn: () =>
      apiFetch<WarmupResponse>(`/providers/${encodeURIComponent(provider.id)}/warmup`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ params: warmValues }),
      }),
    onSuccess: (res) => {
      invalidate();
      // Warmup failure is reported on a 200 response, not as an HTTP error.
      if (res.status === "FAILED") {
        const message = res.error ?? res.status;
        setWarmError(t("providers.card.warmupFailed", { message }));
        toast(t("providers.card.warmupFailed", { message }), "error");
        return;
      }
      setWarmError(null);
      toast(t("providers.card.warmupSuccess", { status: res.status }));
    },
    onError: (err) => {
      const message = detail(err);
      setWarmError(t("providers.card.warmupFailed", { message }));
      toast(t("providers.card.warmupFailed", { message }), "error");
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () =>
      apiFetch<{ status: string }>(`/providers/${encodeURIComponent(provider.id)}`, {
        method: "DELETE",
      }),
    onSuccess: () => {
      toast(t("providers.card.deleted", { id: provider.id }));
      invalidate();
      onDeleted();
    },
    onError: (err) => {
      setConfirmDelete(false);
      if (err instanceof ApiError && Array.isArray(err.extra?.referenced_by)) {
        setReferencedBy(err.extra.referenced_by as string[]);
        return;
      }
      toast(t("providers.card.deleteFailed", { message: detail(err) }), "error");
    },
  });

  const handleWarmup = () => {
    const missing = missingRequiredParams(warmupParams, warmValues);
    if (missing.length > 0) {
      setShowWarmErrors(true);
      setWarmError(t("providers.card.warmupBlocked", { fields: missing.join(", ") }));
      return;
    }
    setShowWarmErrors(false);
    setWarmError(null);
    warmupMutation.mutate();
  };

  const handleExport = () => {
    downloadProvider(provider.id).catch((err: unknown) => {
      toast(t("providers.card.exportFailed", { message: detail(err) }), "error");
    });
  };

  return (
    <section data-testid={`provider-card-${provider.id}`} className="space-y-4">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="eyebrow">{t("providers.card.eyebrow")}</div>
          <div className="mt-2 flex flex-wrap items-center gap-2.5">
            <h2 className="truncate text-[22px] font-semibold tracking-[-0.02em]">
              {provider.name}
            </h2>
            <LifecycleBadge status={status} />
          </div>
          <div className="mt-1.5 flex flex-wrap items-center gap-2 font-mono text-[12px] text-muted-foreground">
            <span>{provider.id}</span>
            {status === "warm" && latest && (
              <span>
                · {t("providers.card.warmed")} <RelativeTime ts={latest} />
              </span>
            )}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={() => onEdit(provider)}>
            <Pencil className="h-3.5 w-3.5" aria-hidden="true" />
            {t("providers.card.edit")}
          </Button>
          <Button size="sm" variant="outline" onClick={handleExport}>
            <Download className="h-3.5 w-3.5" aria-hidden="true" />
            {t("providers.card.export")}
          </Button>
          <Menu
            label={t("providers.card.actions")}
            items={[
              {
                key: "delete",
                label: t("providers.card.delete"),
                icon: <Trash2 size={14} aria-hidden="true" />,
                danger: true,
                onSelect: () => {
                  setReferencedBy(null);
                  setConfirmDelete(true);
                },
              },
            ]}
          />
        </div>
      </header>

      {provider.description && (
        <p className="max-w-3xl text-[14px] leading-6 text-muted-foreground">
          {provider.description}
        </p>
      )}
      {referencedBy && (
        <p
          role="alert"
          className="rounded-md border border-danger-border bg-danger-bg px-3 py-2 text-sm text-destructive"
        >
          {t("providers.card.referencedBy", { projects: referencedBy.join(", ") })}
        </p>
      )}

      <div className="flex flex-wrap overflow-hidden rounded-md border bg-card">
        {[
          {
            label: t("providers.card.warmupLabel"),
            value: provider.warmup
              ? t("providers.card.supported")
              : t("providers.card.notSupported"),
            muted: !provider.warmup,
          },
          {
            label: t("providers.card.installLabel"),
            value: provider.install
              ? t("providers.card.supported")
              : t("providers.card.notSupported"),
            muted: !provider.install,
          },
          {
            label: t("providers.card.paramsTitle"),
            value: t("providers.card.paramCount", { count: declaredParams.length }),
            muted: declaredParams.length === 0,
          },
        ].map((cell) => (
          <div
            key={cell.label}
            className="min-w-[150px] flex-1 basis-1/2 border-r border-b border-border/60 px-4 py-3.5 last:border-r-0 sm:basis-1/3 sm:border-b-0"
          >
            <div className="eyebrow">{cell.label}</div>
            <div
              className={
                cell.muted
                  ? "mt-1.5 text-sm font-medium text-muted-foreground"
                  : "mt-1.5 text-sm font-semibold"
              }
            >
              {cell.value}
            </div>
          </div>
        ))}
      </div>

      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1.25fr)_minmax(300px,0.75fr)]">
        <div className="space-y-4">
          <section className="panel overflow-hidden">
            <div className="border-b px-4 py-3.5">
              <div className="eyebrow">{t("providers.card.paramsTitle")}</div>
            </div>
            {declaredParams.length === 0 ? (
              <p className="p-4 text-sm text-muted-foreground">{t("providers.card.noParams")}</p>
            ) : (
              <div
                className="overflow-x-auto"
                role="region"
                aria-label={t("providers.card.paramsTitle")}
                tabIndex={0}
              >
                <table className="w-full min-w-[520px] border-collapse text-[12.5px]">
                  <thead>
                    <tr className="border-b text-left">
                      {[
                        t("providers.editor.paramID"),
                        t("providers.editor.paramLabel"),
                        t("providers.editor.paramScope"),
                        t("providers.editor.paramType"),
                        t("providers.card.paramFlags"),
                      ].map((label) => (
                        <th
                          key={label}
                          className="px-4 py-2.5 text-[11px] font-semibold text-muted-foreground"
                        >
                          {label}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {declaredParams.map((p) => (
                      <tr key={p.id} className="border-b last:border-b-0 hover:bg-secondary/40">
                        <td className="px-4 py-2.5 font-mono text-[12px]">{p.id}</td>
                        <td className="px-4 py-2.5">{p.label ?? p.id}</td>
                        <td className="px-4 py-2.5">
                          <Badge variant="muted">{t(`params.scope.${p.scope}`)}</Badge>
                        </td>
                        <td className="px-4 py-2.5 font-mono text-[11.5px] text-muted-foreground">
                          {p.type}
                        </td>
                        <td className="px-4 py-2.5">
                          <span className="flex flex-wrap gap-1">
                            {p.required && (
                              <Badge variant="outline">{t("providers.card.required")}</Badge>
                            )}
                            {p.secret && (
                              <Badge variant="outline">{t("providers.card.secret")}</Badge>
                            )}
                            {!p.required && !p.secret && (
                              <span className="text-muted-foreground">—</span>
                            )}
                          </span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          {provider.warmup && (
            <section className="panel space-y-3 p-4">
              <div>
                <div className="eyebrow">{t("providers.card.warmupLabel")}</div>
                <h3 className="mt-1 text-[15px] font-semibold">
                  {t("providers.card.warmupParams")}
                </h3>
              </div>
              {warmupParams.length > 0 && (
                <ParamForm
                  params={warmupParams}
                  values={warmValues}
                  onChange={setWarmValues}
                  showErrors={showWarmErrors}
                  idPrefix={`warm-${provider.id}`}
                />
              )}
              <Button
                size="sm"
                variant="outline"
                disabled={warmupMutation.isPending}
                onClick={handleWarmup}
              >
                {warmupMutation.isPending
                  ? t("providers.card.warming")
                  : status === "warm"
                    ? t("providers.card.rewarm")
                    : t("providers.card.warmup")}
              </Button>
              {warmError && (
                <p role="alert" className="text-sm whitespace-pre-wrap text-destructive">
                  {warmError}
                </p>
              )}
            </section>
          )}
        </div>

        <aside className="space-y-4">
          <section className="panel overflow-hidden">
            <div className="flex items-center justify-between border-b px-4 py-3.5">
              <div className="eyebrow">{t("providers.card.warmCache")}</div>
              <span className="font-mono text-[11px] text-muted-foreground">
                {warmups.length}
              </span>
            </div>
            {warmups.length === 0 ? (
              <p className="p-4 text-[12px] leading-5 text-muted-foreground">
                {t("providers.card.noCache")}
              </p>
            ) : (
              <ul className="divide-y">
                {warmups.map((w) => (
                  <li key={w.fingerprint} className="px-4 py-3">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-mono text-[12px]">
                        {w.fingerprint.slice(0, 12)}
                      </span>
                      <CopyButton text={w.fingerprint} />
                      {w.warming ? (
                        <Badge variant="running">{t("providers.card.warmingEntry")}</Badge>
                      ) : (
                        <span className="ml-auto font-mono text-[11px] text-muted-foreground">
                          {formatTime(w.warmed_at, i18n.language)}
                        </span>
                      )}
                    </div>
                    {w.params && Object.keys(w.params).length > 0 && (
                      <dl className="mt-2 space-y-1">
                        {Object.entries(w.params).map(([key, value]) => (
                          <div key={key} className="flex items-baseline gap-2">
                            <dt className="w-20 flex-none text-[11px] font-medium text-muted-foreground">
                              {key}=
                            </dt>
                            <dd
                              className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-foreground/80"
                              title={value}
                            >
                              {value}
                            </dd>
                          </div>
                        ))}
                      </dl>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </section>
        </aside>
      </div>

      <Dialog
        open={confirmDelete}
        title={t("providers.card.deleteTitle")}
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
                ? t("providers.card.deleting")
                : t("providers.card.confirmDelete")}
            </Button>
          </>
        }
      >
        {t("providers.card.deleteBody", { id: provider.id })}
      </Dialog>
    </section>
  );
}
