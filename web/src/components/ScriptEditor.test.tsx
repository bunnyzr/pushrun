import { render, screen } from "@testing-library/react";
import { EditorView } from "@codemirror/view";
import { describe, expect, it, vi } from "vitest";
import ScriptEditor from "./ScriptEditor";

function viewOf(container: HTMLElement): EditorView {
  const dom = container.querySelector(".cm-editor");
  const view = dom && EditorView.findFromDOM(dom as HTMLElement);
  if (!view) throw new Error("no EditorView mounted");
  return view;
}

describe("ScriptEditor", () => {
  it("renders the script with a line-number gutter and shell content", () => {
    const { container } = render(
      <ScriptEditor value={"echo one\nmake build"} onChange={() => {}} aria-label="Run 1" />,
    );
    expect(container.querySelector(".cm-gutters")).toBeTruthy();
    expect(container.querySelector(".cm-lineNumbers")).toBeTruthy();
    const content = screen.getByLabelText("Run 1");
    expect(content.textContent).toContain("echo one");
    expect(content.textContent).toContain("make build");
  });

  it("notifies onChange when the document is edited", () => {
    const onChange = vi.fn();
    const { container } = render(<ScriptEditor value="a" onChange={onChange} />);
    viewOf(container).dispatch({ changes: { from: 1, insert: "b" } });
    expect(onChange).toHaveBeenCalledWith("ab");
  });

  it("adopts a new external value after the draft resets", () => {
    const { container, rerender } = render(<ScriptEditor value="first" onChange={() => {}} />);
    rerender(<ScriptEditor value="second" onChange={() => {}} />);
    expect(viewOf(container).state.doc.toString()).toBe("second");
  });
});
