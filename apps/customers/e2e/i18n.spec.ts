import { expect, test } from "@playwright/test";
import { DEFAULT_PASSWORD, signIn, signOut, signUp, uniqueEmail } from "./helpers";

test.describe("i18n", () => {
  test("Norwegian browser gets Norwegian sign-in page", async ({ browser }) => {
    const context = await browser.newContext({ locale: "nb-NO" });
    const page = await context.newPage();
    await page.goto("/sign-in");
    await expect(page.getByRole("heading", { name: "Velkommen tilbake!" })).toBeVisible();
    await context.close();
  });

  test("language toggle persists via cookie and user record", async ({ page }) => {
    const email = uniqueEmail("i18n");
    const name = "I18n Tester";

    await signUp(page, { name, email, password: DEFAULT_PASSWORD });
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();

    // Switch to Norwegian via the user menu
    await page.getByRole("button", { name }).click();
    await page.getByText("Norsk", { exact: true }).click();
    await expect(page.getByRole("heading", { name: "Dashbord" })).toBeVisible();
    await expect(page.getByText("Totalt antall kunder")).toBeVisible();

    // Survives reload (cookie)
    await page.reload();
    await expect(page.getByRole("heading", { name: "Dashbord" })).toBeVisible();

    // Survives sign-out + sign-in (persisted on user record)
    await page.getByRole("button", { name }).click();
    await page.getByRole("menuitem", { name: "Logg ut" }).click();
    await expect(page).toHaveURL(/\/sign-in/);

    // Clear the locale cookie to prove the user record is the source
    await page.context().clearCookies({ name: "locale" });
    await signIn(page, { email, password: DEFAULT_PASSWORD });
    await expect(page.getByRole("heading", { name: "Dashbord" })).toBeVisible();
  });
});
