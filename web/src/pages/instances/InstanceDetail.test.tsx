import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import "@/i18n";
import type { InstanceState } from "@/lib/types";
import InstanceDetail from "./InstanceDetail";

const actionCalls: string[] = [];

const server = setupServer(
  http.get("/api/v1/instances/demo/main/runs", () => HttpResponse.json({ runs: [] })),
  http.get("/api/v1/instances/demo/main/logs/tree", () => HttpResponse.json({ files: [] })),
  http.post("/api/v1/instances/demo/main/:action", ({ params }) => {
    actionCalls.push(String(params.action));
    return HttpResponse.json({ status: "SUCCESS", port: 21001 });
  }),
);

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  server.resetHandlers();
  actionCalls.length = 0;
});
afterAll(() => server.close());

const instance: InstanceState = {
  project: "demo",
  instance: "main",
  status: "STOPPED",
  branch: "main",
  commit: "17154a364a5b47526bfc8e8b17d8ca31cd397ec6",
  port: 20000,
  url: "http://127.0.0.1:20000",
  workspace: "/srv/pushrun/instances/demo/main",
  members: [
    {
      path: "app",
      repo: "github.com/x/app",
      branch: "main",
      commit: "17154a364a5b47526bfc8e8b17d8ca31cd397ec6",
    },
    {
      path: "lib",
      repo: "github.com/x/lib",
      branch: "release",
      commit: "aaaabbbbccccdddd",
    },
  ],
  updated_at: "2026-10-20T12:05:45Z",
};

function renderDetail() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <InstanceDetail instance={instance} onDeleted={vi.fn()} />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("InstanceDetail", () => {
  it("shows the workspace path and every member's branch and commit", async () => {
    renderDetail();
    expect(await screen.findByText("/srv/pushrun/instances/demo/main")).toBeTruthy();
    expect(screen.getByText("Member snapshot")).toBeTruthy();
    expect(screen.getByText("github.com/x/app")).toBeTruthy();
    expect(screen.getByText("github.com/x/lib")).toBeTruthy();
    expect(screen.getByText("release")).toBeTruthy();
    expect(screen.getAllByText("17154a364a5b").length).toBeGreaterThan(0);
  });

  it("posts the reset-port action from the instance toolbar", async () => {
    const user = userEvent.setup();
    renderDetail();
    await screen.findByText("Member snapshot");
    await user.click(screen.getByRole("button", { name: "Instance actions" }));
    await user.click(screen.getByRole("menuitem", { name: "Reset port" }));
    await waitFor(() => expect(actionCalls).toContain("reset-port"));
  });
});
