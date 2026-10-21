import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ApiError, apiFetch, fetchVersion, setUnauthorizedHandler } from "@/lib/api";
import { tokenStore } from "@/lib/auth";
import type { Project } from "@/lib/types";

type GateState = "loading" | "open" | "gate" | "unreachable";

export default function TokenGate({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const [state, setState] = useState<GateState>("loading");
  const [token, setToken] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    setUnauthorizedHandler(() => {
      tokenStore.clear();
      setState("gate");
      setToken("");
    });
    return () => setUnauthorizedHandler(null);
  }, []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      let version;
      try {
        version = await fetchVersion();
      } catch {
        if (!cancelled) setState("unreachable");
        return;
      }
      if (!version.auth.enabled) {
        if (!cancelled) setState("open");
        return;
      }
      if (!tokenStore.get()) {
        if (!cancelled) setState("gate");
        return;
      }
      try {
        await apiFetch<Project[]>("/projects");
        if (!cancelled) setState("open");
      } catch (err) {
        // A 401 already moved us back to the gate via the unauthorized handler.
        if (!cancelled && !(err instanceof ApiError && err.status === 401)) {
          setState("unreachable");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const onSubmit = useCallback(
    async (e: FormEvent) => {
      e.preventDefault();
      const value = token.trim();
      if (!value || submitting) return;
      setSubmitting(true);
      setError(null);
      tokenStore.set(value);
      try {
        await apiFetch<Project[]>("/projects");
        setState("open");
      } catch (err) {
        tokenStore.clear();
        setState("gate");
        setError(err instanceof ApiError ? t("gate.invalidToken") : t("gate.unreachable"));
      } finally {
        setSubmitting(false);
      }
    },
    [token, submitting, t],
  );

  if (state === "open") {
    return <>{children}</>;
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <div className="w-full max-w-sm rounded-lg border bg-card p-6 shadow-sm">
        <h1 className="text-lg font-bold">{t("app.name")}</h1>
        {state === "loading" && (
          <p className="mt-4 text-sm text-muted-foreground">{t("gate.loading")}</p>
        )}
        {state === "unreachable" && (
          <p className="mt-4 text-sm text-destructive">{t("gate.unreachable")}</p>
        )}
        {state === "gate" && (
          <form onSubmit={onSubmit} className="mt-4 space-y-4">
            <div className="space-y-1">
              <h2 className="text-base font-semibold">{t("gate.title")}</h2>
              <p className="text-sm text-muted-foreground">{t("gate.description")}</p>
            </div>
            <div className="space-y-2">
              <label htmlFor="pushrun-token" className="text-sm font-medium">
                {t("gate.tokenLabel")}
              </label>
              <input
                id="pushrun-token"
                type="password"
                autoComplete="off"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder={t("gate.tokenPlaceholder")}
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring"
              />
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <button
              type="submit"
              disabled={submitting || token.trim() === ""}
              className="w-full rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:opacity-50"
            >
              {submitting ? t("gate.checking") : t("gate.submit")}
            </button>
          </form>
        )}
      </div>
    </div>
  );
}
