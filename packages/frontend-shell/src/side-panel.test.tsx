import { MantineProvider } from "@mantine/core";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppShellLayout } from "./app-shell-layout";
import { type SidePanel, useSidePanelController, useSidePanelShown } from "./side-panel";

let wide = true;
const mounts: string[] = [];
let shownNow: Record<string, boolean> = {};

const setViewport = (isWide: boolean) => {
  wide = isWide;
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: query.includes("62em") ? wide : false,
    media: query,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
  })) as never;
};

const Body = ({ name }: { name: string }) => {
  const shown = useSidePanelShown();
  shownNow[name] = shown;
  useEffect(() => {
    mounts.push(name);
  }, [name]);
  return <textarea aria-label={`draft ${name}`} />;
};

const panels = (hiddenB = false): SidePanel[] => [
  { key: "a", label: "Alpha", icon: null, render: () => <Body name="a" /> },
  { key: "b", label: "Beta", icon: null, hidden: hiddenB, render: () => <Body name="b" /> },
];

const Harness = ({ hiddenB = false }: { hiddenB?: boolean }) => {
  const controller = useSidePanelController();
  return (
    <AppShellLayout
      headerActions={
        <>
          <button type="button" onClick={() => controller.toggle("a")}>
            toggle a
          </button>
          <button type="button" onClick={() => controller.toggle("b")}>
            toggle b
          </button>
        </>
      }
      aside={controller.aside(panels(hiddenB))}
    >
      <div>page</div>
    </AppShellLayout>
  );
};

const view = (hiddenB = false) =>
  render(
    <MantineProvider env="test">
      <Harness hiddenB={hiddenB} />
    </MantineProvider>,
  );

beforeEach(() => {
  mounts.length = 0;
  shownNow = {};
  window.localStorage.clear();
  setViewport(true);
});
afterEach(() => {
  vi.restoreAllMocks();
});

const host = () => document.querySelector("[data-side-panel]") as HTMLElement;
const isOpen = () => !host().hasAttribute("inert");
const split = () => screen.getByRole("separator", { name: "Resize the panels", hidden: true });

