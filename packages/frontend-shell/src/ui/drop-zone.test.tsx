import { MantineProvider } from "@mantine/core";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ComponentProps } from "react";
import { describe, expect, it, vi } from "vitest";
import { DropZone } from "./drop-zone";

const renderZone = (props: Partial<ComponentProps<typeof DropZone>> = {}) =>
  render(
    <MantineProvider env="test">
      <DropZone aria-label="Drop here" role="group" onDropFiles={() => {}} {...props}>
        <span>Drop a file</span>
      </DropZone>
    </MantineProvider>,
  );

describe("DropZone", () => {
  it("renders its children", () => {
    renderZone();
    expect(screen.getByText("Drop a file")).toBeInTheDocument();
  });

  it("hands the dropped files to the caller", () => {
    const onDropFiles = vi.fn();
    renderZone({ onDropFiles });
    const file = new File(["x"], "a.csv", { type: "text/csv" });
    fireEvent.drop(screen.getByRole("group"), { dataTransfer: { files: [file] } });
    expect(onDropFiles).toHaveBeenCalledWith([file]);
  });

  it("highlights while a file is dragged over and clears on leave", () => {
    renderZone();
    const zone = screen.getByRole("group");
    expect(zone.style.background).toBe("");
    fireEvent.dragOver(zone);
    expect(zone.getAttribute("style")).toContain("light-dark");
    fireEvent.dragLeave(zone);
    expect(zone.getAttribute("style")).not.toContain("light-dark");
  });

  it("draws a red border when invalid", () => {
    renderZone({ invalid: true });
    expect(screen.getByRole("group").getAttribute("style")).toContain("red-6");
  });
});
