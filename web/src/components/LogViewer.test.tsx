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

describe("LogViewer search", () => {
  const lines = ["alpha hit", "beta", "gamma hit", "delta"];

  it("highlights matches without hiding context and counts them", async () => {
    const user = userEvent.setup();
    render(<LogViewer lines={lines} />);
    await user.type(screen.getByPlaceholderText("Search loaded logs…"), "hit");
    // Context lines stay visible — search highlights, never filters.
    expect(screen.getByText("beta")).toBeTruthy();
    expect(screen.getByText("delta")).toBeTruthy();
    expect(screen.getAllByText("hit")).toHaveLength(2);
    expect(screen.getByText("1/2")).toBeTruthy();
  });

  it("navigates matches with Enter and Shift+Enter", async () => {
    const user = userEvent.setup();
    render(<LogViewer lines={lines} />);
    const input = screen.getByPlaceholderText("Search loaded logs…");
    await user.type(input, "hit");
    expect(screen.getByText("1/2")).toBeTruthy();
    await user.type(input, "{Enter}");
    expect(screen.getByText("2/2")).toBeTruthy();
    await user.type(input, "{Shift>}{Enter}{/Shift}");
    expect(screen.getByText("1/2")).toBeTruthy();
  });

  it("shows a no-match note and a 0/0 counter", async () => {
    const user = userEvent.setup();
    render(<LogViewer lines={lines} />);
    await user.type(screen.getByPlaceholderText("Search loaded logs…"), "zzz");
    expect(screen.getByText("0/0")).toBeTruthy();
    expect(screen.getByText("No lines match the search.")).toBeTruthy();
    // All lines remain visible even with no matches.
    expect(screen.getByText("alpha hit")).toBeTruthy();
  });
});

describe("LogViewer tone and tail controls", () => {
  it("colors lines by log level", () => {
    render(
      <LogViewer lines={["INFO boot", "WARN slow", "ERROR broken", "plain"]} />,
    );
    expect(screen.getByText("ERROR broken").className).toContain("text-red-300");
    expect(screen.getByText("WARN slow").className).toContain("text-amber-300");
    expect(screen.getByText("INFO boot").className).toContain("text-sky-200/90");
    expect(screen.getByText("plain").className).not.toContain("text-red-300");
  });

  it("respects the case-sensitivity toggle", async () => {
    const user = userEvent.setup();
    render(<LogViewer lines={["Hit", "hit"]} />);
    await user.type(screen.getByPlaceholderText("Search loaded logs…"), "hit");
    expect(screen.getByText("1/2")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Match case" }));
    expect(screen.getByText("1/1")).toBeTruthy();
  });

  it("reports tail changes", async () => {
    const user = userEvent.setup();
    const tails: number[] = [];
    render(
      <LogViewer lines={["a"]} tail={{ value: 500, onChange: (n) => tails.push(n) }} />,
    );
    await user.selectOptions(screen.getByRole("combobox", { name: "Tail lines" }), "1000");
    expect(tails).toEqual([1000]);
  });
});
