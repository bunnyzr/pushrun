import { tokenStore } from "./auth";
import type { VersionInfo } from "./types";

export const API_BASE = "/api/v1";

export class ApiError extends Error {
  code: string;
  requestId: string;
  status: number;
  extra?: Record<string, unknown>;

  constructor(init: {
    code: string;
    message: string;
    requestId: string;
    status: number;
    extra?: Record<string, unknown>;
  }) {
    super(init.message);
    this.name = "ApiError";
    this.code = init.code;
    this.requestId = init.requestId;
    this.status = init.status;
    this.extra = init.extra;
  }
}

type UnauthorizedHandler = () => void;

let unauthorizedHandler: UnauthorizedHandler | null = null;

export function setUnauthorizedHandler(handler: UnauthorizedHandler | null): void {
  unauthorizedHandler = handler;
}

export function notifyUnauthorized(): void {
  unauthorizedHandler?.();
}

export async function parseErrorBody(res: Response): Promise<ApiError> {
  let code = "unknown";
  let message = res.statusText || `HTTP ${res.status}`;
  let requestId = "";
  let extra: Record<string, unknown> | undefined;
  try {
    const body: unknown = await res.json();
    if (body && typeof body === "object" && "error" in body) {
      const err = (body as { error: Record<string, unknown> }).error;
      if (typeof err.code === "string") code = err.code;
      if (typeof err.message === "string") message = err.message;
      if (typeof err.request_id === "string") requestId = err.request_id;
      const rest = Object.fromEntries(
        Object.entries(err).filter(
          ([key]) => key !== "code" && key !== "message" && key !== "request_id",
        ),
      );
      if (Object.keys(rest).length > 0) extra = rest;
    }
  } catch {
    // Non-JSON error body: keep the HTTP-derived defaults.
  }
  return new ApiError({ code, message, requestId, status: res.status, extra });
}

export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  const token = tokenStore.get();
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  const res = await fetch(`${API_BASE}${path}`, { ...init, headers });
  if (res.status === 401) {
    notifyUnauthorized();
    throw await parseErrorBody(res);
  }
  if (!res.ok) {
    throw await parseErrorBody(res);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

// The version handshake is exempt from token auth; it must never send one.
export async function fetchVersion(): Promise<VersionInfo> {
  const res = await fetch(`${API_BASE}/version`);
  if (!res.ok) {
    throw await parseErrorBody(res);
  }
  return (await res.json()) as VersionInfo;
}
