export interface VersionInfo {
  version: string;
  auth: { enabled: boolean };
}

export interface Project {
  schema: string;
  name: string;
  display_name?: string;
  tree: TreeNode[];
  pipeline: Step[];
}

export interface TreeNode {
  path: string;
  mount?: Mount;
}

export interface Mount {
  provider: string;
  primary?: boolean;
  params?: Record<string, string>;
}

export interface Step {
  name: string;
  run: string;
  timeout?: number;
  background?: boolean;
  health?: Health;
}

export interface Health {
  type: string;
  target: string;
  interval?: number;
  retries?: number;
}

export interface Provider {
  schema: string;
  id: string;
  name: string;
  description?: string;
  warmup?: string;
  install: string;
  parameters?: Param[];
  builtin?: boolean;
  warmups?: WarmupInfo[];
  // Present only on the single-provider GET, for stored providers.
  scripts?: Record<string, string>;
}

export interface Param {
  id: string;
  label?: string;
  type: "string" | "number" | "boolean" | "select";
  options?: string[];
  scope: "warmup" | "install";
  required?: boolean;
  default?: string;
  secret?: boolean;
}

export interface WarmupInfo {
  fingerprint: string;
  warmed_at: string;
  params?: Record<string, string>;
  warming?: boolean;
}

export interface InstanceState {
  project: string;
  instance: string;
  status: string;
  branch?: string;
  commit?: string;
  run_id?: string;
  port?: number;
  url?: string;
  trigger_repo?: string;
  updated_at: string;
}

export interface RunRecord {
  id: string;
  commit?: string;
  branch?: string;
  user?: string;
  status: string;
  error?: string;
  steps?: StepResult[];
  started_at: string;
  finished_at: string;
}

export interface StepResult {
  name: string;
  status: string;
  exit_code: number;
  started_at: string;
  finished_at: string;
}

export interface MountStatus {
  path: string;
  provider: string;
  status: "cold" | "warming" | "warm";
  warmed_at?: string;
}
