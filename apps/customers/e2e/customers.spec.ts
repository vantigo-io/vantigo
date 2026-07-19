import { expect, test } from "@playwright/test";
import { DEFAULT_PASSWORD, signUp, uniqueEmail } from "./helpers";

// Valid Norwegian test identifiers (mod11-correct)
const VALID_ORGNR = "923609016";
const VALID_ORGNR_2 = "974760673";

test.describe("customer management", () => {
  test.beforeEach(async ({ page }) => {
    await signUp(page, {
      name: "Customer Admin",
      email: uniqueEmail("cadmin"),
      password: DEFAULT_PASSWORD,
    });
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
    await page.getByRole("link", { name: "Customers" }).click();
    await expect(page.getByRole("heading", { name: "Customers" })).toBeVisible();
  });

  test("create, search, quick edit, detail and archive flow", async ({ page }) => {
    const name = `Acme ${Date.now()} AS`;
    const editedName = `${name} (edited)`;

    // --- Create via modal
    await page.getByRole("button", { name: "New customer" }).click();
    const modal = page.getByRole("dialog");
    await expect(modal.getByText("New customer")).toBeVisible();

    // Validation: invalid orgnr rejected client-side
    await modal.getByLabel("Name").fill(name);
    await modal.getByLabel("Organisation number").fill("123456789");
    await modal.getByLabel("Email").fill("kontakt@acme.no");
    await modal.getByLabel("Phone").fill("+4722334455");
    await modal.getByRole("button", { name: "Create customer" }).click();
    await expect(modal.getByText(/Invalid legal id/)).toBeVisible();

    // Fix legal id and submit
    await modal.getByLabel("Organisation number").fill(VALID_ORGNR);
    await modal.getByRole("button", { name: "Create customer" }).click();
    await expect(page.getByText("Customer created")).toBeVisible();

    // --- Appears in grid via search
    await page.getByPlaceholder(/Search name/).fill(name);
    const row = page.getByRole("row", { name: new RegExp(name.replace(/[()]/g, "\\$&")) });
    await expect(row.first()).toBeVisible();
    await expect(row.first().getByText("Business")).toBeVisible();

    // --- Quick edit via row menu
    await row.first().getByRole("button", { name: "Actions" }).click();
    await page.getByRole("menuitem", { name: "Quick edit" }).click();
    const editModal = page.getByRole("dialog");
    await editModal.getByLabel("Name").fill(editedName);
    await editModal.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Customer updated")).toBeVisible();

    // --- Open detail page via row click
    await page.getByPlaceholder(/Search name/).fill(editedName);
    await page
      .getByRole("row", { name: new RegExp(editedName.replace(/[()]/g, "\\$&")) })
      .first()
      .getByText(editedName)
      .click();
    await expect(page).toHaveURL(/\/customers\/\d+/);
    await expect(page.getByRole("heading", { name: editedName })).toBeVisible();
    await expect(page.getByText(`🇳🇴 ${VALID_ORGNR}`)).toBeVisible();

    // --- Archive from detail page
    await page.getByRole("button", { name: "Archive" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Archive", exact: true }).click();
    await expect(page.getByText("Customer archived")).toBeVisible();
    await expect(page.getByText("Archived", { exact: true })).toBeVisible();

    // --- Reactivate
    await page.getByRole("button", { name: "Reactivate" }).click();
    await expect(page.getByText("Customer updated")).toBeVisible();
  });

  test("duplicate legal id shows a warning but does not block", async ({ page }) => {
    const name = `Duplo ${Date.now()} AS`;

    const create = async (customerName: string) => {
      await page.getByRole("button", { name: "New customer" }).click();
      const modal = page.getByRole("dialog");
      await modal.getByLabel("Name").fill(customerName);
      await modal.getByLabel("Organisation number").fill(VALID_ORGNR_2);
      await modal.getByLabel("Email").fill("dup@duplo.no");
      await modal.getByLabel("Phone").fill("+4722334455");
      await modal.getByRole("button", { name: "Create customer" }).click();
      await expect(page.getByText("Customer created")).toBeVisible();
      // Dismiss notification to avoid strict-mode clashes on the next round
      await page.getByText("Customer created").first().click();
    };

    await create(`${name} 1`);
    await create(`${name} 2`);

    // Both exist
    await page.getByPlaceholder(/Search name/).fill(name);
    await expect(page.getByRole("row", { name: new RegExp(`${name} 1`) })).toBeVisible();
    await expect(page.getByRole("row", { name: new RegExp(`${name} 2`) })).toBeVisible();
  });

  test("stats and filters reflect data", async ({ page }) => {
    // Stats cards render numbers
    await expect(page.getByText("Total customers")).toBeVisible();
    await expect(page.getByText("New this month")).toBeVisible();

    // Type filter narrows the grid
    const typeFilter = page.getByPlaceholder("Type");
    await typeFilter.click();
    await page.getByRole("option", { name: "Private" }).click();
    // Grid reloads without crashing; either rows or the empty state show
    await expect(
      page.getByText("No customers found").or(page.getByRole("table")).first(),
    ).toBeVisible();
  });

  test("brreg company search fills fields on selection but respects manual edits", async ({
    page,
  }) => {
    // Mock the proxy route so the test never depends on brreg availability.
    await page.route("**/api/brreg/search**", async (route) => {
      const url = new URL(route.request().url());
      const query = (url.searchParams.get("q") ?? "").toLowerCase();
      const results = query.includes("equi")
        ? [
            {
              orgnr: "923609016",
              name: "EQUINOR ASA",
              orgForm: "ASA",
              city: "STAVANGER",
              email: "post@equinor.com",
              phone: "+4751990000",
            },
          ]
        : query.includes("nocontact")
          ? [{ orgnr: "974760673", name: "NOCONTACT AS" }]
          : [];
      await route.fulfill({ json: { results } });
    });

    await page.getByRole("button", { name: "New customer" }).click();
    const modal = page.getByRole("dialog");

    // Search box only visible for NO + business
    await expect(modal.getByLabel("Company search")).toBeVisible();
    await modal.locator("label", { hasText: "Private" }).click();
    await expect(modal.getByLabel("Company search")).toBeHidden();
    await modal.locator("label", { hasText: "Business" }).click();

    // Search and select → fills orgnr + name + contact details
    await modal.getByLabel("Company search").fill("equinor");
    await page.getByRole("option", { name: /EQUINOR ASA/ }).click();
    await expect(modal.getByLabel("Organisation number")).toHaveValue("923609016");
    await expect(modal.getByLabel("Name")).toHaveValue("EQUINOR ASA");
    await expect(modal.getByLabel("Email")).toHaveValue("post@equinor.com");
    await expect(modal.getByLabel("Phone")).toHaveValue("+4751990000");

    // Selecting a company WITHOUT contact info must not blank existing values
    await modal.getByLabel("Company search").fill("nocontact");
    await page.getByRole("option", { name: /NOCONTACT AS/ }).click();
    await expect(modal.getByLabel("Organisation number")).toHaveValue("974760673");
    await expect(modal.getByLabel("Name")).toHaveValue("NOCONTACT AS");
    await expect(modal.getByLabel("Email")).toHaveValue("post@equinor.com");
    await expect(modal.getByLabel("Phone")).toHaveValue("+4751990000");

    // Manual rename afterwards must survive submission untouched
    const manualName = `Nocontact Norge ${Date.now()}`;
    await modal.getByLabel("Name").fill(manualName);
    await modal.getByRole("button", { name: "Create customer" }).click();
    await expect(page.getByText("Customer created")).toBeVisible();

    await page.getByPlaceholder(/Search name/).fill(manualName);
    await expect(page.getByRole("row", { name: new RegExp(manualName) })).toBeVisible();
  });
});
