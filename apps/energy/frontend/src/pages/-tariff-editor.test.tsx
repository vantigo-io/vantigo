import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Tariff, TariffInput } from "../api/tariffs";
import { formPath } from "../lib/tariff-draft";
import { stubFetch } from "../test/fetch";
import { TariffEditor, type TariffEditorState } from "./-tariff-editor";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const unused = {
  rate: null,
  rateNight: null,
  dayFrom: null,
  dayTo: null,
  weekdaysOnly: null,
  tiers: null,
  rateReduced: null,
  rateBusiness: null,
};

const renderEditor = (state: TariffEditorState, answer?: (url: string, init?: RequestInit) => Response | undefined) => {
  const fetchMock = stubFetch(async (input, init) => answer?.(String(input), init) ?? json({ id: 1001, name: "x" }));
  const onClose = vi.fn();
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <TariffEditor state={state} onClose={onClose} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  const sent = () => {
    const call = fetchMock.mock.calls.find(([, init]) => init?.method === "POST" || init?.method === "PUT");
    return call ? { url: String(call[0]), method: call[1]?.method, body: JSON.parse(String(call[1]?.body)) } : null;
  };
  return { fetchMock, onClose, sent: sent as () => { url: string; method?: string; body: TariffInput } | null };
};

const drawer = () => screen.getByRole("dialog");
const version = (number: number) => within(drawer()).getByRole("region", { name: `Version ${number}` });
const component = (name: string, scope: HTMLElement = drawer()) => within(scope).getByRole("region", { name });

const addComponent = async (kind: string, scope: HTMLElement = version(1)) => {
  await userEvent.click(within(scope).getByRole("button", { name: "Add component" }));
  await userEvent.click(await screen.findByRole("menuitem", { name: kind }));
};

const fillHeader = async (name = "Tariff") => {
  await userEvent.type(within(drawer()).getByRole("textbox", { name: "Name" }), name);
  fireEvent.change(within(version(1)).getByLabelText(/^Valid from/), { target: { value: "2026-01-01" } });
};

