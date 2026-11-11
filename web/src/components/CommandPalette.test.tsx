import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter, useLocation } from "react-router";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import "@/i18n";
import CommandPalette from "./CommandPalette";

const instance = {
  project: "web",
  instance: "main",
  status: "RUNNING",
  branch: "main",
  updated_at: "2026-01-01T00:00:00Z",
};
const project = {
  schema: "pushrun.project/v1",
  name: "web",
  tree: [],
  pipeline: [],
};
const provider = {
  schema: "pushrun.provider/v1",
  id: "node-cache",
  name: "Node Cache",
  install: "scripts/install.sh",
  parameters: [],
  builtin: false,
  warmups: [],
};
const builtin = { ...provider, id: "git", name: "Git", builtin: true };

let actionCalls: string[];
const server = setupServer(
  http.get("/api/v1/instances", () => HttpResponse.json({ instances: [instance] })),
  http.get("/api/v1/projects", () => HttpResponse.json({ projects: [project] })),
  http.get("/api/v1/providers", () => HttpResponse.json({ providers: [provider, builtin] })),
  http.post("/api/v1/instances/web/main/:action", ({ params }) => {
    actionCalls.push(String(params.action));
    return HttpResponse.json({ status: "SUCCESS" });
  }),
);

beforeAll(() => server.listen());
afterEach(() => {
  server.resetHandlers();
  actionCalls = [];
});
afterAll(() => server.close());

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="location">{loc.pathname + loc.search}</div>;
}

function renderPalette() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={["/instances"]}>
      <QueryClientProvider client={client}>
        <CommandPalette open onClose={() => {}} />
        <LocationProbe />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("CommandPalette", () => {
  it("renders nothing when closed", () => {
    const client = new QueryClient();
    const { container } = render(
      <MemoryRouter>
        <QueryClientProvider client={client}>
          <CommandPalette open={false} onClose={() => {}} />
        </QueryClientProvider>
      </MemoryRouter>,
    );
    expect(container.textContent).toBe("");
  });

  it("jumps to an instance deep link, leaving builtins out of providers", async () => {
    const user = userEvent.setup();
    renderPalette();
    const item = await screen.findByText("web/main");
    await user.click(item);
    await waitFor(() =>
      expect(screen.getByTestId("location").textContent).toBe("/instances?sel=web%2Fmain"),
    );
  });

  it("filters by fuzzy query and fires instance actions without navigating", async () => {
    const user = userEvent.setup();
    renderPalette();
    const input = await screen.findByPlaceholderText(/Type to search/);
    await user.type(input, "stop");
    const item = await screen.findByText(/Stop · web\/main/);
    await user.click(item);
    await waitFor(() => expect(actionCalls).toEqual(["stop"]));
    expect(screen.getByTestId("location").textContent).toBe("/instances");
  });
});
