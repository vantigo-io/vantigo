import { expect, test } from "@playwright/test";
import { DEFAULT_PASSWORD, queryTestDb, signIn, signOut, signUp, uniqueEmail } from "./helpers";

test.describe("password reset", () => {
  test("full reset flow via verification token", async ({ page }) => {
    const email = uniqueEmail("reset");
    const name = "Reset Tester";
    const newPassword = "N3wE2ePassword!";

    await signUp(page, { name, email, password: DEFAULT_PASSWORD });
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
    await signOut(page, name);

    await page.goto("/forgot-password");
    await page.getByLabel("Email address").fill(email);
    await page.getByRole("button", { name: "Send reset link" }).click();
    await expect(page.getByText("Check your inbox")).toBeVisible();

    // The reset email is stubbed; fetch the token from better-auth's verifications table.
    const rows = await queryTestDb<{ identifier: string }>(
      "SELECT identifier FROM customers.verifications WHERE identifier LIKE 'reset-password:%' ORDER BY created_at DESC LIMIT 1",
    );
    expect(rows).toHaveLength(1);
    const token = rows[0].identifier.replace("reset-password:", "");

    await page.goto(`/reset-password?token=${token}`);
    await page.getByLabel("New password", { exact: true }).fill(newPassword);
    await page.getByLabel("Confirm new password").fill(newPassword);
    await page.getByRole("button", { name: "Reset password" }).click();

    await expect(page).toHaveURL(/\/sign-in/);
    await expect(page.getByText("Password updated")).toBeVisible();

    await signIn(page, { email, password: newPassword });
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  });

  test("reset page without token shows invalid link state", async ({ page }) => {
    await page.goto("/reset-password");
    await expect(page.getByText(/invalid or has expired/)).toBeVisible();
    await expect(page.getByRole("link", { name: "Request a new link" })).toBeVisible();
  });
});
