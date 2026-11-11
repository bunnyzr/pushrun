import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import "@/i18n";
import LogFileTree from "./LogFileTree";

const files = [
  { path: "a.log", size: 2 },
  { path: "dir/b.log", size: 3 },
  { path: "dir/nested/c.log", size: 4 },
];

describe("LogFileTree", () => {
  it("nests log files and collapses a directory", async () => {
    const user = userEvent.setup();
    render(
      <LogFileTree
        files={files}
        selectedFile={null}
        filter=""
        onFilterChange={vi.fn()}
        onSelect={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    expect(screen.getByRole("button", { name: "a.log" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "b.log" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "c.log" })).toBeTruthy();

    await user.click(screen.getByRole("button", { name: /^dir$/ }));
    expect(screen.queryByRole("button", { name: "b.log" })).toBeNull();
    expect(screen.queryByRole("button", { name: "c.log" })).toBeNull();
  });

  it("flattens matches while filtering and reports the selected file", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(
      <LogFileTree
        files={files}
        selectedFile={null}
        filter="c.log"
        onFilterChange={vi.fn()}
        onSelect={onSelect}
        onDelete={vi.fn()}
      />,
    );

    expect(screen.getByRole("button", { name: "dir/nested/c.log" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "a.log" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "dir/nested/c.log" }));
    expect(onSelect).toHaveBeenCalledWith("dir/nested/c.log");
  });
});
