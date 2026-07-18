import { expect, test } from "@playwright/test";
import { DEFAULT_PASSWORD, signIn, signOut, signUp, uniqueEmail } from "./helpers";

test.describe("authentication", () => {
  test("sign-up, sign-out, sign-in round trip", async ({ page }) => {
    const email = uniqueEmail();
    const name = "E2E Tester";

    await signUp(page, { name, email, password: DEFAULT_PASSWORD });
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
    await expect(page.getByRole("button", { name })).toBeVisible();

    await signOut(page, name);
    await expect(page).toHaveURL(/\/sign-in/);

    await signIn(page, { email, password: "definitely-wrong" });
    await expect(page.getByText("Sign-in failed")).toBeVisible();

    await signIn(page, { email, password: DEFAULT_PASSWORD });
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  });

  test("navigation between dashboard and customers works", async ({ page }) => {
    const email = uniqueEmail("nav");
    await signUp(page, { name: "Nav Tester", email, password: DEFAULT_PASSWORD });
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();

    await page.getByRole("link", { name: "Customers" }).click();
    await expect(page).toHaveURL(/\/customers/);
    await expect(page.getByRole("heading", { name: "Customers" })).toBeVisible();

    await page.getByRole("link", { name: "Dashboard" }).click();
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
    await expect(page.getByText("Total customers")).toBeVisible();
  });
});
