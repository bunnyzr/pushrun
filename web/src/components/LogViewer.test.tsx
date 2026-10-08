import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@/i18n";
import LogViewer from "./LogViewer";

function stubClipboard() {
  const writeText = vi.fn<(text: string) => Promise<void>>().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
  return writeText;
}

describe("LogViewer", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("shows an empty state when there are no lines", () => {
    render(<LogViewer lines={[]} />);
    expect(screen.getByText("No output yet.")).toBeTruthy();
  });

  it("appends streamed lines and shows the terminal status", () => {
    const { rerender } = render(<LogViewer lines={["line one"]} />);
    expect(screen.getByText("line one")).toBeTruthy();
    rerender(<LogViewer lines={["line one", "line two"]} status="SUCCESS" />);
    expect(screen.getByText("line two")).toBeTruthy();
    expect(screen.getByText("Success")).toBeTruthy();
  });

  it("copies a single line", async () => {
    const user = userEvent.setup();
    const writeText = stubClipboard();
    render(<LogViewer lines={["alpha", "beta"]} />);
    await user.click(screen.getAllByRole("button", { name: "Copy line" })[1]!);
    expect(writeText).toHaveBeenCalledWith("beta");
  });

  it("copies the whole log", async () => {
    const user = userEvent.setup();
    const writeText = stubClipboard();
    render(<LogViewer lines={["alpha", "beta"]} />);
    await user.click(screen.getByRole("button", { name: "Copy all" }));
    expect(writeText).toHaveBeenCalledWith("alpha\nbeta");
  });

  it("downloads the log as a blob", async () => {
    const user = userEvent.setup();
    const createObjectURL = vi.fn().mockReturnValue("blob:mock");
    const revokeObjectURL = vi.fn();
    Object.defineProperty(URL, "createObjectURL", { value: createObjectURL, configurable: true });
    Object.defineProperty(URL, "revokeObjectURL", { value: revokeObjectURL, configurable: true });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    render(<LogViewer lines={["alpha", "beta"]} downloadName="build.log" />);
    await user.click(screen.getByRole("button", { name: "Download" }));

    expect(createObjectURL).toHaveBeenCalledTimes(1);
    const blob = createObjectURL.mock.calls[0]![0] as Blob;
    expect(await blob.text()).toBe("alpha\nbeta");
    expect(clickSpy).toHaveBeenCalled();
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:mock");
  });

  it("disengages auto-follow when the user scrolls up and re-engages from the button", async () => {
    const user = userEvent.setup();
    render(<LogViewer lines={["a", "b"]} />);
    const box = screen.getByTestId("log-box");
    Object.defineProperty(box, "scrollHeight", { value: 1000, configurable: true });
    Object.defineProperty(box, "clientHeight", { value: 100, configurable: true });
    box.scrollTop = 0;
    fireEvent.scroll(box);

    const back = await screen.findByRole("button", { name: /Back to bottom/ });
    await user.click(back);
    expect(screen.queryByRole("button", { name: /Back to bottom/ })).toBeNull();
    expect(box.scrollTop).toBe(1000);
  });
});
