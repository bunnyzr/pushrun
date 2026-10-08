import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import "@/i18n";
import { tokenStore } from "@/lib/auth";
import { getToasts } from "@/lib/toast";
import type { Provider } from "@/lib/types";
import ProvidersPage from "./ProvidersPage";

const builtinGit = {
  schema: "pushrun.provider/v1",
  id: "git",
  name: "Git",
  install: "",
  builtin: true,
  warmups: [],
};

const builtinSymlink = {
  schema: "pushrun.provider/v1",
  id: "symlink",
  name: "Symlink",
  install: "",
  builtin: true,
  warmups: [],
};

const nodeCache: Provider = {
  schema: "pushrun.provider/v1",
  id: "node-cache",
  name: "Node Cache",
  description: "Caches node_modules per Node version",
  warmup: "scripts/warmup.sh",
  install: "scripts/install.sh",
  parameters: [
    { id: "version", label: "Node version", type: "string", scope: "warmup", required: true },
    { id: "prefix", label: "Install prefix", type: "string", scope: "install" },
  ],
  warmups: [
    {
      fingerprint: "abcdef1234567890fedcba",
      warmed_at: "2026-10-01T10:00:00Z",
      params: { version: "22" },
    },
  ],
};

const nodeCacheScripts = {
  "scripts/warmup.sh": "#!/bin/sh\necho warm\n",
  "scripts/install.sh": "#!/bin/sh\necho install\n",
};

const coldProvider: Provider = {
  schema: "pushrun.provider/v1",
  id: "docker-cache",
  name: "Docker Cache",
  install: "scripts/install.sh",
  warmups: [],
};

interface ProviderPayload extends Record<string, unknown> {
  scripts?: Record<string, string>;
}

function errorBody(code: string, message: string, extra: Record<string, unknown> = {}) {
  return { error: { code, message, request_id: "req-test", ...extra } };
}

let providersStore: Provider[];
let putBody: ProviderPayload | null;
let postBody: ProviderPayload | null;
let warmupBody: { params?: Record<string, string> } | null;

const server = setupServer(
  http.get("/api/v1/providers", () => HttpResponse.json({ providers: providersStore })),
  // The single-provider GET carries the on-disk script contents; the list
  // payload above deliberately does not.
  http.get("/api/v1/providers/:id", ({ params }) => {
    const p = providersStore.find((x) => x.id === params.id);
    if (!p) {
      return HttpResponse.json(errorBody("not_found", "provider not found"), { status: 404 });
    }
    return HttpResponse.json({ ...p, ...(params.id === "node-cache" ? { scripts: nodeCacheScripts } : {}) });
  }),
  http.post("/api/v1/providers", async ({ request }) => {
    postBody = (await request.json()) as ProviderPayload;
    return HttpResponse.json(postBody, { status: 201 });
  }),
  http.put("/api/v1/providers/:id", async ({ request, params }) => {
    putBody = (await request.json()) as ProviderPayload;
    return HttpResponse.json({ ...putBody, id: params.id });
  }),
  http.delete("/api/v1/providers/:id", () => HttpResponse.json({ status: "deleted" })),
  http.post("/api/v1/providers/:id/warmup", async ({ request, params }) => {
    warmupBody = (await request.json()) as { params?: Record<string, string> };
    return HttpResponse.json({ provider: params.id, status: "SUCCESS" });
  }),
  http.post("/api/v1/providers/import", () =>
    HttpResponse.json({ status: "imported", provider: nodeCache }, { status: 201 }),
  ),
);

beforeAll(() => {
  tokenStore.set("test-token");
  server.listen({ onUnhandledRequest: "error" });
});
beforeEach(() => {
  providersStore = structuredClone([builtinGit, builtinSymlink, nodeCache]) as Provider[];
  putBody = null;
  postBody = null;
  warmupBody = null;
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
      <ProvidersPage />
    </QueryClientProvider>,
  );
}

