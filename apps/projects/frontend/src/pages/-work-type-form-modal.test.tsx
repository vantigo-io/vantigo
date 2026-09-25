import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { WorkType } from "../api/projects";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { WorkTypeFormModal, type WorkTypeModalState } from "./-work-type-form-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** Literally what the server sends for a type. */
const overtime: WorkType = {
  id: 11,
  projectId: 7,
  name: "Overtid 50 %",
  billMultiplierPercent: 150,
  costMultiplierPercent: 140,
  active: true,
  createdAt: "2026-09-01T08:00:00Z",
  updatedAt: "2026-09-01T08:00:00Z",
};

const stubWrites = (status = 200) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname.startsWith("/api/v1/projects/7/work-types") && init?.method) {
      return Promise.resolve(
        status === 200 ? jsonResponse(200, overtime) : jsonResponse(status, { title: "Work type exists", status }),
      );
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const renderModal = (state: WorkTypeModalState | null) => {
  const onClose = vi.fn<() => void>();
  renderWithProviders(<WorkTypeFormModal projectId={7} state={state} onClose={onClose} />);
  return { onClose };
};

/** The body of the one write with this method — never "the last fetch". */
const written = (fetchMock: ReturnType<typeof stubFetch>, method: string) => {
  const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === method) ?? [];
  return {
    url: url === undefined ? undefined : String(url),
    body: init?.body ? JSON.parse(String(init.body)) : undefined,
  };
};

const setNumber = async (label: string, value: string) => {
  const input = screen.getByRole("textbox", { name: label });
  await userEvent.clear(input);
  await userEvent.type(input, value);
};

describe("WorkTypeFormModal", () => {
  it("starts a new type at 100 % on both multipliers", () => {
    stubWrites();
    renderModal({ mode: "create" });

    expect(screen.getByRole("textbox", { name: "Bill multiplier" })).toHaveValue("100 %");
    expect(screen.getByRole("textbox", { name: "Cost multiplier" })).toHaveValue("100 %");
    expect(screen.queryByRole("switch", { name: "Active" })).not.toBeInTheDocument();
  });

  it("adds a type with its name trimmed and both multipliers, explaining what a percentage does", async () => {
    const fetchMock = stubWrites();
    const { onClose } = renderModal({ mode: "create" });

    expect(
      screen.getByText("150 % bills and costs one and a half times the rate; 100 % is the rate as it stands."),
    ).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "  Overtid 50 %  ");
    await setNumber("Bill multiplier", "150");
    await setNumber("Cost multiplier", "125");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(written(fetchMock, "POST")).toEqual({
      url: "/api/v1/projects/7/work-types",
      body: { name: "Overtid 50 %", billMultiplierPercent: 150, costMultiplierPercent: 125 },
    });
  });

  it("shows a taken name on the name field", async () => {
    stubWrites(409);
    const { onClose } = renderModal({ mode: "create" });

    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "overtid 50 %");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("This project already has a work type with that name.")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Name" })).toHaveAttribute("aria-invalid", "true");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("deactivates a type through its Active switch, sending the rest as it stands", async () => {
    const fetchMock = stubWrites();
    renderModal({ mode: "edit", workType: overtime });

    const dialog = screen.getByRole("dialog", { name: "Edit work type" });
    expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveValue("Overtid 50 %");
    await userEvent.click(within(dialog).getByRole("switch", { name: "Active" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save changes" }));

    await waitFor(() =>
      expect(written(fetchMock, "PUT")).toEqual({
        url: "/api/v1/projects/7/work-types/11",
        body: { name: "Overtid 50 %", billMultiplierPercent: 150, costMultiplierPercent: 140, active: false },
      }),
    );
  });

  it("refuses a blank name and a multiplier of nothing without asking the server", async () => {
    const fetchMock = stubWrites();
    renderModal({ mode: "create" });

    await setNumber("Bill multiplier", "0");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("Give the work type a name")).toBeInTheDocument();
    expect(screen.getByText("Must be greater than zero")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
  });

  it("refuses a multiplier above ten times the rate in the server's words", async () => {
    const fetchMock = stubWrites();
    renderModal({ mode: "create" });

    await userEvent.type(screen.getByRole("textbox", { name: "Name" }), "Helg");
    await setNumber("Cost multiplier", "1001");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("Cannot be more than 1000 %")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
  });

  // The server counts a name in characters (runes), not UTF-16 code units:
  // an emoji is one character here as there, though it is two units long.
  it("counts a name in characters, the way the server does", async () => {
    const fetchMock = stubWrites();
    renderModal({ mode: "create" });
    const name = screen.getByRole("textbox", { name: "Name" });

    await userEvent.click(name);
    await userEvent.paste("🌙".repeat(101));
    await userEvent.click(screen.getByRole("button", { name: "Create" }));
    expect(await screen.findByText("A work type name cannot be longer than 100 characters")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);

    await userEvent.clear(name);
    await userEvent.paste("🌙".repeat(100));
    await userEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(written(fetchMock, "POST").body).toMatchObject({ name: "🌙".repeat(100) }));
  });
});
