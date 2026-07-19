import { expect, type Page, test } from "@playwright/test";
import * as OTPAuth from "otpauth";
import { DEFAULT_PASSWORD, signIn, signOut, signUp, uniqueEmail } from "./helpers";

function totpCode(secret: string): string {
  return new OTPAuth.TOTP({
    secret: OTPAuth.Secret.fromBase32(secret),
    digits: 6,
    period: 30,
  }).generate();
}

async function fillPin(page: Page, testId: string, code: string) {
  await page.getByTestId(testId).locator("input").first().pressSequentially(code);
}

async function fillDialogPassword(page: Page, password: string) {
  await page.getByRole("dialog").getByRole("textbox", { name: "Password" }).fill(password);
}

test.describe("two-factor authentication", () => {
  test("enroll in settings, verify on sign-in, then use a backup code", async ({ page }) => {
    const email = uniqueEmail("2fa");
    const name = "TwoFactor Tester";

    await signUp(page, { name, email, password: DEFAULT_PASSWORD });
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();

    // --- Enroll from the settings security tab ---
    await page.goto("/settings?tab=security");
    await expect(page.getByRole("heading", { name: "Two-factor authentication" })).toBeVisible();
    await page.getByRole("button", { name: "Enable 2FA" }).click();

    await fillDialogPassword(page, DEFAULT_PASSWORD);
    await page.getByRole("button", { name: "Continue" }).click();

    const secret = (await page.getByTestId("totp-secret").textContent()) ?? "";
    expect(secret.length).toBeGreaterThan(10);

    const backupCodes = (await page.getByTestId("backup-codes").locator("code").allTextContents())
      .map((code) => code.trim())
      .filter(Boolean);
    expect(backupCodes.length).toBeGreaterThan(0);

    await page.getByLabel("I have saved my backup codes").check();
    await page.getByRole("button", { name: "Continue" }).click();

    await fillPin(page, "enroll-pin", totpCode(secret));
    await expect(page.getByText("Two-factor authentication enabled")).toBeVisible();
    await expect(page.getByText("Enabled", { exact: true })).toBeVisible();

    // --- Sign out, sign in again: TOTP challenge ---
    await signOut(page, name);
    await signIn(page, { email, password: DEFAULT_PASSWORD });

    await expect(page).toHaveURL(/\/two-factor/);
    await fillPin(page, "verify-pin", totpCode(secret));
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();

    // --- Sign out, sign in with a backup code instead ---
    await signOut(page, name);
    await signIn(page, { email, password: DEFAULT_PASSWORD });

    await expect(page).toHaveURL(/\/two-factor/);
    await page.getByRole("button", { name: "Use a backup code instead" }).click();
    await page.getByLabel("Backup code").fill(backupCodes[0]);
    await page.getByRole("button", { name: "Verify" }).click();
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  });

  test("disable 2FA restores plain password sign-in", async ({ page }) => {
    const email = uniqueEmail("2fa-disable");
    const name = "TwoFactor Disabler";

    await signUp(page, { name, email, password: DEFAULT_PASSWORD });
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();

    await page.goto("/settings?tab=security");
    await page.getByRole("button", { name: "Enable 2FA" }).click();
    await fillDialogPassword(page, DEFAULT_PASSWORD);
    await page.getByRole("button", { name: "Continue" }).click();

    const secret = (await page.getByTestId("totp-secret").textContent()) ?? "";
    await page.getByLabel("I have saved my backup codes").check();
    await page.getByRole("button", { name: "Continue" }).click();
    await fillPin(page, "enroll-pin", totpCode(secret));
    await expect(page.getByText("Enabled", { exact: true })).toBeVisible();

    // Disable again.
    await page.getByRole("button", { name: "Disable 2FA" }).click();
    await fillDialogPassword(page, DEFAULT_PASSWORD);
    await page.getByRole("dialog").getByRole("button", { name: "Disable 2FA" }).click();
    await expect(page.getByText("Two-factor authentication disabled")).toBeVisible();

    // Sign-in goes straight to the dashboard again.
    await signOut(page, name);
    await signIn(page, { email, password: DEFAULT_PASSWORD });
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  });
});
