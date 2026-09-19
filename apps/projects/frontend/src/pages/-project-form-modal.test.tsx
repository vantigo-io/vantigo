import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { Project } from "../api/projects";
import { stubFetch } from "../test/fetch";
import { ProjectFormModal, type ProjectModalState } from "./-project-form-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const customers = [
  { id: 1001, name: "Kverneland" },
  { id: 1002, name: "Equinor" },
];

const project: Project = {
  id: 7,
  code: "KVEWEBS",
  name: "Website",
  description: "The new public site",
  status: "active",
  billingType: "time-and-materials",
  internal: false,
  customerId: 1001,
  customerName: "Kverneland",
  startDate: "2026-01-05",
  endDate: null,
  budgetHours: 120,
  financials: { currency: "NOK", budgetAmount: 50000 },
  capabilities: { canManage: true, canContribute: true, canSeeFinancials: true, canManageMilestones: true },
  billingLinesAvailable: true,
  managers: [],
  revision: 3,
  createdAt: "2026-01-01T10:00:00Z",
  updatedAt: "2026-01-02T10:00:00Z",
};

/** The customers search, the code suggestion and one write; anything else is a 404 the test would notice. */
const stubProjectsApi = (write?: (url: string, init?: RequestInit) => Response) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/customers") {
      return Promise.resolve(jsonResponse(200, { data: customers, pagination: { page: 1, pageSize: 20 } }));
    }
    if (url.pathname === "/api/v1/projects/code-suggestion") {
      const prefix = url.searchParams.get("customerId") ? "KVE" : "INT";
      const name = (url.searchParams.get("name") ?? "").replace(/[^a-z]/gi, "").toUpperCase();
      return Promise.resolve(jsonResponse(200, { code: `${prefix}${name.slice(0, 4)}` }));
    }
    if (init?.method === "POST" || init?.method === "PUT") {
      return Promise.resolve(write?.(url.pathname, init) ?? jsonResponse(200, project));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const renderModal = (state: ProjectModalState | null) => {
  const onClose = vi.fn();
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>
  );
  render(<ProjectFormModal state={state} onClose={onClose} />, { wrapper });
  return { onClose };
};

/** Lets the code suggestion's 300 ms debounce and its response go by. */
const settle = () => act(() => new Promise((resolve) => setTimeout(resolve, 500)));

const pickCustomer = async (name: string) => {
  await userEvent.click(screen.getByRole("combobox", { name: /customer/i }));
  await userEvent.click(await screen.findByRole("option", { name }));
};

const codeInput = () => screen.getByLabelText(/project code/i);

