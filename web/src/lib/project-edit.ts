// Client-side validation for the new project fields, mirroring the server
// rules in internal/project/project.go.

export const ENV_KEY_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/;
export const PORT_NAME_PATTERN = /^[A-Za-z][A-Za-z0-9]*$/;

export interface EnvRow {
  key: string;
  value: string;
}

export interface EnvIssue {
  code: "invalid_env_key" | "duplicate_env_key";
  index: number;
  key: string;
}

export interface PortIssue {
  code: "invalid_port_name" | "duplicate_port_name";
  index: number;
  name: string;
}

export function isValidEnvKey(key: string): boolean {
  return ENV_KEY_PATTERN.test(key);
}

// Rows with an empty key are unfinished drafts, not errors; the editor drops
// them before saving.
export function validateEnvRows(rows: EnvRow[]): EnvIssue[] {
  const issues: EnvIssue[] = [];
  const seen = new Set<string>();
  rows.forEach((row, index) => {
    if (row.key === "") return;
    if (!isValidEnvKey(row.key)) {
      issues.push({ code: "invalid_env_key", index, key: row.key });
      return;
    }
    if (seen.has(row.key)) {
      issues.push({ code: "duplicate_env_key", index, key: row.key });
      return;
    }
    seen.add(row.key);
  });
  return issues;
}

// Port names map to CI_PORT_<NAME> uppercased, so uniqueness is
// case-insensitive. Empty names are unfinished rows and are dropped on save.
export function validatePortNames(names: string[]): PortIssue[] {
  const issues: PortIssue[] = [];
  const seen = new Set<string>();
  names.forEach((name, index) => {
    if (name === "") return;
    if (!PORT_NAME_PATTERN.test(name)) {
      issues.push({ code: "invalid_port_name", index, name });
      return;
    }
    const key = name.toUpperCase();
    if (seen.has(key)) {
      issues.push({ code: "duplicate_port_name", index, name });
      return;
    }
    seen.add(key);
  });
  return issues;
}

// log_dir must be empty (the default) or a clean relative path that stays
// inside the instance directory.
export function isValidLogDir(dir: string): boolean {
  if (dir === "") return true;
  if (dir.startsWith("/")) return false;
  const parts = dir.split("/");
  if (parts.some((p) => p === "" || p === ".")) return false;
  if (parts.some((p) => p === "..")) return false;
  return true;
}

// Picks a free duplicate name: <name>-copy, then <name>-copy-2, -3, …
export function nextCopyName(name: string, existing: string[]): string {
  const taken = new Set(existing.map((n) => n.toLowerCase()));
  let candidate = `${name}-copy`;
  let n = 2;
  while (taken.has(candidate.toLowerCase())) {
    candidate = `${name}-copy-${n}`;
    n++;
  }
  return candidate;
}
