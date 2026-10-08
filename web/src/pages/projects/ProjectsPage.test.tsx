import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import "@/i18n";
import { tokenStore } from "@/lib/auth";
import { getToasts } from "@/lib/toast";
import type { Project } from "@/lib/types";
import ProjectsPage from "./ProjectsPage";

const providers = [
  {
    schema: "pushrun.provider/v1",
    id: "git",
    name: "Git",
    install: "",
    builtin: true,
    parameters: [
      { id: "repo", label: "Repository", type: "string", scope: "warmup", required: true },
      { id: "token", label: "Token", type: "string", scope: "warmup", secret: true },
    ],
  },
  {
    schema: "pushrun.provider/v1",
    id: "symlink",
    name: "Symlink",
    install: "",
    builtin: true,
    parameters: [{ id: "target", label: "Target", type: "string", scope: "install", required: true }],
  },
];

const projectWeb: Project = {
  schema: "pushrun.project/v1",
  name: "web",
  display_name: "Web App",
  tree: [
    {
      path: "app",
      mount: { provider: "git", primary: true, params: { repo: "github.com/x/web", token: "***" } },
    },
    { path: "data" },
  ],
  pipeline: [
    { name: "build", run: "make build", timeout: 300 },
    {
      name: "serve",
      run: "./app/serve",
      background: true,
      health: { type: "http", target: "http://127.0.0.1/healthz" },
    },
  ],
};

const projectApi: Project = {
  schema: "pushrun.project/v1",
  name: "api",
  tree: [
    { path: "svc", mount: { provider: "git", primary: true, params: { repo: "github.com/x/api" } } },
  ],
  pipeline: [],
};

let projectsStore: Project[];
let putBody: Project | null;
let postBody: Record<string, unknown> | null;
let warmCalls: number;
let mountStatus: string;

function errorBody(code: string, message: string, extra: Record<string, unknown> = {}) {
  return { error: { code, message, request_id: "req-test", ...extra } };
}

const server = setupServer(
  http.get("/api/v1/projects", () => HttpResponse.json({ projects: projectsStore })),
  http.post("/api/v1/projects", async ({ request }) => {
    postBody = (await request.json()) as Record<string, unknown>;
    projectsStore = [...projectsStore, postBody as unknown as Project];
    return HttpResponse.json(postBody, { status: 201 });
  }),
  http.get("/api/v1/providers", () => HttpResponse.json({ providers })),
  http.get("/api/v1/projects/:name", ({ params }) => {
    const p = projectsStore.find((x) => x.name === params.name);
    return p
      ? HttpResponse.json(p)
      : HttpResponse.json(errorBody("not_found", "no such project"), { status: 404 });
  }),
  http.put("/api/v1/projects/:name", async ({ request, params }) => {
    putBody = (await request.json()) as Project;
    const updated = { ...putBody, name: params.name as string, display_name: "Renamed!" };
    projectsStore = projectsStore.map((x) => (x.name === params.name ? updated : x));
    return HttpResponse.json(updated);
  }),
  http.get("/api/v1/projects/:name/warmup", ({ params }) => {
    const p = projectsStore.find((x) => x.name === params.name);
    const mounts = (p?.tree ?? [])
      .filter((n) => n.mount)
      .map((n) => ({ path: n.path, provider: n.mount!.provider, status: mountStatus }));
    return HttpResponse.json({ mounts });
  }),
  http.post("/api/v1/projects/:name/warmup", () => {
    warmCalls++;
    mountStatus = "warm";
    return HttpResponse.json({
      status: "SUCCESS",
      results: [{ provider: "git", path: "app", status: "SUCCESS" }],
    });
  }),
);

beforeAll(() => {
  tokenStore.set("test-token");
  server.listen({ onUnhandledRequest: "error" });
});
beforeEach(() => {
  projectsStore = structuredClone([projectWeb, projectApi]);
  putBody = null;
  postBody = null;
  warmCalls = 0;
  mountStatus = "cold";
});
afterEach(() => server.resetHandlers());
afterAll(() => {
  tokenStore.clear();
  server.close();
});

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ProjectsPage />
    </QueryClientProvider>,
  );
}

async function selectWeb(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: /Web App/ }));
  await screen.findByRole("button", { name: "app" });
}

