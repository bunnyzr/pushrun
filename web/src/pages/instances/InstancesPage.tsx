import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Boxes } from "lucide-react";
import EmptyState from "@/components/EmptyState";
import PageShell from "@/components/PageShell";
import { apiFetch } from "@/lib/api";
import type { InstanceState } from "@/lib/types";
import { useIsMobile } from "@/lib/useMediaQuery";
import InstanceDetail from "./InstanceDetail";
import InstanceList, { type InstanceFilter } from "./InstanceList";

export default function InstancesPage() {
  const { t } = useTranslation();
  // Selection lives in the URL so palette jumps and shared links land on
  // the same instance.
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedId = searchParams.get("sel");
  const setSelectedId = useCallback(
    (id: string | null) => setSearchParams(id ? { sel: id } : {}, { replace: true }),
    [setSearchParams],
  );
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<InstanceFilter>("all");
  const query = useQuery({
    queryKey: ["instances"],
    queryFn: ({ signal }) => apiFetch<{ instances: InstanceState[] }>("/instances", { signal }),
    refetchInterval: 5000,
  });

  const instances = useMemo(() => query.data?.instances ?? [], [query.data]);
  const isMobile = useIsMobile();

  useEffect(() => {
    if (isMobile || query.isLoading || selectedId) return;
    if (instances.length > 0) {
      setSelectedId(`${instances[0]!.project}/${instances[0]!.instance}`);
    }
  }, [instances, isMobile, query.isLoading, selectedId, setSelectedId]);

  const q = search.trim().toLowerCase();
  const filtered = instances.filter((instance) => {
    if (filter === "running" && instance.status !== "RUNNING") return false;
    if (filter === "failed" && instance.status !== "FAILED") return false;
    if (filter === "stopped" && (instance.status === "RUNNING" || instance.status === "FAILED")) {
      return false;
    }
    if (!q) return true;
    return `${instance.project}/${instance.instance} ${instance.branch ?? ""} ${instance.commit ?? ""}`
      .toLowerCase()
      .includes(q);
  });
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
      detailActive={selectedId !== null}
      onBack={() => setSelectedId(null)}
      sidebar={
        query.isError ? (
          <p className="p-2 text-sm text-destructive">{t("instances.loadError")}</p>
        ) : query.isLoading ? (
          <p className="p-2 text-sm text-muted-foreground">{t("common.loading")}</p>
        ) : instances.length === 0 ? (
          <p className="p-2 text-sm text-muted-foreground">{t("instances.empty")}</p>
        ) : (
          <InstanceList
            instances={filtered}
            all={instances}
            filter={filter}
            onFilterChange={setFilter}
            selectedId={selectedId}
            onSelect={setSelectedId}
          />
        )
      }
    >
      {query.isError && instances.length > 0 && (
        <p
          role="status"
          className="mb-4 rounded-md border border-warning-border bg-warning-bg px-3 py-2 text-sm text-warning"
        >
          {t("instances.stale")}
        </p>
      )}
      {selected ? (
        <InstanceDetail
          key={`${selected.project}/${selected.instance}`}
          instance={selected}
          onDeleted={() => setSelectedId(null)}
        />
      ) : query.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {t("instances.loadError")}
        </p>
      ) : query.isLoading ? (
        <p className="text-sm text-muted-foreground">{t("common.loading")}</p>
      ) : instances.length === 0 ? (
        <EmptyState
          icon={<Boxes className="h-5 w-5" aria-hidden="true" />}
          title={t("instances.emptyTitle")}
          description={t("instances.emptyDescription")}
          action={
            <Link
              to="/projects"
              className="inline-flex h-9 items-center rounded-md border border-input bg-card px-3.5 text-sm font-medium hover:bg-accent"
            >
              {t("instances.emptyAction")}
            </Link>
          }
        />
      ) : (
        <p className="text-sm text-muted-foreground">{t("instances.selectHint")}</p>
      )}
    </PageShell>
  );
}
