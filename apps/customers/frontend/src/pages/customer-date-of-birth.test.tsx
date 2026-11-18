import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerDateOfBirth } from "./-customer-date-of-birth";

const jsonResponse = (body: unknown) =>
  new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });

describe("a person's date of birth (invoices phase 6 design I30)", () => {
  it("saves the date and has the customer read again, so its new revision is the next edit's", async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    queryClient.setQueryData(["customers", 2002], { id: 2002, revision: 3 });
    let stored: Record<string, unknown> = { revision: 3 };
    const fetchMock = stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/api/v1/customers/2002/date-of-birth" && init?.method === "PUT") {
        stored = { dateOfBirth: "1980-05-17", revision: 4 };
        return jsonResponse(stored);
      }
      if (String(input) === "/api/v1/customers/2002/date-of-birth") return jsonResponse(stored);
      return new Response(null, { status: 404 });
    });
    render(
      <MantineProvider env="test">
        <QueryClientProvider client={queryClient}>
          <CustomerDateOfBirth customerId={2002} canEdit />
        </QueryClientProvider>
      </MantineProvider>,
    );
    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    await userEvent.type(screen.getByLabelText(/Date of birth/), "1980-05-17");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("1980-05-17")).toBeInTheDocument();
    await waitFor(() => expect(queryClient.getQueryState(["customers", 2002])?.isInvalidated).toBe(true));
    expect(fetchMock.calls.some(([, init]) => String(init?.body).includes('"dateOfBirth":"1980-05-17"'))).toBe(true);
  });
});
