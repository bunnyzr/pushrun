import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import { streamSSE, type SSEEvent } from "./sse";
import { tokenStore } from "./auth";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  server.resetHandlers();
  tokenStore.clear();
});
afterAll(() => server.close());

function sseResponse(chunks: string[], { close = true }: { close?: boolean } = {}) {
  const encoder = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) {
        controller.enqueue(encoder.encode(chunk));
      }
      if (close) controller.close();
    },
  });
  return new HttpResponse(stream, {
    headers: { "Content-Type": "text/event-stream" },
  });
}

describe("streamSSE", () => {
  it("dispatches named events and joins multi-line data", async () => {
    server.use(
      http.get("/api/v1/runs/run-1/output", () =>
        sseResponse([
          "event: log\n",
          "data: line one\n",
          "data: line two\n",
          "\n",
          "event: status\n",
          'data: {"status":"succeeded"}\n',
          "\n",
        ]),
      ),
    );

    const events: SSEEvent[] = [];
    await streamSSE("/runs/run-1/output", {
      signal: new AbortController().signal,
      onEvent: (ev) => events.push(ev),
    });

    expect(events).toEqual([
      { event: "log", data: ["line one", "line two"] },
      { event: "status", data: ['{"status":"succeeded"}'] },
    ]);
  });

  it("turns heartbeat comments into comment events", async () => {
    server.use(
      http.get("/api/v1/runs/run-1/output", () =>
        sseResponse([": ping\n\n", "event: log\ndata: hello\n\n"]),
      ),
    );

    const events: SSEEvent[] = [];
    await streamSSE("/runs/run-1/output", {
      signal: new AbortController().signal,
      onEvent: (ev) => events.push(ev),
    });

    expect(events).toEqual([
      { event: "", data: [], comment: "ping" },
      { event: "log", data: ["hello"] },
    ]);
  });

  it("sends the bearer token header", async () => {
    tokenStore.set("sse-token");
    let seen: string | null = null;
    server.use(
      http.get("/api/v1/runs/run-1/output", ({ request }) => {
        seen = request.headers.get("authorization");
        return sseResponse(["event: status\ndata: {}\n\n"]);
      }),
    );

    await streamSSE("/runs/run-1/output", {
      signal: new AbortController().signal,
      onEvent: () => {},
    });

    expect(seen).toBe("Bearer sse-token");
  });

  it("rejects on HTTP errors with the parsed envelope", async () => {
    server.use(
      http.get("/api/v1/runs/missing/output", () =>
        HttpResponse.json(
          { error: { code: "not_found", message: "no such run", request_id: "r1" } },
          { status: 404 },
        ),
      ),
    );

    const err: unknown = await streamSSE("/runs/missing/output", {
      signal: new AbortController().signal,
      onEvent: () => {},
    }).catch((e: unknown) => e);

    expect(err).toMatchObject({ code: "not_found", status: 404 });
  });

  it("stops reading when the signal aborts", async () => {
    server.use(
      http.get("/api/v1/runs/run-1/output", () =>
        sseResponse(["event: log\ndata: first\n\n"], { close: false }),
      ),
    );

    const controller = new AbortController();
    const events: SSEEvent[] = [];
    const promise = streamSSE("/runs/run-1/output", {
      signal: controller.signal,
      onEvent: (ev) => {
        events.push(ev);
        controller.abort();
      },
    });

    const err: unknown = await promise.catch((e: unknown) => e);
    expect((err as Error).name).toBe("AbortError");
    expect(events).toEqual([{ event: "log", data: ["first"] }]);
  });
});
