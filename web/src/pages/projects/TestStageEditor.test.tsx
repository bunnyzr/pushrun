import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { EditorView } from "@codemirror/view";
import { describe, expect, it, vi } from "vitest";
import "@/i18n";
import type { Step } from "@/lib/types";
import TestStageEditor from "./TestStageEditor";

function viewOf(container: HTMLElement): EditorView {
  const dom = container.querySelector(".cm-editor");
  const view = dom && EditorView.findFromDOM(dom as HTMLElement);
  if (!view) throw new Error("no EditorView mounted");
  return view;
}

function typeScript(container: HTMLElement, text: string) {
  const view = viewOf(container);
  act(() => {
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text } });
  });
}

describe("TestStageEditor", () => {
  it("keeps a whitespace-only script as typed instead of wiping it", () => {
    const onChange = vi.fn();
    const { container } = render(<TestStageEditor value={undefined} onChange={onChange} />);

    typeScript(container, "  \n  ");

    // The editor keeps the whitespace and the parent hears "no test stage".
    expect(viewOf(container).state.doc.toString()).toBe("  \n  ");
    expect(onChange).toHaveBeenCalledWith(undefined);
  });

  it("keeps name/timeout edits made while the script is empty", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { container } = render(<TestStageEditor value={undefined} onChange={onChange} />);

    const nameInput = screen.getByLabelText("Name", { exact: false });
    await user.clear(nameInput);
    await user.type(nameInput, "unit");
    await user.type(screen.getByLabelText("Timeout (s)"), "60");
    expect(onChange).toHaveBeenLastCalledWith(undefined);

    // Typing the script afterwards carries the earlier name/timeout along.
    typeScript(container, "make test");
    expect(onChange).toHaveBeenLastCalledWith({ name: "unit", run: "make test", timeout: 60 });
    expect((nameInput as HTMLInputElement).value).toBe("unit");
  });

  it("loads an existing test step and drops it when the script is cleared", () => {
    const onChange = vi.fn();
    const step: Step = { name: "unit", run: "make test", timeout: 30 };
    const { container } = render(<TestStageEditor value={step} onChange={onChange} />);

    expect((screen.getByLabelText("Name", { exact: false }) as HTMLInputElement).value).toBe(
      "unit",
    );
    expect(viewOf(container).state.doc.toString()).toBe("make test");

    typeScript(container, "");
    expect(onChange).toHaveBeenLastCalledWith(undefined);
  });
});