describe("ProjectFormModal", () => {
  it("fills the code with the suggestion as the customer and the name are chosen", async () => {
    stubProjectsApi();
    renderModal({ mode: "create" });

    await pickCustomer("Kverneland");
    await userEvent.type(screen.getByLabelText(/project name/i), "Website");

    await waitFor(() => expect(codeInput()).toHaveValue("KVEWEBS"), { timeout: 3000 });
  });

  it("keeps a code the user typed when the name changes again", async () => {
    stubProjectsApi();
    renderModal({ mode: "create" });

    await pickCustomer("Kverneland");
    await userEvent.type(screen.getByLabelText(/project name/i), "Website");
    await waitFor(() => expect(codeInput()).toHaveValue("KVEWEBS"), { timeout: 3000 });

    await userEvent.clear(codeInput());
    await userEvent.type(codeInput(), "mycode");
    expect(codeInput()).toHaveValue("MYCODE");

    await userEvent.type(screen.getByLabelText(/project name/i), " rebuild");
    await settle();

    expect(codeInput()).toHaveValue("MYCODE");
  });

  it("forces and disables the billing type for an internal project", async () => {
    stubProjectsApi();
    renderModal({ mode: "create" });

    expect(screen.getByRole("radio", { name: "Time and materials" })).toBeChecked();

    await pickCustomer("Internal project");

    expect(screen.getByRole("radio", { name: "Non-billable" })).toBeChecked();
    expect(screen.getByRole("radio", { name: "Time and materials" })).toBeDisabled();
    expect(screen.getByRole("radio", { name: "Fixed price" })).toBeDisabled();
  });

  it("offers the suggestion again once the code field is emptied", async () => {
    stubProjectsApi();
    renderModal({ mode: "create" });

    await pickCustomer("Kverneland");
    await userEvent.type(screen.getByLabelText(/project name/i), "Website");
    await waitFor(() => expect(codeInput()).toHaveValue("KVEWEBS"), { timeout: 3000 });

    await userEvent.clear(codeInput());
    await userEvent.click(await screen.findByRole("button", { name: "Use suggestion: KVEWEBS" }));

    expect(codeInput()).toHaveValue("KVEWEBS");
  });

  it("refuses to create a project until a customer or Internal project is chosen", async () => {
    const fetchMock = stubProjectsApi();
    const { onClose } = renderModal({ mode: "create" });

    await userEvent.type(screen.getByLabelText(/project name/i), "Website");
    await userEvent.type(codeInput(), "WEBSITE");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("Choose a customer, or select Internal project")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "POST")).toBe(false);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("refuses to save an existing project whose customer was cleared", async () => {
    const fetchMock = stubProjectsApi();
    const { onClose } = renderModal({ mode: "edit", project });

    // Picking the customer it already has deselects it — Mantine's own way out.
    await pickCustomer("Kverneland");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    expect(await screen.findByText("Choose a customer, or select Internal project")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "PUT")).toBe(false);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("asks for a fixed price amount only on a fixed-price project", async () => {
    stubProjectsApi();
    renderModal({ mode: "create" });

    expect(screen.queryByLabelText(/fixed price amount/i)).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: "Fixed price" }));

    expect(screen.getByLabelText(/fixed price amount/i)).toBeInTheDocument();
  });

  it("creates a project with the code upper-cased and the empty fields left out", async () => {
    const fetchMock = stubProjectsApi(() => jsonResponse(201, project));
    const { onClose } = renderModal({ mode: "create" });

    await pickCustomer("Equinor");
    await userEvent.type(screen.getByLabelText(/project name/i), "  Website  ");
    await waitFor(() => expect(codeInput()).toHaveValue("KVEWEBS"), { timeout: 3000 });
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    const [, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "POST") ?? [];
    expect(JSON.parse(String(init?.body))).toEqual({
      customerId: 1002,
      name: "Website",
      code: "KVEWEBS",
      billingType: "time-and-materials",
      currency: "NOK",
    });
  });

  it("sends the default bill rate on create", async () => {
    const fetchMock = stubProjectsApi(() => jsonResponse(201, project));
    const { onClose } = renderModal({ mode: "create" });

    await pickCustomer("Equinor");
    await userEvent.type(screen.getByLabelText(/project name/i), "Website");
    await waitFor(() => expect(codeInput()).toHaveValue("KVEWEBS"), { timeout: 3000 });
    await userEvent.type(screen.getByLabelText(/default bill rate/i), "950");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    const [, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "POST") ?? [];
    expect(JSON.parse(String(init?.body))).toMatchObject({ defaultBillRate: 950, currency: "NOK" });
  });

  it("shows a server field error under the code input", async () => {
    stubProjectsApi(() =>
      jsonResponse(400, { title: "Invalid project", errors: { code: ["A project already uses the code KVEWEBS"] } }),
    );
    const { onClose } = renderModal({ mode: "create" });

    await pickCustomer("Kverneland");
    await userEvent.type(screen.getByLabelText(/project name/i), "Website");
    await waitFor(() => expect(codeInput()).toHaveValue("KVEWEBS"), { timeout: 3000 });
    await userEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText("A project already uses the code KVEWEBS")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  // The guard that refuses to drop a fixed price while milestones are a share
  // of it answers on `billingType`, which is a segmented control rather than
  // an input Mantine would hang the message on by itself.
  it("shows the fixed-price guard's refusal under the billing type", async () => {
    stubProjectsApi(() =>
      jsonResponse(400, {
        title: "Invalid project",
        errors: {
          billingType: ["A project with billing milestones priced as a share of the fixed price must keep one"],
        },
      }),
    );
    const { onClose } = renderModal({ mode: "edit", project });

    await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));

    const message = await screen.findByText(
      "A project with billing milestones priced as a share of the fixed price must keep one",
    );
    expect(message).toBeInTheDocument();
    expect(message.parentElement).toBe(screen.getByRole("radiogroup", { name: "Billing type" }).parentElement);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("asks before changing the code of an existing project, and cancelling does not save", async () => {
    const fetchMock = stubProjectsApi();
    const { onClose } = renderModal({ mode: "edit", project });

    await userEvent.clear(codeInput());
    await userEvent.type(codeInput(), "KVENEW");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    const confirm = await screen.findByRole("dialog", { name: /change the project code/i });
    expect(within(confirm).getByText(/KVEWEBS/)).toBeInTheDocument();
    await userEvent.click(within(confirm).getByRole("button", { name: "Cancel" }));

    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: /change the project code/i })).not.toBeInTheDocument(),
    );
    expect(fetchMock.actualCalls.some(([, init]) => init?.method === "PUT")).toBe(false);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("saves the changed code once it is confirmed, carrying the revision it was read at", async () => {
    const fetchMock = stubProjectsApi();
    const { onClose } = renderModal({ mode: "edit", project });

    await userEvent.clear(codeInput());
    await userEvent.type(codeInput(), "KVENEW");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    const confirm = await screen.findByRole("dialog", { name: /change the project code/i });
    await userEvent.click(within(confirm).getByRole("button", { name: "Change the code" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    const [url, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "PUT") ?? [];
    expect(String(url)).toBe("/api/v1/projects/7");
    expect(JSON.parse(String(init?.body))).toMatchObject({ code: "KVENEW", revision: 3, budgetAmount: 50000 });
  });

  it("never suggests a code over one an existing project already has", async () => {
    stubProjectsApi();
    renderModal({ mode: "edit", project });

    await userEvent.type(screen.getByLabelText(/project name/i), " rebuild");
    await settle();

    expect(codeInput()).toHaveValue("KVEWEBS");
  });

  it("tells the user to reload when someone else changed the project", async () => {
    stubProjectsApi(() =>
      jsonResponse(409, { title: "Project revision conflict", detail: "Revision 4 is current", status: 409 }),
    );
    const { onClose } = renderModal({ mode: "edit", project });

    await userEvent.type(screen.getByLabelText(/project name/i), " rebuild");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    expect(await screen.findByText(/changed by someone else/i)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it("stops asking for a currency once the fixed price it belonged to is no longer billed", async () => {
    const fetchMock = stubProjectsApi();
    const { onClose } = renderModal({
      mode: "edit",
      project: { ...project, billingType: "fixed-price", financials: { currency: "NOK", fixedPriceAmount: 250000 } },
    });

    // The amount stays in the form's state; it is simply not sent any more,
    // so it must not keep the currency required either.
    await userEvent.click(screen.getByRole("radio", { name: "Time and materials" }));
    await userEvent.clear(screen.getByLabelText(/^currency/i));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(screen.queryByText("A currency is required once an amount is set")).not.toBeInTheDocument();
    const [, init] = fetchMock.actualCalls.find(([, request]) => request?.method === "PUT") ?? [];
    const body = JSON.parse(String(init?.body));
    expect(body).not.toHaveProperty("fixedPriceAmount");
    expect(body).not.toHaveProperty("currency");
  });

  it("hides the amounts from someone who may not see them", () => {
    stubProjectsApi();
    renderModal({
      mode: "edit",
      project: {
        ...project,
        financials: undefined,
        capabilities: { canManage: true, canContribute: true, canSeeFinancials: false, canManageMilestones: true },
      },
    });

    expect(screen.queryByLabelText(/budget amount/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/^currency/i)).not.toBeInTheDocument();
    expect(screen.getByText(/cannot see this project's amounts/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/budget hours/i)).toBeInTheDocument();
  });
});
