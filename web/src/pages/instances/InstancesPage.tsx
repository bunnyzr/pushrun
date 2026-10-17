import { useState } from "react";
import { useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import PageShell from "@/components/PageShell";
import { apiFetch } from "@/lib/api";
import type { InstanceState } from "@/lib/types";
import InstanceDetail from "./InstanceDetail";
import InstanceList from "./InstanceList";

export default function InstancesPage() {
  const { t } = useTranslation();
  // Selection lives in the URL so palette jumps and shared links land on
  // the same instance.
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedId = searchParams.get("sel");
  const setSelectedId = (id: string | null) =>
    setSearchParams(id ? { sel: id } : {}, { replace: true });
  const [search, setSearch] = useState("");
  const query = useQuery({
    queryKey: ["instances"],
    queryFn: ({ signal }) => apiFetch<{ instances: InstanceState[] }>("/instances", { signal }),
    refetchInterval: 5000,
  });

  const instances = query.data?.instances ?? [];
  const q = search.trim().toLowerCase();
  const filtered = q
    ? instances.filter((i) =>
        `${i.project}/${i.instance} ${i.branch ?? ""} ${i.commit ?? ""}`.toLowerCase().includes(q),
      )
    : instances;
  const selected =
    instances.find((i) => `${i.project}/${i.instance}` === selectedId) ?? null;

  return (
    <PageShell
      id="instances"
      title={t("pages.instances")}
      count={instances.length}
      onRefresh={() => void query.refetch()}
      refreshing={query.isFetching}
      searchValue={search}
      onSearchChange={setSearch}
      searchPlaceholder={t("instances.searchPlaceholder")}
      sidebar={
        query.isError ? (
          <p className="p-2 text-sm text-destructive">{t("instances.loadError")}</p>
        ) : query.isLoading ? (
          <p className="p-2 text-sm text-muted-foreground">{t("common.loading")}</p>
        ) : filtered.length === 0 ? (
          <p className="p-2 text-sm text-muted-foreground">{t("instances.empty")}</p>
        ) : (
          <InstanceList instances={filtered} selectedId={selectedId} onSelect={setSelectedId} />
        )
      }
    >
      {selected ? (
        <InstanceDetail
          key={`${selected.project}/${selected.instance}`}
          instance={selected}
          onDeleted={() => setSelectedId(null)}
        />
      ) : (
        <div className="grid h-full min-h-[240px] place-items-center rounded-lg border border-dashed text-sm text-muted-foreground">
          {t("instances.selectHint")}
        </div>
      )}
    </PageShell>
  );
}
