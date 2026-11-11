import type { Provider } from "@/lib/types";

export type LifecycleStatus = "cold" | "warming" | "warm";

// lifecycleStatus aggregates the shared-artifact cache entries: an in-flight
// entry wins, otherwise any entry means warm. A failed warmup is not a
// persisted state — it arrives on the warmup POST response.
export function lifecycleStatus(provider: Provider): LifecycleStatus {
  const warmups = provider.warmups ?? [];
  if (warmups.some((w) => w.warming)) return "warming";
  if (warmups.length > 0) return "warm";
  return "cold";
}
