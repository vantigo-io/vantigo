import { MantineProvider } from "@mantine/core";
import { Notifications, notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { routeTree } from "../test/route-tree";

const ACCOUNT_EXISTS_MESSAGE = "An account already exists for this email address.";
const VALIDATION_MESSAGE = "Email cannot be used for an invitation";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const renderRoute = async (path: string) => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [path] }),
  });

  render(
    <MantineProvider>
      <Notifications />
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </MantineProvider>,
  );

  return router;
};

const fillSetupForm = async () => {
  await userEvent.type(screen.getByLabelText(/setup secret/i), "setup-secret");
  await userEvent.type(screen.getByRole("textbox", { name: /display name/i }), "Test Owner");
  await userEvent.type(screen.getByRole("textbox", { name: /^email$/i }), "existing@example.com");
  await userEvent.type(screen.getByLabelText(/^password$/i), "password");
  await userEvent.click(screen.getByRole("button", { name: /create owner account/i }));
};

const ownerSession = {
  user: {
    id: "owner-user",
    displayName: "Test Owner",
    email: "owner@example.com",
    roles: ["Owner"],
  },
};

const stubSettingsFetch = (invitationResponse: Response) =>
  stubFetch(
    (url: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      const requestUrl = String(url);
      if (method === "GET" && requestUrl === "/api/v1/identity/owner/invitations") {
        return Promise.resolve(jsonResponse(200, []));
      }
      if (method === "GET" && requestUrl === "/api/v1/identity/owner/mfa") {
        return Promise.resolve(jsonResponse(200, { twoFactorEnabled: false, mfaEnrollmentRequired: false }));
      }
      if (method === "POST" && requestUrl === "/api/v1/identity/owner/invitations")
        return Promise.resolve(invitationResponse);
      return Promise.resolve(new Response(null, { status: 404 }));
    },
    { session: ownerSession },
  );

describe("lifecycle form errors", () => {
  afterEach(() => {
    notifications.clean();
  });

  it("shows the setup account-exists message inline, on email, and in a notification", async () => {
    const notificationSpy = vi.spyOn(notifications, "show");
    stubFetch((url: RequestInfo | URL) => {
      if (String(url) === "/api/v1/identity/bootstrap-status")
        return Promise.resolve(jsonResponse(200, { available: true }));
      if (String(url) === "/api/v1/identity/bootstrap") {
        return Promise.resolve(
          jsonResponse(409, { error: { code: "account_exists", message: ACCOUNT_EXISTS_MESSAGE, fields: null } }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });

    await renderRoute("/setup");
    await screen.findByRole("heading", { name: /set up vantigo/i });
    await fillSetupForm();

    const emailInput = screen.getByRole("textbox", { name: /^email$/i });
    await waitFor(() => expect(emailInput).toHaveAttribute("aria-invalid", "true"));
    expect(screen.getAllByText(ACCOUNT_EXISTS_MESSAGE).length).toBeGreaterThan(0);
    await waitFor(() =>
      expect(notificationSpy).toHaveBeenCalledWith(
        expect.objectContaining({ color: "red", message: ACCOUNT_EXISTS_MESSAGE }),
      ),
    );
  });

  it("shows the owner invitation account-exists message on email and in a notification", async () => {
    const notificationSpy = vi.spyOn(notifications, "show");
    stubSettingsFetch(
      jsonResponse(409, { error: { code: "account_exists", message: ACCOUNT_EXISTS_MESSAGE, fields: null } }),
    );

    await renderRoute("/settings");
    await screen.findByRole("heading", { name: /account settings/i });

    const emailInput = screen.getByRole("textbox", { name: /^email$/i });
    await userEvent.type(emailInput, "existing@example.com");
    await userEvent.click(screen.getByRole("button", { name: /send invite/i }));

    await waitFor(() => expect(emailInput).toHaveAttribute("aria-invalid", "true"));
    expect(screen.getAllByText(ACCOUNT_EXISTS_MESSAGE).length).toBeGreaterThan(0);
    await waitFor(() =>
      expect(notificationSpy).toHaveBeenCalledWith(
        expect.objectContaining({ color: "red", message: ACCOUNT_EXISTS_MESSAGE }),
      ),
    );
  });

  it("maps structured invitation validation to email and still shows a concise notification", async () => {
    const notificationSpy = vi.spyOn(notifications, "show");
    stubSettingsFetch(jsonResponse(400, { error: { fields: { email: [VALIDATION_MESSAGE] } } }));

    await renderRoute("/settings");
    await screen.findByRole("heading", { name: /account settings/i });

    const emailInput = screen.getByRole("textbox", { name: /^email$/i });
    await userEvent.type(emailInput, "invalid@example.com");
    await userEvent.click(screen.getByRole("button", { name: /send invite/i }));

    await waitFor(() => expect(emailInput).toHaveAttribute("aria-invalid", "true"));
    expect(screen.getByText(VALIDATION_MESSAGE)).toBeInTheDocument();
    await waitFor(() =>
      expect(notificationSpy).toHaveBeenCalledWith(
        expect.objectContaining({ color: "red", message: "Request failed (HTTP 400)" }),
      ),
    );
  });
});
