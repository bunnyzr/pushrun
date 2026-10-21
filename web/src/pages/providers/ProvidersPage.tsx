import { useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import PageShell from "@/components/PageShell";
import { Button } from "@/components/ui/button";
import { ApiError, apiFetch } from "@/lib/api";
import { toast } from "@/lib/toast";
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
  const setSelectedId = (id: string | null) =>
    setSearchParams(id ? { sel: id } : {}, { replace: true });
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
  const external = (providersQuery.data?.providers ?? []).filter((p) => !p.builtin);
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
      sidebar={
        <div className="space-y-2">
          <div className="flex gap-2 px-1">
            <Button size="sm" className="flex-1" onClick={() => setEditor({ provider: null })}>
              {t("providers.new")}
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="flex-1"
              disabled={importMutation.isPending}
              onClick={() => fileRef.current?.click()}
            >
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
          ) : filtered.length === 0 ? (
            <p className="p-2 text-sm text-muted-foreground">{t("providers.empty")}</p>
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
      ) : (
        <div className="grid h-full min-h-[240px] place-items-center rounded-lg border border-dashed text-sm text-muted-foreground">
          {t("providers.selectHint")}
        </div>
      )}
      {editor && <ProviderEditor provider={editor.provider} onClose={() => setEditor(null)} />}
    </PageShell>
  );
}
