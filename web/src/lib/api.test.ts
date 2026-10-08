import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { ApiError, apiFetch, setUnauthorizedHandler } from "./api";
import { tokenStore } from "./auth";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  server.resetHandlers();
  tokenStore.clear();
  setUnauthorizedHandler(null);
});
afterAll(() => server.close());

describe("apiFetch", () => {
  it("injects the bearer token header from the token store", async () => {
    tokenStore.set("secret-token");
    let seen: string | null = null;
    server.use(
      http.get("/api/v1/projects", ({ request }) => {
        seen = request.headers.get("authorization");
        return HttpResponse.json([]);
      }),
    );

    const result = await apiFetch<unknown[]>("/projects");

    expect(result).toEqual([]);
    expect(seen).toBe("Bearer secret-token");
  });

  it("sends no Authorization header when no token is stored", async () => {
    let seen: string | null = null;
    server.use(
      http.get("/api/v1/projects", ({ request }) => {
        seen = request.headers.get("authorization");
        return HttpResponse.json([]);
      }),
    );

    await apiFetch("/projects");

    expect(seen).toBeNull();
  });

  it("parses the standard error envelope into an ApiError", async () => {
    server.use(
      http.get("/api/v1/nope", () =>
        HttpResponse.json(
          {
            error: {
              code: "not_found",
              message: "missing resource",
              request_id: "req-123",
              hint: "check the path",
            },
          },
          { status: 404 },
        ),
      ),
    );

    const err: unknown = await apiFetch("/nope").catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiError);
    const apiErr = err as ApiError;
    expect(apiErr.code).toBe("not_found");
    expect(apiErr.message).toBe("missing resource");
    expect(apiErr.requestId).toBe("req-123");
    expect(apiErr.status).toBe(404);
    expect(apiErr.extra).toEqual({ hint: "check the path" });
  });

  it("fires the registered onUnauthorized handler and throws on 401", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    server.use(
      http.get("/api/v1/projects", () =>
        HttpResponse.json(
          { error: { code: "unauthorized", message: "bad token", request_id: "req-9" } },
          { status: 401 },
        ),
      ),
    );

    await expect(apiFetch("/projects")).rejects.toBeInstanceOf(ApiError);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it("does not fire onUnauthorized for non-401 errors", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    server.use(
      http.get("/api/v1/broken", () =>
        HttpResponse.json(
          { error: { code: "internal", message: "boom", request_id: "r" } },
          { status: 500 },
        ),
      ),
    );

    await expect(apiFetch("/broken")).rejects.toBeInstanceOf(ApiError);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
});