describe("TariffEditor", () => {
  it("starts a power tariff with the spot price, adds a markup, and sends null for every field a kind does not use", async () => {
    const { sent, onClose } = renderEditor({ mode: "create" });
    await fillHeader("Spot 2026");
    expect(component("Spot price")).toBeInTheDocument();
    expect(within(component("Spot price")).queryByRole("textbox", { name: /NOK/ })).not.toBeInTheDocument();
    await addComponent("Markup");
    await userEvent.type(within(component("Markup")).getByRole("textbox", { name: "Markup (NOK/kWh)" }), "0.05");
    await addComponent("Monthly fee");
    await userEvent.type(within(component("Monthly fee")).getByRole("textbox", { name: "Amount (NOK/month)" }), "39");
    // A power version holds one energy kind: the menu no longer offers the other.
    await userEvent.click(within(version(1)).getByRole("button", { name: "Add component" }));
    expect(screen.queryByRole("menuitem", { name: "Fixed price" })).not.toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Markup" })).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");

    await userEvent.click(within(drawer()).getByRole("button", { name: "Create tariff" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(sent()).toEqual({
      url: "/api/v1/energy/tariffs",
      method: "POST",
      body: {
        kind: "power",
        name: "Spot 2026",
        description: null,
        versions: [
          {
            validFrom: "2026-01-01",
            validTo: null,
            components: [
              { kind: "spot_energy", name: "Spot price", ...unused },
              { kind: "markup", name: "Markup", ...unused, rate: 0.05 },
              { kind: "monthly_fee", name: "Monthly fee", ...unused, rate: 39 },
            ],
          },
        ],
      },
    });
  });

  it("shows each grid kind's fields, the night window and the capacity steps", async () => {
    const { sent, onClose } = renderEditor({ mode: "create" });
    await userEvent.click(within(drawer()).getByRole("radio", { name: "Grid tariff" }));
    await fillHeader("Grid 2026");
    expect(within(version(1)).getByText("No components yet. A version needs at least one.")).toBeInTheDocument();

    await addComponent("Energy charge");
    const energy = component("Energy charge");
    await userEvent.type(within(energy).getByRole("textbox", { name: "Day rate (NOK/kWh)" }), "0.4");
    expect(within(energy).queryByRole("textbox", { name: "Night rate (NOK/kWh)" })).not.toBeInTheDocument();
    await userEvent.click(within(energy).getByRole("switch", { name: /^Night and weekend rate/ }));
    await userEvent.type(within(energy).getByRole("textbox", { name: "Night rate (NOK/kWh)" }), "0.3");
    await userEvent.type(within(energy).getByRole("textbox", { name: "Day starts (hour)" }), "6");
    await userEvent.type(within(energy).getByRole("textbox", { name: "Day ends (hour, not included)" }), "22");
    await userEvent.click(within(energy).getByRole("checkbox", { name: "Day rate on weekdays only" }));

    await addComponent("Capacity charge");
    const capacity = component("Capacity charge");
    expect(within(capacity).getByRole("textbox", { name: "Step 1: from kW" })).toHaveValue("0");
    expect(within(capacity).getByRole("button", { name: "Remove step 1" })).toBeDisabled();
    await userEvent.type(within(capacity).getByRole("textbox", { name: "Step 1: NOK per month" }), "150");
    await userEvent.click(within(capacity).getByRole("button", { name: "Add step" }));
    await userEvent.type(within(capacity).getByRole("textbox", { name: "Step 2: from kW" }), "2");
    await userEvent.type(within(capacity).getByRole("textbox", { name: "Step 2: NOK per month" }), "250");
    await userEvent.click(within(capacity).getByRole("button", { name: "Add step" }));
    await userEvent.click(within(capacity).getByRole("button", { name: "Remove step 3" }));
    expect(within(capacity).queryByRole("textbox", { name: "Step 3: from kW" })).not.toBeInTheDocument();

    await addComponent("Fixed charge");
    await userEvent.type(within(component("Fixed charge")).getByRole("textbox", { name: "Amount (NOK/month)" }), "100");
    await addComponent("Electricity tax");
    const tax = component("Electricity tax");
    await userEvent.type(within(tax).getByRole("textbox", { name: "Ordinary rate (NOK/kWh)" }), "0.0713");
    await userEvent.type(within(tax).getByRole("textbox", { name: "Reduced rate (NOK/kWh)" }), "0.0063");
    await addComponent("Enova levy");
    const enova = component("Enova levy");
    await userEvent.type(within(enova).getByRole("textbox", { name: "Household rate (NOK/kWh)" }), "0.01");
    await userEvent.type(within(enova).getByRole("textbox", { name: "Business rate (NOK/year)" }), "800");

    // The energy, capacity, tax and levy kinds are held once per version; the fixed charge and the fee may repeat.
    await userEvent.click(within(version(1)).getByRole("button", { name: "Add component" }));
    expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Fixed charge", "Monthly fee"]);
    await userEvent.keyboard("{Escape}");

    await userEvent.click(within(drawer()).getByRole("button", { name: "Create tariff" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(sent()?.body.versions[0]?.components).toEqual([
      {
        kind: "grid_energy",
        name: "Energy charge",
        ...unused,
        rate: 0.4,
        rateNight: 0.3,
        dayFrom: 6,
        dayTo: 22,
        weekdaysOnly: true,
      },
      {
        kind: "grid_capacity",
        name: "Capacity charge",
        ...unused,
        tiers: [
          { fromKw: 0, perMonth: 150 },
          { fromKw: 2, perMonth: 250 },
        ],
      },
      { kind: "grid_fixed", name: "Fixed charge", ...unused, rate: 100 },
      { kind: "electricity_tax", name: "Electricity tax", ...unused, rate: 0.0713, rateReduced: 0.0063 },
      { kind: "enova_levy", name: "Enova levy", ...unused, rate: 0.01, rateBusiness: 800 },
    ]);
  });

  it("adds and removes versions, moves and removes components", async () => {
    const { sent, onClose } = renderEditor({ mode: "create" });
    await fillHeader();
    fireEvent.change(within(version(1)).getByLabelText("Valid until (not included)"), {
      target: { value: "2026-07-01" },
    });
    await addComponent("Markup");
    await userEvent.type(within(component("Markup")).getByRole("textbox", { name: "Markup (NOK/kWh)" }), "0.1");
    await userEvent.click(within(component("Markup")).getByRole("button", { name: "Move Markup up" }));
    expect(within(component("Markup")).getByRole("button", { name: "Move Markup up" })).toBeDisabled();

    await userEvent.click(within(drawer()).getByRole("button", { name: "Add version" }));
    // The new version starts where the last one ends.
    expect(within(version(2)).getByLabelText(/^Valid from/)).toHaveValue("2026-07-01");
    await addComponent("Fixed price", version(2));
    await userEvent.type(
      within(component("Fixed price", version(2))).getByRole("textbox", { name: "Price (NOK/kWh)" }),
      "1.2",
    );
    await userEvent.click(within(drawer()).getByRole("button", { name: "Add version" }));
    expect(version(3)).toBeInTheDocument();
    await userEvent.click(within(version(3)).getByRole("button", { name: "Remove version" }));
    expect(within(drawer()).queryByRole("region", { name: "Version 3" })).not.toBeInTheDocument();
    await userEvent.click(within(component("Spot price")).getByRole("button", { name: "Remove Spot price" }));
    await addComponent("Spot price");

    await userEvent.click(within(drawer()).getByRole("button", { name: "Create tariff" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    const versions = sent()?.body.versions ?? [];
    expect(versions.map((item) => [item.validFrom, item.validTo, item.components.map((c) => c.kind)])).toEqual([
      ["2026-01-01", "2026-07-01", ["markup", "spot_energy"]],
      ["2026-07-01", null, ["fixed_energy"]],
    ]);
  });

  it("shows the server's 400 on the fields it names, and lists what it cannot place", async () => {
    const { onClose } = renderEditor({ mode: "create" }, (_url, init) =>
      init?.method === "POST"
        ? json(
            {
              title: "One or more validation errors occurred.",
              errors: {
                name: ["A tariff named Tariff exists."],
                "versions[0].components[1].rate": ["A rate is below 1 000 000."],
                "versions[0].components": ["A power version holds exactly one of spot_energy and fixed_energy."],
                "versions[0].components[2].tiers[1].fromKw": ["Not shown here."],
              },
            },
            400,
          )
        : undefined,
    );
    await fillHeader();
    await addComponent("Markup");
    await userEvent.type(within(component("Markup")).getByRole("textbox", { name: "Markup (NOK/kWh)" }), "5");
    await userEvent.click(within(drawer()).getByRole("button", { name: "Create tariff" }));

    expect(await within(drawer()).findByText("A tariff named Tariff exists.")).toBeInTheDocument();
    expect(within(drawer()).getByRole("textbox", { name: "Name" })).toHaveAttribute("aria-invalid", "true");
    expect(within(component("Markup")).getByText("A rate is below 1 000 000.")).toBeInTheDocument();
    expect(within(component("Markup")).getByRole("textbox", { name: "Markup (NOK/kWh)" })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    expect(
      within(version(1)).getByText("A power version holds exactly one of spot_energy and fixed_energy."),
    ).toBeInTheDocument();
    const alert = within(drawer()).getByRole("alert");
    expect(alert).toHaveTextContent("One or more validation errors occurred.");
    expect(alert).toHaveTextContent("versions[0].components[2].tiers[1].fromKw: Not shown here.");
    expect(alert).not.toHaveTextContent("A tariff named");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("asks for what a kind needs before sending", async () => {
    const { fetchMock } = renderEditor({ mode: "create" });
    await addComponent("Markup");
    await userEvent.click(within(drawer()).getByRole("button", { name: "Create tariff" }));
    expect(await within(drawer()).findByText("Enter a name")).toBeInTheDocument();
    expect(within(version(1)).getByText("Enter the first day the version applies")).toBeInTheDocument();
    expect(within(component("Markup")).getByText("Enter a value")).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("edits a tariff: loads it, keeps the kind fixed and replaces it", async () => {
    const tariff: Tariff = {
      id: 1002,
      kind: "grid",
      name: "Grid household",
      description: "Ours",
      status: "archived",
      createdAt: "2026-01-01T00:00:00Z",
      updatedAt: "2026-01-01T00:00:00Z",
      usage: { supplyPeriods: 0, meteringPoints: 2 },
      versions: [
        {
          id: 1,
          validFrom: "2026-01-01",
          validTo: null,
          components: [
            {
              id: 1,
              position: 0,
              kind: "grid_energy",
              name: "Energiledd",
              rate: 0.4,
              rateNight: 0.3,
              dayFrom: 6,
              dayTo: 22,
              weekdaysOnly: false,
              tiers: null,
              rateReduced: null,
              rateBusiness: null,
            },
          ],
        },
      ],
    };
    const { sent, onClose } = renderEditor({ mode: "edit", tariffId: 1002 }, (url, init) =>
      url === "/api/v1/energy/tariffs/1002" && !init?.method ? json(tariff) : undefined,
    );
    expect(await within(drawer()).findByDisplayValue("Grid household")).toBeInTheDocument();
    expect(within(drawer()).queryByRole("radio", { name: "Power tariff" })).not.toBeInTheDocument();
    expect(within(drawer()).getByText("A tariff's kind cannot be changed.")).toBeInTheDocument();
    const energy = component("Energy charge");
    expect(within(energy).getByRole("switch", { name: /^Night and weekend rate/ })).toBeChecked();
    expect(within(energy).getByRole("textbox", { name: "Night rate (NOK/kWh)" })).toHaveValue("0.3");
    // Turning the night rate off sends its four fields as null.
    await userEvent.click(within(energy).getByRole("switch", { name: /^Night and weekend rate/ }));
    await userEvent.click(within(drawer()).getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(sent()).toEqual({
      url: "/api/v1/energy/tariffs/1002",
      method: "PUT",
      body: {
        kind: "grid",
        name: "Grid household",
        description: "Ours",
        versions: [
          {
            validFrom: "2026-01-01",
            validTo: null,
            components: [{ kind: "grid_energy", name: "Energiledd", ...unused, rate: 0.4 }],
          },
        ],
      },
    });
  });

  it("reads the server's keys as the form's paths", () => {
    expect(formPath("versions[1].components[2].rate")).toBe("versions.1.components.2.rate");
    expect(formPath("versions[0].components[0].tiers[1].fromKw")).toBe("versions.0.components.0.tiers.1.fromKw");
    expect(formPath("name")).toBe("name");
  });
});
