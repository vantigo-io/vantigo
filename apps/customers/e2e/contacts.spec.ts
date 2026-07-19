import { expect, type Page, test } from "@playwright/test";
import { DEFAULT_PASSWORD, signUp, uniqueEmail } from "./helpers";

/**
 * Generates a valid Norwegian orgnr (mod11) unique per run, so specs sharing
 * the database never trigger duplicate-legal-id warnings for each other.
 */
function uniqueOrgnr(): string {
  const weights = [3, 2, 7, 6, 5, 4, 3, 2];
  for (let base = Date.now() % 90_000_000; ; base = (base + 1) % 90_000_000) {
    const digits = String(10_000_000 + base)
      .slice(0, 8)
      .split("")
      .map(Number);
    const remainder = digits.reduce((sum, digit, i) => sum + digit * weights[i], 0) % 11;
    const check = remainder === 0 ? 0 : 11 - remainder;
    if (check < 10) return `${digits.join("")}${check}`;
  }
}

async function createCustomer(page: Page, name: string, orgnr: string) {
  await page.goto("/customers");
  await page.getByRole("button", { name: "New customer" }).click();
  const modal = page.getByRole("dialog");
  await modal.getByLabel("Name").fill(name);
  await modal.getByLabel("Organisation number").fill(orgnr);
  await modal.getByRole("button", { name: "Create customer" }).click();
  await expect(page.getByText("Customer created")).toBeVisible();
}

async function openCustomerDetail(page: Page, name: string) {
  await page.goto("/customers");
  await page.getByPlaceholder(/Search name/).fill(name);
  await page.getByRole("row").filter({ hasText: name }).first().getByText(name).click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
}

test.describe("contacts", () => {
  test.beforeEach(async ({ page }) => {
    await signUp(page, {
      name: "Contact Admin",
      email: uniqueEmail("contacts"),
      password: DEFAULT_PASSWORD,
    });
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  });

  test("full contact lifecycle across two customers", async ({ page }) => {
    const stamp = Date.now();
    const customerA = `Alpha ${stamp} AS`;
    const customerB = `Beta ${stamp} AS`;
    const contactName = `Kari Kontakt ${stamp}`;

    await createCustomer(page, customerA, uniqueOrgnr());
    await createCustomer(page, customerB, uniqueOrgnr());

    // --- Create + link a new contact from customer A, marked primary
    await openCustomerDetail(page, customerA);
    await page.getByRole("button", { name: "Add contact" }).click();
    const addModal = page.getByRole("dialog");
    await addModal.getByLabel("Name").fill(contactName);
    await addModal.getByLabel("Email").fill(`kari-${stamp}@example.com`);
    await addModal.getByLabel("Role").fill("CEO");
    await addModal.getByLabel("Primary contact for this customer").check();
    await addModal.getByRole("button", { name: "Create and link" }).click();
    await expect(page.getByText("Contact added")).toBeVisible();

    const contactRow = page.getByText(contactName, { exact: true });
    await expect(contactRow).toBeVisible();
    await expect(page.getByText("Primary", { exact: true })).toBeVisible();
    await expect(page.getByText("CEO")).toBeVisible();

    // --- Link the same (existing) contact to customer B with another role
    await openCustomerDetail(page, customerB);
    await page.getByRole("button", { name: "Add contact" }).click();
    const linkModal = page.getByRole("dialog");
    await linkModal.getByText("Existing contact", { exact: true }).click();
    const contactSelect = linkModal.getByRole("combobox", { name: "Contact" });
    await contactSelect.click();
    await contactSelect.fill(contactName);
    await page.getByRole("option", { name: new RegExp(contactName) }).click();
    await linkModal.getByLabel("Role").fill("Board member");
    await linkModal.getByRole("button", { name: "Link contact" }).click();
    await expect(page.getByText("Contact added")).toBeVisible();
    await expect(page.getByText("Board member")).toBeVisible();

    // --- Contact detail shows both linked customers
    await page.getByRole("link", { name: contactName }).click();
    await expect(page).toHaveURL(/\/contacts\/\d+/);
    await expect(page.getByRole("heading", { name: contactName })).toBeVisible();
    await expect(page.getByRole("link", { name: customerA })).toBeVisible();
    await expect(page.getByRole("link", { name: customerB })).toBeVisible();

    // --- Unlink from customer B; the contact itself survives
    await openCustomerDetail(page, customerB);
    await page.getByRole("button", { name: "Contact actions" }).click();
    await page.getByRole("menuitem", { name: "Remove from customer" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Remove", exact: true }).click();
    await expect(page.getByText(`${contactName} was removed from the customer`)).toBeVisible();
    await expect(page.getByText("No contacts linked yet")).toBeVisible();

    // --- Still listed at /contacts with one linked customer; searchable
    await page.getByRole("link", { name: "Contacts" }).click();
    await expect(page.getByRole("heading", { name: "Contacts" })).toBeVisible();
    await page.getByPlaceholder(/Search name, email or phone/).fill(contactName);
    await expect(page.getByRole("row").filter({ hasText: contactName })).toBeVisible();

    // --- Delete with linked-customer warning
    await page.getByRole("row").filter({ hasText: contactName }).getByText(contactName).click();
    await page.getByRole("button", { name: "Delete", exact: true }).click();
    await expect(page.getByText(/linked to 1 customer/)).toBeVisible();
    await page.getByRole("dialog").getByRole("button", { name: "Delete", exact: true }).click();
    await expect(page.getByText(`${contactName} was deleted`)).toBeVisible();
    await expect(page).toHaveURL(/\/contacts$/);
  });
});
