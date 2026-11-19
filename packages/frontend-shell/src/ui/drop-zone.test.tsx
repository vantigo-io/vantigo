import { MantineProvider } from "@mantine/core";
import { createEvent, fireEvent, render, screen } from "@testing-library/react";
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

const filesDrag = { dataTransfer: { types: ["Files"] } };

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

  it("passes an empty list when nothing was dropped", () => {
    const onDropFiles = vi.fn();
    renderZone({ onDropFiles });
    fireEvent.drop(screen.getByRole("group"));
    expect(onDropFiles).toHaveBeenCalledWith([]);
  });

  it("accepts a file drag and highlights until it leaves", () => {
    renderZone();
    const zone = screen.getByRole("group");
    expect(zone).not.toHaveAttribute("data-over");
    expect(fireEvent.dragOver(zone, filesDrag)).toBe(false);
    expect(zone).toHaveAttribute("data-over", "true");
    fireEvent.dragLeave(zone);
    expect(zone).not.toHaveAttribute("data-over");
  });

  it("ignores a drag that carries no files", () => {
    renderZone();
    const zone = screen.getByRole("group");
    expect(fireEvent.dragOver(zone, { dataTransfer: { types: ["text/plain"] } })).toBe(true);
    expect(zone).not.toHaveAttribute("data-over");
  });

  it("stays highlighted when the drag moves onto a child", () => {
    renderZone();
    const zone = screen.getByRole("group");
    fireEvent.dragOver(zone, filesDrag);
    const leave = createEvent.dragLeave(zone);
    Object.defineProperty(leave, "relatedTarget", { value: screen.getByText("Drop a file") });
    fireEvent(zone, leave);
    expect(zone).toHaveAttribute("data-over", "true");
  });

  it("clears the highlight after a drop", () => {
    renderZone();
    const zone = screen.getByRole("group");
    fireEvent.dragOver(zone, filesDrag);
    fireEvent.drop(zone);
    expect(zone).not.toHaveAttribute("data-over");
  });

  it("neither highlights nor accepts a drop when disabled", () => {
    const onDropFiles = vi.fn();
    renderZone({ disabled: true, onDropFiles });
    const zone = screen.getByRole("group");
    expect(zone).toHaveAttribute("data-disabled", "true");
    fireEvent.dragOver(zone, filesDrag);
    expect(zone).not.toHaveAttribute("data-over");
    fireEvent.drop(zone, { dataTransfer: { files: [new File(["x"], "a.csv")] } });
    expect(onDropFiles).not.toHaveBeenCalled();
  });

  it("marks an invalid zone", () => {
    renderZone({ invalid: true });
    expect(screen.getByRole("group")).toHaveAttribute("data-invalid", "true");
  });

  it("merges the caller's style", () => {
    renderZone({ style: { opacity: 0.5 } });
    const zone = screen.getByRole("group");
    expect(zone.style.opacity).toBe("0.5");
    expect(zone.getAttribute("style")).toContain("dashed");
  });
});
