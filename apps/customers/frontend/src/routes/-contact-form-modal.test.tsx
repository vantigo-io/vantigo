import { MantineProvider } from "@mantine/core";
import { Notifications, notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ContactFormModal, type ContactModalState } from "./-contact-form-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const renderModal = (state: ContactModalState) => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });

  const wrapper = ({ children }: { children: ReactNode }) => (
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </MantineProvider>
  );

  render(<ContactFormModal state={state} onClose={vi.fn()} />, { wrapper });
};

describe("ContactFormModal name chips", () => {
  afterEach(() => {
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it("hides the optional name fields until their chip is toggled", async () => {
    vi.stubGlobal("fetch", vi.fn());

    renderModal({ mode: "create" });

    expect(screen.getByRole("textbox", { name: /first name/i })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: /last name/i })).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /middle name/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /prefix/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: /suffix/i })).not.toBeInTheDocument();

    await userEvent.click(screen.getByText("+ Middle name"));

    expect(screen.getByRole("textbox", { name: /middle name/i })).toBeInTheDocument();
  });

  it("clears a hidden field's value when its chip is toggled off", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(201, {
        id: 1001,
        firstName: "Anders",
        lastName: "Refsdal",
        middleName: null,
        prefix: null,
        suffix: null,
        phone: null,
        email: null,
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    renderModal({ mode: "create" });

    await userEvent.type(screen.getByRole("textbox", { name: /first name/i }), "Anders");
    await userEvent.type(screen.getByRole("textbox", { name: /last name/i }), "Refsdal");

    await userEvent.click(screen.getByText("+ Prefix"));
    await userEvent.type(screen.getByRole("textbox", { name: /prefix/i }), "Dr.");
    await userEvent.click(screen.getByText("+ Prefix"));

    expect(screen.queryByRole("textbox", { name: /prefix/i })).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /create contact/i }));

    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const body = JSON.parse((fetchMock.mock.calls[0][1] as RequestInit).body as string);
    expect(body).toEqual({ firstName: "Anders", lastName: "Refsdal" });
  });

  it("pre-opens chips for name parts that already have values when editing", async () => {
    vi.stubGlobal("fetch", vi.fn());

    renderModal({
      mode: "edit",
      contact: {
        id: 1001,
        firstName: "Anders",
        lastName: "Refsdal",
        middleName: "Bernhard",
        prefix: null,
        suffix: "PhD",
        phone: null,
        email: null,
      },
    });

    expect(await screen.findByRole("textbox", { name: /middle name/i })).toHaveValue("Bernhard");
    expect(screen.getByRole("textbox", { name: /suffix/i })).toHaveValue("PhD");
    expect(screen.queryByRole("textbox", { name: /prefix/i })).not.toBeInTheDocument();
  });
});