describe("ProjectsPage", () => {
  it("lists projects with their primary repo summary", async () => {
    renderPage();
    expect(await screen.findByRole("button", { name: /Web App/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /^api/ })).toBeTruthy();
    expect(screen.getByText("github.com/x/web")).toBeTruthy();
  });

  it("shows the tree, inspector, and pipeline of the selected project", async () => {
    const user = userEvent.setup();
    renderPage();
    await selectWeb(user);

    expect(screen.getByRole("button", { name: "data" })).toBeTruthy();

    // The first node (the mounted "app") is preselected in the inspector.
    const providerSelect = (await screen.findByLabelText("Provider")) as HTMLSelectElement;
    expect(providerSelect.value).toBe("git");
    expect((screen.getByLabelText("Repository") as HTMLInputElement).value).toBe(
      "github.com/x/web",
    );
    const token = screen.getByLabelText("Token") as HTMLInputElement;
    expect(token.type).toBe("password");
    expect(token.value).toBe("***");

    expect(screen.getByDisplayValue("make build")).toBeTruthy();
    expect(screen.getByDisplayValue("./app/serve")).toBeTruthy();
  });

  it("saves with PUT, re-renders from the response, and round-trips masked secrets verbatim", async () => {
    const user = userEvent.setup();
    renderPage();
    await selectWeb(user);

    const nameInput = (await screen.findByLabelText("Display name")) as HTMLInputElement;
    await user.clear(nameInput);
    await user.type(nameInput, "Renamed");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(putBody).not.toBeNull());
    expect(putBody!.tree[0]!.mount!.params!.token).toBe("***");
    expect(putBody!.display_name).toBe("Renamed");
    // Success re-renders from the server response, not the local draft.
    expect(await screen.findByDisplayValue("Renamed!")).toBeTruthy();
  });

  it("warns that secrets must be re-entered when renaming a node with masked params", async () => {
    const user = userEvent.setup();
    renderPage();
    await selectWeb(user);

    await user.click(screen.getByRole("button", { name: "Rename app" }));
    const input = (await screen.findByDisplayValue("app")) as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "frontend{Enter}");

    // The rename applies, and a warning names the orphaned node path.
    expect(await screen.findByRole("button", { name: "frontend" })).toBeTruthy();
    await waitFor(() =>
      expect(
        getToasts().some(
          (item) =>
            item.variant === "error" &&
            item.message.includes("app") &&
            item.message.includes("re-enter"),
        ),
      ).toBe(true),
    );
  });

  it("surfaces the server's secret_params_orphaned rejection in the save error", async () => {
    server.use(
      http.put("/api/v1/projects/:name", () =>
        HttpResponse.json(
          errorBody(
            "secret_params_orphaned",
            "secret parameter values could not be restored (the node was renamed or moved, or the secret is new): re-enter them for frontend: token",
          ),
          { status: 400 },
        ),
      ),
    );
    const user = userEvent.setup();
    renderPage();
    await selectWeb(user);

    await user.click(screen.getByRole("button", { name: "Rename app" }));
    const input = (await screen.findByDisplayValue("app")) as HTMLInputElement;
    await user.clear(input);
    await user.type(input, "frontend{Enter}");
    await user.click(screen.getByRole("button", { name: "Save" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("secret_params_orphaned");
    expect(alert.textContent).toContain("re-enter them for frontend: token");
  });

  it("keeps the draft and shows the server error when the save fails", async () => {
    server.use(
      http.put("/api/v1/projects/:name", () =>
        HttpResponse.json(errorBody("invalid", "tree broken"), { status: 400 }),
      ),
    );
    const user = userEvent.setup();
    renderPage();
    await selectWeb(user);

    const nameInput = (await screen.findByLabelText("Display name")) as HTMLInputElement;
    await user.clear(nameInput);
    await user.type(nameInput, "Drafty");
    await user.click(screen.getByRole("button", { name: "Save" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("invalid: tree broken");
    expect((screen.getByLabelText("Display name") as HTMLInputElement).value).toBe("Drafty");
  });

  it("asks once before dropping unsaved changes on project switch", async () => {
    const user = userEvent.setup();
    renderPage();
    await selectWeb(user);

    const nameInput = (await screen.findByLabelText("Display name")) as HTMLInputElement;
    await user.type(nameInput, "dirty");
    await user.click(screen.getByRole("button", { name: /^api/ }));

    const dialog = await screen.findByRole("dialog", { name: "Discard unsaved changes?" });
    await user.click(within(dialog).getByRole("button", { name: "Discard changes" }));

    // The new project loads and every piece of prior state is gone.
    expect(await screen.findByRole("button", { name: "svc" })).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(putBody).toBeNull();

    // Switching back with a clean draft does not prompt again.
    await user.click(screen.getByRole("button", { name: /Web App/ }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(await screen.findByRole("button", { name: "app" })).toBeTruthy();
  });

  it("cancelling the switch prompt keeps the draft", async () => {
    const user = userEvent.setup();
    renderPage();
    await selectWeb(user);

    const nameInput = (await screen.findByLabelText("Display name")) as HTMLInputElement;
    await user.type(nameInput, "keep me");
    await user.click(screen.getByRole("button", { name: /^api/ }));

    const dialog = await screen.findByRole("dialog", { name: "Discard unsaved changes?" });
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    expect(screen.queryByRole("dialog")).toBeNull();
    expect((screen.getByLabelText("Display name") as HTMLInputElement).value).toContain("keep me");
  });

  it("blocks the save when a required parameter is empty and flags the field", async () => {
    const user = userEvent.setup();
    renderPage();
    await selectWeb(user);

    const repo = (await screen.findByLabelText("Repository")) as HTMLInputElement;
    await user.clear(repo);
    await user.click(screen.getByRole("button", { name: "Save" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Required parameters");
    expect(repo.getAttribute("aria-invalid")).toBe("true");
    expect(putBody).toBeNull();
  });

  it("triggers warmup and refreshes the per-mount status", async () => {
    const user = userEvent.setup();
    renderPage();
    await selectWeb(user);

    expect((await screen.findAllByText("Cold")).length).toBeGreaterThan(0);
    await user.click(screen.getByRole("button", { name: "Warm up" }));

    await waitFor(() => expect(warmCalls).toBe(1));
    await waitFor(() => expect(screen.getAllByText("Warm").length).toBeGreaterThan(0));
  });

  it("adds a root node inline and includes it in the saved tree", async () => {
    const user = userEvent.setup();
    renderPage();
    await selectWeb(user);

    await user.click(screen.getByRole("button", { name: "Add root node" }));
    const input = await screen.findByPlaceholderText("node name");
    await user.type(input, "assets{Enter}");

    expect(await screen.findByRole("button", { name: "assets" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(putBody).not.toBeNull());
    expect(putBody!.tree.some((n) => n.path === "assets")).toBe(true);
  });

  it("creates a project with the v1 schema and selects it", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "New project" }));
    const dialog = await screen.findByRole("dialog", { name: "New project" });
    await user.type(within(dialog).getByLabelText("Name"), "blog");
    await user.click(within(dialog).getByRole("button", { name: "Create" }));

    await waitFor(() => expect(postBody).not.toBeNull());
    expect(postBody!.schema).toBe("pushrun.project/v1");
    expect(postBody!.name).toBe("blog");
    expect(postBody!.tree).toEqual([]);
    expect(await screen.findByText("No nodes yet. Add a root node to start.")).toBeTruthy();
  });

  it("reports missing providers specifically when an import is refused", async () => {
    server.use(
      http.post("/api/v1/projects/import", () =>
        HttpResponse.json(
          errorBody("missing_providers", "project x references missing providers", {
            missing_providers: ["docker", "node"],
          }),
          { status: 400 },
        ),
      ),
    );
    renderPage();

    const file = new File([new Uint8Array([1, 2, 3])], "bundle.tar.gz", {
      type: "application/gzip",
    });
    fireEvent.change(await screen.findByLabelText("Import"), { target: { files: [file] } });

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("docker, node");
  });

  it("exports the project as a download with the token in the header", async () => {
    let authHeader: string | null = null;
    server.use(
      http.get("/api/v1/projects/web/export", ({ request }) => {
        authHeader = request.headers.get("Authorization");
        return new HttpResponse(new Uint8Array([1, 2, 3]), {
          headers: {
            "Content-Type": "application/gzip",
            "Content-Disposition": 'attachment; filename="web.tar.gz"',
          },
        });
      }),
    );
    const createObjectURL = vi.fn(() => "blob:fake");
    const revokeObjectURL = vi.fn();
    URL.createObjectURL = createObjectURL;
    URL.revokeObjectURL = revokeObjectURL;
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    try {
      const user = userEvent.setup();
      renderPage();
      await selectWeb(user);
      await user.click(screen.getByRole("button", { name: "Export" }));

      await waitFor(() => expect(createObjectURL).toHaveBeenCalled());
      expect(authHeader).toBe("Bearer test-token");
      expect(clickSpy).toHaveBeenCalled();
    } finally {
      clickSpy.mockRestore();
      delete (URL as { createObjectURL?: unknown }).createObjectURL;
      delete (URL as { revokeObjectURL?: unknown }).revokeObjectURL;
    }
  });

  it("drops a stale save result that lands after switching projects", async () => {
    let releasePut: () => void = () => {};
    server.use(
      http.put("/api/v1/projects/web", async () => {
        await new Promise<void>((resolve) => {
          releasePut = resolve;
        });
        return HttpResponse.json(errorBody("invalid", "late failure"), { status: 400 });
      }),
    );
    const user = userEvent.setup();
    renderPage();
    await selectWeb(user);

    const nameInput = (await screen.findByLabelText("Display name")) as HTMLInputElement;
    await user.type(nameInput, "dirty");
    await user.click(screen.getByRole("button", { name: "Save" }));
    // The PUT is in flight (the button shows the pending label).
    expect(await screen.findByRole("button", { name: "Saving…" })).toBeTruthy();

    // Switch to api while the save is pending, confirming the discard prompt.
    await user.click(screen.getByRole("button", { name: /^api/ }));
    const dialog = await screen.findByRole("dialog", { name: "Discard unsaved changes?" });
    await user.click(within(dialog).getByRole("button", { name: "Discard changes" }));
    await screen.findByRole("button", { name: "svc" });

    // web's PUT now rejects; nothing from it may land on api's editor.
    releasePut();
    await waitFor(() => expect(screen.getByRole("button", { name: "Save" })).toBeTruthy());
    expect(screen.queryByRole("alert")).toBeNull();
    expect(getToasts().some((item) => item.message.includes("late failure"))).toBe(false);
  });
});
