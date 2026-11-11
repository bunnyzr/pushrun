import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Layers, Plus, Upload } from "lucide-react";
import EmptyState from "@/components/EmptyState";
import PageShell from "@/components/PageShell";
import { Button } from "@/components/ui/button";
import { ApiError, apiFetch } from "@/lib/api";
import { toast } from "@/lib/toast";
import { useIsMobile } from "@/lib/useMediaQuery";
import type { Provider } from "@/lib/types";
import ProviderDetail from "./ProviderDetail";
import ProviderEditor from "./ProviderEditor";
import ProviderList from "./ProviderList";

export default function ProvidersPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const fileRef = useRef<HTMLInputElement>(null);
  // Selection lives in the URL so palette jumps and shared links land on
  // the same provider.
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedId = searchParams.get("sel");
  const setSelectedId = useCallback(
    (id: string | null) => setSearchParams(id ? { sel: id } : {}, { replace: true }),
    [setSearchParams],
  );
  const [search, setSearch] = useState("");
  // null = dialog closed; {provider: null} = create mode.
  const [editor, setEditor] = useState<{ provider: Provider | null } | null>(null);
  const [importError, setImportError] = useState<string | null>(null);

  const providersQuery = useQuery({
    queryKey: ["providers"],
    queryFn: ({ signal }) => apiFetch<{ providers: Provider[] }>("/providers", { signal }),
    refetchInterval: (query) =>
      (query.state.data?.providers ?? []).some((p) => (p.warmups ?? []).some((w) => w.warming))
        ? 2000
        : false,
  });

  // The builtins (git, symlink) are platform-owned and never shown here.
  const external = useMemo(
    () => (providersQuery.data?.providers ?? []).filter((p) => !p.builtin),
    [providersQuery.data],
  );
  const isMobile = useIsMobile();

  useEffect(() => {
    if (isMobile || providersQuery.isLoading || selectedId) return;
    if (external.length > 0) {
      setSelectedId(external[0]!.id);
    }
  }, [external, isMobile, providersQuery.isLoading, selectedId, setSelectedId]);

  const q = search.trim().toLowerCase();
  const filtered = q
    ? external.filter((p) => `${p.name} ${p.id}`.toLowerCase().includes(q))
    : external;
  const selected = external.find((p) => p.id === selectedId) ?? null;

  const importMutation = useMutation({
    mutationFn: (file: File) =>
      apiFetch<{ status: string; provider: Provider }>("/providers/import", {
        method: "POST",
        headers: { "Content-Type": "application/gzip" },
        body: file,
      }),
    onSuccess: (res) => {
      setImportError(null);
      toast(t("providers.imported", { id: res.provider.id }));
      void queryClient.invalidateQueries({ queryKey: ["providers"] });
      setSelectedId(res.provider.id);
    },
    onError: (err) => {
      const message = err instanceof ApiError ? `${err.code}: ${err.message}` : String(err);
      setImportError(t("providers.importFailed", { message }));
    },
  });

  return (
    <PageShell
      id="providers"
      title={t("pages.providers")}
      count={external.length}
      onRefresh={() => void providersQuery.refetch()}
      refreshing={providersQuery.isFetching}
      searchValue={search}
      onSearchChange={setSearch}
      searchPlaceholder={t("providers.searchPlaceholder")}
      detailActive={selectedId !== null}
      onBack={() => setSelectedId(null)}
      sidebar={
        <div className="space-y-3">
          <div className="space-y-2 px-1">
            <Button size="sm" className="w-full" onClick={() => setEditor({ provider: null })}>
              <Plus className="h-4 w-4" aria-hidden="true" />
              {t("providers.new")}
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="w-full"
              disabled={importMutation.isPending}
              onClick={() => fileRef.current?.click()}
            >
              <Upload className="h-4 w-4" aria-hidden="true" />
              {t("providers.import")}
            </Button>
            <input
              ref={fileRef}
              type="file"
              accept=".tar.gz,.tgz,application/gzip"
              aria-label={t("providers.importLabel")}
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
          {providersQuery.isLoading ? (
            <p className="p-2 text-sm text-muted-foreground">{t("common.loading")}</p>
          ) : providersQuery.isError ? (
            <p role="alert" className="p-2 text-sm text-destructive">
              {t("providers.loadError")}
            </p>
          ) : external.length === 0 ? (
            <p className="p-2 text-sm text-muted-foreground">{t("providers.empty")}</p>
          ) : filtered.length === 0 ? (
            <p className="p-2 text-sm text-muted-foreground">{t("providers.emptyFilter")}</p>
          ) : (
            <ProviderList providers={filtered} selectedId={selectedId} onSelect={setSelectedId} />
          )}
        </div>
      }
    >
      {selected ? (
        <ProviderDetail
          key={selected.id}
          provider={selected}
          onEdit={(p) => setEditor({ provider: p })}
          onDeleted={() => setSelectedId(null)}
        />
      ) : providersQuery.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {t("providers.loadError")}
        </p>
      ) : providersQuery.isLoading ? (
        <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
      ) : external.length === 0 ? (
        <EmptyState
          icon={<Layers className="h-5 w-5" aria-hidden="true" />}
          title={t("providers.emptyTitle")}
          description={t("providers.emptyDescription")}
          action={
            <>
              <Button onClick={() => setEditor({ provider: null })}>{t("providers.new")}</Button>
              <Button variant="outline" onClick={() => fileRef.current?.click()}>
                {t("providers.import")}
              </Button>
            </>
          }
        />
      ) : (
        <p className="text-sm text-muted-foreground">{t("providers.selectHint")}</p>
      )}
      {editor && (
        <ProviderEditor
          provider={editor.provider}
          onClose={() => setEditor(null)}
        />
      )}
    </PageShell>
  );
}
