import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sent } from "../test/helpers";
import { stubAccountingApi } from "../test/server";
import { SupplierFormModal, type SupplierFormSupplier } from "./suppliers";

const renderForm = (props: { supplier?: SupplierFormSupplier; books?: boolean }) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <SupplierFormModal onClose={() => {}} {...props} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
};

const booksFields = ["bankAccount", "iban", "bic", "defaultAccountId", "defaultTaxCodeId"];

// The buyer's form (purchasing design A12, A26): opened from Purchasing for a
// holder of accounting:suppliers-manage, it neither shows nor sends the books'
// own fields, and a change carries the revision it was given.
describe("the supplier form without the books", () => {
  it("creates a supplier with no books field", async () => {
    const fetchMock = stubAccountingApi();
    renderForm({ books: false });
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("textbox", { name: /Bank account/ })).not.toBeInTheDocument();
    await userEvent.type(within(dialog).getByRole("textbox", { name: /^Name/ }), "Stål og Rør AS");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/suppliers$/)).toHaveLength(1));
    const body = sent(fetchMock, "POST", /\/suppliers$/)[0].body;
    expect(body).toMatchObject({ name: "Stål og Rør AS" });
    for (const field of [...booksFields, "active", "revision"]) expect(body).not.toHaveProperty(field);
  });

  it("changes a supplier from Purchasing's read, sending its revision and no books field", async () => {
    const fetchMock = stubAccountingApi();
    renderForm({
      books: false,
      supplier: {
        id: 3001,
        revision: 7,
        name: "Rørlegger Hansen AS",
        organisationNumber: "923609016",
        country: "NO",
        addressLine: "Storgata 1",
        postalCode: "0155",
        city: "Oslo",
        email: "post@hansen.no",
        currency: "NOK",
        paymentTermsDays: 30,
        isPerson: false,
        active: true,
      },
    });
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("textbox", { name: "City" })).toHaveValue("Oslo");
    await userEvent.clear(within(dialog).getByRole("textbox", { name: "City" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "City" }), "Bergen");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT", /\/suppliers\/3001$/)).toHaveLength(1));
    const body = sent(fetchMock, "PUT", /\/suppliers\/3001$/)[0].body;
    expect(body).toMatchObject({ name: "Rørlegger Hansen AS", city: "Bergen", revision: 7 });
    for (const field of [...booksFields, "active", "isPerson"]) expect(body).not.toHaveProperty(field);
  });
});