describe("SidePanelHost", () => {
  it("mounts a panel when it is first shown, and keeps it mounted across a switch", () => {
    view();
    expect(mounts).toEqual([]);
    expect(isOpen()).toBe(false);

    fireEvent.click(screen.getByText("toggle a"));
    expect(isOpen()).toBe(true);
    expect(mounts).toEqual(["a"]);

    fireEvent.change(screen.getByLabelText("draft a"), { target: { value: "half a reply" } });
    fireEvent.click(screen.getByRole("tab", { name: "Beta" }));
    expect(mounts).toEqual(["a", "b"]);
    expect(shownNow).toEqual({ a: false, b: true });
    fireEvent.click(screen.getByRole("tab", { name: "Alpha" }));
    // Not remounted: the draft is still there.
    expect(mounts).toEqual(["a", "b"]);
    expect(screen.getByLabelText("draft a")).toHaveValue("half a reply");
  });

  it("toggles its own panel closed and shows the tab of the other", () => {
    view();
    fireEvent.click(screen.getByText("toggle a"));
    fireEvent.click(screen.getByText("toggle b"));
    expect(isOpen()).toBe(true);
    expect(screen.getByRole("tab", { name: "Beta" })).toHaveAttribute("aria-selected", "true");
    fireEvent.click(screen.getByText("toggle b"));
    expect(isOpen()).toBe(false);
    // The panel stays mounted, hidden from the tab order.
    expect(screen.getByLabelText("draft b", { selector: "textarea" })).toBeInTheDocument();
  });

  it("stacks both panels on a wide screen, with a divider moved by the arrow keys", () => {
    view();
    fireEvent.click(screen.getByText("toggle a"));
    expect(screen.queryByRole("separator", { name: "Resize the panels" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Show both panels" }));
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
    expect(mounts).toContain("b");
    expect(shownNow).toEqual({ a: true, b: true });

    vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue({ height: 600, top: 0 } as DOMRect);
    expect(split()).toHaveAttribute("aria-valuenow", "50");
    fireEvent.keyDown(split(), { key: "ArrowDown" });
    expect(split()).toHaveAttribute("aria-valuenow", "54");
    fireEvent.keyDown(split(), { key: "ArrowUp" });
    fireEvent.keyDown(split(), { key: "ArrowUp" });
    expect(split()).toHaveAttribute("aria-valuenow", "46");
    expect(window.localStorage.getItem("vantigo.sidePanel.mode")).toBe("both");
  });

  it("keeps 200 px for each panel", () => {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue({ height: 600, top: 0 } as DOMRect);
    window.localStorage.setItem("vantigo.sidePanel.mode", "both");
    view();
    fireEvent.click(screen.getByText("toggle a"));
    for (let i = 0; i < 40; i++) fireEvent.keyDown(split(), { key: "ArrowUp" });
    // 200 / 600
    expect(split()).toHaveAttribute("aria-valuenow", "33");
    for (let i = 0; i < 40; i++) fireEvent.keyDown(split(), { key: "ArrowDown" });
    // (600 - 200 - 8) / 600
    expect(split()).toHaveAttribute("aria-valuenow", "65");
    // A drag past the edge is held at the minimum too.
    fireEvent.pointerDown(split(), { pointerId: 1 });
    fireEvent.pointerMove(split(), { pointerId: 1, clientY: 10 });
    expect(split()).toHaveAttribute("aria-valuenow", "33");
  });

  it("falls back to one panel below md, whatever the remembered mode", () => {
    setViewport(false);
    window.localStorage.setItem("vantigo.sidePanel.mode", "both");
    view();
    fireEvent.click(screen.getByText("toggle a"));
    expect(screen.getByRole("tab", { name: "Alpha" })).toBeInTheDocument();
    expect(shownNow).toEqual({ a: true });
    expect(mounts).toEqual(["a"]);
  });

  it("shows no tabs for a single visible panel", () => {
    const { rerender } = view(true);
    fireEvent.click(screen.getByText("toggle a"));
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
    expect(isOpen()).toBe(true);
    rerender(
      <MantineProvider env="test">
        <Harness hiddenB />
      </MantineProvider>,
    );
    expect(isOpen()).toBe(true);
  });

  it("resizes by the keyboard and remembers the width", () => {
    view();
    fireEvent.click(screen.getByText("toggle a"));
    const handle = screen.getByRole("separator", { name: "Resize the side panel" });
    expect(handle).toHaveAttribute("aria-valuenow", "400");
    fireEvent.keyDown(handle, { key: "ArrowLeft" });
    expect(handle).toHaveAttribute("aria-valuenow", "424");
    expect(window.localStorage.getItem("vantigo.sidePanel.width")).toBe("424");
  });

  it("reads the Assistant's old width once and moves it to the new key", () => {
    window.localStorage.setItem("vantigo.assistant.panelWidth", "520");
    view();
    fireEvent.click(screen.getByText("toggle a"));
    expect(screen.getByRole("separator", { name: "Resize the side panel" })).toHaveAttribute("aria-valuenow", "520");
    expect(window.localStorage.getItem("vantigo.sidePanel.width")).toBe("520");
  });

  it("remembers the panel last shown", () => {
    view();
    fireEvent.click(screen.getByText("toggle b"));
    expect(window.localStorage.getItem("vantigo.sidePanel.last")).toBe("b");
  });

  it("works when storage throws", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("full");
    });
    view();
    act(() => {
      fireEvent.click(screen.getByText("toggle a"));
    });
    const handle = screen.getByRole("separator", { name: "Resize the side panel" });
    expect(handle).toHaveAttribute("aria-valuenow", "400");
    fireEvent.keyDown(handle, { key: "ArrowLeft" });
    expect(handle).toHaveAttribute("aria-valuenow", "424");
    expect(within(host()).getByLabelText("draft a")).toBeInTheDocument();
  });
});
