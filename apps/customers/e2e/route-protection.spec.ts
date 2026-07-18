import { expect, test } from "@playwright/test";

test.describe("route protection", () => {
  test("unauthenticated / redirects to sign-in", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveURL(/\/sign-in/);
    await expect(page.getByRole("heading", { name: "Welcome back!" })).toBeVisible();
  });

  test("unauthenticated /customers redirects to sign-in", async ({ page }) => {
    await page.goto("/customers");
    await expect(page).toHaveURL(/\/sign-in/);
  });
});
