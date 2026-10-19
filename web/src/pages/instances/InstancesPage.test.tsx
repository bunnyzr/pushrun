import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import "@/i18n";
import InstancesPage from "./InstancesPage";

const instanceA = {
  project: "web",
  instance: "main",
  status: "RUNNING",
  branch: "main",
  commit: "abc1234deadbeef",
  run_id: "run-a",
  port: 8080,
  url: "http://localhost:8080",
  updated_at: "2026-10-07T00:00:00Z",
};

const instanceB = {
  project: "api",
  instance: "dev",
  status: "STOPPED",
  updated_at: "2026-10-07T00:00:00Z",
};

const runA = {
  id: "run-a",
  status: "SUCCESS",
  branch: "main",
  commit: "abc1234deadbeef",
  started_at: "2026-10-06T23:00:00Z",
  finished_at: "2026-10-06T23:01:00Z",
};

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

const server = setupServer(
  http.get("/api/v1/instances", () => HttpResponse.json({ instances: [instanceA, instanceB] })),
  http.get("/api/v1/instances/:project/:instance/runs", () => HttpResponse.json({ runs: [] })),
  http.get("/api/v1/instances/:project/:instance/logs/tree", () =>
    HttpResponse.json({ files: [] }),
  ),
  http.get("/api/v1/runs/:id/output", () => sseResponse(["event: status\ndata: {}\n\n"])),
);

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
      <InstancesPage />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("InstancesPage", () => {
  it("renders instance states from the API", async () => {
    renderPage();
    expect(await screen.findByText("web/main")).toBeTruthy();
    expect(screen.getByText("api/dev")).toBeTruthy();
    expect(screen.getByText("Running")).toBeTruthy();
    expect(screen.getByText("Stopped")).toBeTruthy();
    expect(screen.getByText("abc1234")).toBeTruthy();
    expect(screen.getByText(":8080")).toBeTruthy();
    await userEvent.click(await screen.findByRole("button", { name: /web\/main/ }));
    const link = screen.getByRole("link", { name: "http://localhost:8080" });
    expect(link.getAttribute("href")).toBe("http://localhost:8080");
    expect(link.getAttribute("target")).toBe("_blank");
  });

  it("clicking stop issues a POST to the stop endpoint", async () => {
    let stopped = false;
    server.use(
      http.post("/api/v1/instances/web/main/stop", () => {
        stopped = true;
        return HttpResponse.json({ run_id: "run-9", status: "STOPPED" });
      }),
    );

    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: /web\/main/ }));
    await user.click(screen.getByRole("button", { name: "Stop" }));
    await waitFor(() => expect(stopped).toBe(true));
  });

  it("points the build log at the just-triggered run without a history click", async () => {
    // api/dev is STOPPED, so its state-aware primary action is "Run".
    server.use(
      http.get("/api/v1/instances/api/dev/runs", () => HttpResponse.json({ runs: [] })),
      http.post("/api/v1/instances/api/dev/run", () =>
        HttpResponse.json({ run_id: "run-new", status: "RUNNING" }),
      ),
      http.get("/api/v1/runs/run-new/output", () =>
        sseResponse(["event: log\ndata: fresh run output\n\nevent: status\ndata: {}\n\n"]),
      ),
    );

    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: /api\/dev/ }));
    await user.click(screen.getByRole("button", { name: "Run" }));

    expect(await screen.findByText("fresh run output")).toBeTruthy();
  });

  it("streams build log lines and shows the terminal status", async () => {
    server.use(
      http.get("/api/v1/instances/web/main/runs", () => HttpResponse.json({ runs: [runA] })),
      http.get("/api/v1/runs/run-a/output", () =>
        sseResponse([
          "event: log\n",
          "data: building image\n",
          "data: step 2 done\n",
          "\n",
          "event: status\n",
          'data: {"status":"SUCCESS"}\n',
          "\n",
        ]),
      ),
    );

    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: /web\/main/ }));

    expect(await screen.findByText("building image")).toBeTruthy();
    expect(screen.getByText("step 2 done")).toBeTruthy();
    const buildLog = screen.getByRole("region", { name: "Build log" });
    expect((await within(buildLog).findAllByText("Success")).length).toBeGreaterThan(0);
  });

  it("switching instances aborts the in-flight stream and shows the new empty state", async () => {
    let aAborted = false;
    server.use(
      http.get("/api/v1/instances/web/main/runs", () => HttpResponse.json({ runs: [runA] })),
      http.get("/api/v1/runs/run-a/output", ({ request }) => {
        request.signal.addEventListener("abort", () => {
          aAborted = true;
        });
        const encoder = new TextEncoder();
        const stream = new ReadableStream<Uint8Array>({
          start(controller) {
            controller.enqueue(encoder.encode("event: log\ndata: line from A\n\n"));
          },
          cancel() {
            aAborted = true;
          },
        });
        return new HttpResponse(stream, {
          headers: { "Content-Type": "text/event-stream" },
        });
      }),
    );

    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: /web\/main/ }));
    expect(await screen.findByText("line from A")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: /api\/dev/ }));

    expect(await screen.findByText("No runs yet.")).toBeTruthy();
    await waitFor(() => expect(aAborted).toBe(true));
    expect(screen.queryByText("line from A")).toBeNull();
  });

  it("shows named ports in the state grid", async () => {
    server.use(
      http.get("/api/v1/instances", () =>
        HttpResponse.json({
          instances: [
            {
              ...instanceA,
              ports: { web: 8080, admin: 9090 },
            },
            instanceB,
          ],
        }),
      ),
    );

    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: /web\/main/ }));

    expect(await screen.findByText("Ports")).toBeTruthy();
    expect(screen.getByText("web → :8080")).toBeTruthy();
    expect(screen.getByText("admin → :9090")).toBeTruthy();
  });

  it("deletes a run from the history and clears the selection", async () => {
    let runsList = [runA];
    server.use(
      http.get("/api/v1/instances/web/main/runs", () => HttpResponse.json({ runs: runsList })),
      http.delete("/api/v1/runs/:id", ({ params }) => {
        runsList = runsList.filter((r) => r.id !== params.id);
        return HttpResponse.json({ status: "deleted" });
      }),
      http.get("/api/v1/runs/run-a/output", () =>
        sseResponse(["event: log\ndata: old build output\n\nevent: status\ndata: {}\n\n"]),
      ),
    );

    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: /web\/main/ }));
    expect(await screen.findByText("old build output")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Delete run run-a" }));

    // The row disappears and the build log falls back to the empty state.
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Delete run run-a" })).toBeNull(),
    );
    expect(await screen.findByText("No runs yet.")).toBeTruthy();
    expect(screen.queryByText("old build output")).toBeNull();
  });

  it("deletes a business-log file and clears the selection", async () => {
    let filesList = ["app.log", "error.log"];
    server.use(
      http.get("/api/v1/instances/web/main/logs/tree", () =>
        HttpResponse.json({ files: filesList }),
      ),
      http.get("/api/v1/instances/web/main/logs/file", ({ request }) => {
        const url = new URL(request.url);
        return HttpResponse.json({
          path: url.searchParams.get("path"),
          content: "hello from file\n",
        });
      }),
      http.delete("/api/v1/instances/web/main/logs/file", ({ request }) => {
        const url = new URL(request.url);
        filesList = filesList.filter((f) => f !== url.searchParams.get("path"));
        return HttpResponse.json({ status: "deleted" });
      }),
    );

    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: /web\/main/ }));
    await user.click(screen.getByRole("button", { name: "Business logs" }));
    await user.click(await screen.findByRole("button", { name: "app.log" }));
    expect(await screen.findByText("hello from file")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "Delete log file app.log" }));

    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "app.log" })).toBeNull(),
    );
    expect(screen.getByRole("button", { name: "error.log" })).toBeTruthy();
    expect(await screen.findByText("Select a log file.")).toBeTruthy();
  });
});
