import { render, screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, describe, expect, it } from "vitest";
import App from "./App";

const server = setupServer(
  http.get("/api/v1/version", () =>
    HttpResponse.json({ version: "0.1.0-test", auth: { enabled: false } }),
  ),
);

beforeAll(() => server.listen({ onUnhandledRequest: "error" }));
afterEach(() => {
  server.resetHandlers();
  localStorage.clear();
});
afterAll(() => server.close());

describe("App", () => {
  it("renders the layout after an auth-free handshake", async () => {
    render(<App />);
    expect(await screen.findByRole("link", { name: "Projects" })).toBeTruthy();
    expect(await screen.findByText(/0\.1\.0-test/)).toBeTruthy();
  });
});
