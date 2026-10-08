import type { Param } from "./types";

// missingRequiredParams lists the ids of required params whose value is
// empty or blank. The projects page blocks saving while this is non-empty.
export function missingRequiredParams(
  params: Param[],
  values: Record<string, string>,
): string[] {
  return params
    .filter((p) => p.required && !(values[p.id] ?? "").trim())
    .map((p) => p.id);
}
