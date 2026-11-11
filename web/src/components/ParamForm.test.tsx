import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import "@/i18n";
import { missingRequiredParams } from "@/lib/params";
import type { Param } from "@/lib/types";
import ParamForm from "./ParamForm";

const params: Param[] = [
  { id: "repo", label: "Repository", type: "string", scope: "warmup", required: true },
  { id: "depth", label: "Depth", type: "number", scope: "warmup" },
  { id: "cache", label: "Use cache", type: "boolean", scope: "install" },
  {
    id: "branch",
    label: "Branch",
    type: "select",
    scope: "warmup",
    options: ["main", "dev"],
  },
  { id: "token", label: "Token", type: "string", scope: "warmup", secret: true },
];

describe("ParamForm", () => {
  it("renders one field per param with the control matching its type", () => {
    render(<ParamForm params={params} values={{}} onChange={() => {}} />);

    const repo = screen.getByLabelText("Repository");
    expect(repo.getAttribute("type")).toBe("text");

    const depth = screen.getByLabelText("Depth");
    expect(depth.getAttribute("type")).toBe("number");

    const cache = screen.getByRole("switch", { name: "Use cache" });
    expect(cache.getAttribute("aria-checked")).toBe("false");

    const branch = screen.getByLabelText("Branch");
    expect(branch.tagName).toBe("SELECT");

    const token = screen.getByLabelText("Token");
    expect(token.getAttribute("type")).toBe("password");
  });

  it("falls back to the param id when no label is declared", () => {
    render(
      <ParamForm
        params={[{ id: "repo_url", type: "string", scope: "warmup" }]}
        values={{}}
        onChange={() => {}}
      />,
    );
    expect(screen.getByLabelText("repo_url")).toBeTruthy();
  });

  it("shows the declared options in a select", () => {
    render(<ParamForm params={params} values={{ branch: "dev" }} onChange={() => {}} />);
    const branch = screen.getByLabelText("Branch") as HTMLSelectElement;
    expect(branch.value).toBe("dev");
    const options = Array.from(branch.options).map((o) => o.value);
    expect(options).toEqual(["", "main", "dev"]);
  });

  it("emits updated values on edit and toggle", async () => {
    const user = userEvent.setup();
    const changes: Record<string, string>[] = [];
    render(
      <ParamForm
        params={params}
        values={{ repo: "", cache: "true" }}
        onChange={(v) => changes.push(v)}
      />,
    );

    await user.type(screen.getByLabelText("Repository"), "g");
    expect(changes.at(-1)).toEqual({ repo: "g", cache: "true" });

    await user.click(screen.getByRole("switch", { name: "Use cache" }));
    expect(changes.at(-1)).toEqual({ repo: "", cache: "false" });
  });

  it("masks secret values but keeps them editable", () => {
    render(<ParamForm params={params} values={{ token: "***" }} onChange={() => {}} />);
    const token = screen.getByLabelText("Token") as HTMLInputElement;
    expect(token.type).toBe("password");
    expect(token.value).toBe("***");
  });

  it("lists required params with empty values", () => {
    expect(missingRequiredParams(params, {})).toEqual(["repo"]);
    expect(missingRequiredParams(params, { repo: "x" })).toEqual([]);
    expect(missingRequiredParams(params, { repo: "  " })).toEqual(["repo"]);
  });

  it("flags required-but-empty fields when showErrors is set", () => {
    render(<ParamForm params={params} values={{}} onChange={() => {}} showErrors />);
    const repo = screen.getByLabelText("Repository");
    expect(repo.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByText(/required/i)).toBeTruthy();
  });

  it("does not flag fields before errors are requested", () => {
    render(<ParamForm params={params} values={{}} onChange={() => {}} />);
    expect(screen.getByLabelText("Repository").getAttribute("aria-invalid")).toBeNull();
    expect(screen.queryByText(/required/i)).toBeNull();
  });
});