async function findCard(name: string): Promise<HTMLElement> {
  const heading = await screen.findByRole("heading", { name });
  const card = heading.closest("[data-testid^='provider-card-']");
  if (!card) throw new Error(`no card for ${name}`);
  return card as HTMLElement;
}

describe("ProvidersPage", () => {
  it("renders a warm lifecycle card with its warmed_at time", async () => {
    renderPage();
    const card = await findCard("Node Cache");

    expect(within(card).getByText("Warm")).toBeTruthy();
    const warmedAt = new Date("2026-10-01T10:00:00Z").toLocaleString();
    expect(card.textContent).toContain(warmedAt);
    // The cache entry shows its fingerprint and the redacted params.
    expect(card.textContent).toContain("abcdef123456");
    expect(card.textContent).toContain("version=22");
  });

  it("never lists the builtin providers", async () => {
    renderPage();
    await findCard("Node Cache");

    expect(screen.queryByRole("heading", { name: "Git" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "Symlink" })).toBeNull();
    expect(screen.queryByTestId("provider-card-git")).toBeNull();
  });

  it("shows a cold provider as cold", async () => {
    providersStore = structuredClone([coldProvider]) as Provider[];
    renderPage();
    const card = await findCard("Docker Cache");
    expect(within(card).getByText("Cold")).toBeTruthy();
  });

  it("surfaces the referencing project names when delete is refused with 409", async () => {
    server.use(
      http.delete("/api/v1/providers/node-cache", () =>
        HttpResponse.json(
          errorBody("conflict", "provider is still referenced", {
            referenced_by: ["web", "api"],
          }),
          { status: 409 },
        ),
      ),
    );
    const user = userEvent.setup();
    renderPage();
    const card = await findCard("Node Cache");

    await user.click(within(card).getByRole("button", { name: "Delete" }));
    await user.click(within(card).getByRole("button", { name: "Confirm delete" }));

    const alert = await within(card).findByRole("alert");
    expect(alert.textContent).toContain("web, api");
  });

  it("preloads the script textareas with the current server content when editing", async () => {
    const user = userEvent.setup();
    renderPage();
    const card = await findCard("Node Cache");

    await user.click(within(card).getByRole("button", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit provider node-cache" });

    await waitFor(() =>
      expect((within(dialog).getByLabelText("Warmup script") as HTMLTextAreaElement).value).toBe(
        nodeCacheScripts["scripts/warmup.sh"],
      ),
    );
    expect((within(dialog).getByLabelText("Install script") as HTMLTextAreaElement).value).toBe(
      nodeCacheScripts["scripts/install.sh"],
    );
  });

  it("PUTs the full script contents (preloaded plus edits) on save", async () => {
    const user = userEvent.setup();
    renderPage();
    const card = await findCard("Node Cache");

    await user.click(within(card).getByRole("button", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit provider node-cache" });
    const warmupArea = within(dialog).getByLabelText("Warmup script") as HTMLTextAreaElement;
    await waitFor(() => expect(warmupArea.value).toBe(nodeCacheScripts["scripts/warmup.sh"]));
    await user.clear(warmupArea);
    await user.type(warmupArea, "echo rewarmed");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(putBody).not.toBeNull());
    expect(putBody!.id).toBe("node-cache");
    expect(putBody!.scripts).toEqual({
      "scripts/warmup.sh": "echo rewarmed",
      "scripts/install.sh": nodeCacheScripts["scripts/install.sh"],
    });
    // The descriptor round-trips unchanged fields.
    expect(putBody!.install).toBe("scripts/install.sh");
    expect(putBody!.warmup).toBe("scripts/warmup.sh");
  });

  it("surfaces a warmup failure returned by a 200 response", async () => {
    server.use(
      http.post("/api/v1/providers/node-cache/warmup", () =>
        HttpResponse.json({
          provider: "node-cache",
          status: "FAILED",
          error: "warmup script exited 1: boom",
        }),
      ),
    );
    const user = userEvent.setup();
    renderPage();
    const card = await findCard("Node Cache");

    await user.type(within(card).getByLabelText("Node version"), "22");
    await user.click(within(card).getByRole("button", { name: "Re-warm" }));

    const alert = await within(card).findByRole("alert");
    expect(alert.textContent).toContain("warmup script exited 1: boom");
    await waitFor(() =>
      expect(getToasts().some((t) => t.message.includes("boom"))).toBe(true),
    );
  });

  it("posts the declared warmup params with the warmup request", async () => {
    const user = userEvent.setup();
    renderPage();
    const card = await findCard("Node Cache");

    await user.type(within(card).getByLabelText("Node version"), "24");
    await user.click(within(card).getByRole("button", { name: "Re-warm" }));

    await waitFor(() => expect(warmupBody).not.toBeNull());
    expect(warmupBody!.params).toEqual({ version: "24" });
    await within(card).findByText("Warm", {}, { timeout: 3000 });
    expect(getToasts().some((t) => t.message.includes("SUCCESS"))).toBe(true);
  });

  it("blocks warmup while a required warmup param is empty", async () => {
    const user = userEvent.setup();
    renderPage();
    const card = await findCard("Node Cache");

    await user.click(within(card).getByRole("button", { name: "Re-warm" }));

    expect(await within(card).findByRole("alert")).toBeTruthy();
    expect(warmupBody).toBeNull();
  });

  it("creates a provider with inline scripts", async () => {
    const user = userEvent.setup();
    renderPage();
    await findCard("Node Cache");

    await user.click(screen.getByRole("button", { name: "New provider" }));
    const dialog = await screen.findByRole("dialog", { name: "New provider" });
    await user.type(within(dialog).getByLabelText("ID"), "apt-cache");
    await user.type(within(dialog).getByLabelText("Name"), "APT Cache");
    await user.type(within(dialog).getByLabelText("Install script"), "echo installed");
    await user.click(within(dialog).getByRole("button", { name: "Create" }));

    await waitFor(() => expect(postBody).not.toBeNull());
    expect(postBody!.schema).toBe("pushrun.provider/v1");
    expect(postBody!.id).toBe("apt-cache");
    expect(postBody!.scripts).toEqual({ "scripts/install.sh": "echo installed" });
  });

  it("shows the server code and message when an import is refused", async () => {
    server.use(
      http.post("/api/v1/providers/import", () =>
        HttpResponse.json(
          errorBody("conflict", 'provider id "git" is reserved for a platform builtin'),
          { status: 409 },
        ),
      ),
    );
    renderPage();
    await findCard("Node Cache");

    const file = new File([new Uint8Array([1, 2, 3])], "git.tar.gz", {
      type: "application/gzip",
    });
    fireEvent.change(screen.getByLabelText("Import provider bundle"), {
      target: { files: [file] },
    });

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("conflict");
    expect(alert.textContent).toContain("reserved for a platform builtin");
  });

  it("exports a provider as a download with the token in the header", async () => {
    let authHeader: string | null = null;
    server.use(
      http.get("/api/v1/providers/node-cache/export", ({ request }) => {
        authHeader = request.headers.get("Authorization");
        return new HttpResponse(new Uint8Array([1, 2, 3]), {
          headers: { "Content-Type": "application/gzip" },
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
      const card = await findCard("Node Cache");
      await user.click(within(card).getByRole("button", { name: "Export" }));

      await waitFor(() => expect(createObjectURL).toHaveBeenCalled());
      expect(authHeader).toBe("Bearer test-token");
      expect(clickSpy).toHaveBeenCalled();
    } finally {
      clickSpy.mockRestore();
      delete (URL as { createObjectURL?: unknown }).createObjectURL;
      delete (URL as { revokeObjectURL?: unknown }).revokeObjectURL;
    }
  });
});
