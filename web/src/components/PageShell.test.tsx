import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import "@/i18n";
import PageShell from "./PageShell";

function renderShell() {
  return render(
    <PageShell id="test" title="Projects" sidebar={<p>project list</p>}>
      <p>project detail</p>
    </PageShell>,
  );
}

describe("PageShell", () => {
  beforeEach(() => localStorage.clear());

  it("collapses and restores the context list", async () => {
    const user = userEvent.setup();
    const { container } = renderShell();
    const shell = container.querySelector(".console-shell");
    expect(shell?.className).not.toContain("sidebar-collapsed");

    await user.click(screen.getByRole("button", { name: "Hide list" }));
    expect(shell?.className).toContain("sidebar-collapsed");
    expect(localStorage.getItem("pushrun:shell:test:collapsed")).toBe("true");

    await user.click(screen.getByRole("button", { name: "Show list" }));
    expect(shell?.className).not.toContain("sidebar-collapsed");
    expect(localStorage.getItem("pushrun:shell:test:collapsed")).toBe("false");
  });
});
