import { readFileSync } from "node:fs";
import path from "node:path";
import type { Page } from "@playwright/test";
import { Client } from "pg";

let counter = 0;

/** Unique email per test run to avoid collisions in the shared test DB. */
export function uniqueEmail(prefix = "user"): string {
  counter += 1;
  return `${prefix}-${Date.now()}-${counter}@e2e.test`;
}

export const DEFAULT_PASSWORD = "E2eP4ssword!";

export async function signUp(
  page: Page,
  { name, email, password }: { name: string; email: string; password: string },
) {
  await page.goto("/sign-up");
  await page.getByLabel("Name", { exact: true }).fill(name);
  await page.getByLabel("Email address").fill(email);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByLabel("Confirm password").fill(password);
  await page.getByRole("button", { name: "Create account" }).click();
}

export async function signIn(page: Page, { email, password }: { email: string; password: string }) {
  await page.goto("/sign-in");
  await page.getByLabel("Email address").fill(email);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Login" }).click();
}

export async function signOut(page: Page, userName: string) {
  await page.getByRole("button", { name: userName }).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
}

/** Runs a query against the testcontainer database started by global-setup. */
export async function queryTestDb<T extends Record<string, unknown>>(
  sql: string,
  params: unknown[] = [],
): Promise<T[]> {
  const state = JSON.parse(readFileSync(path.join(__dirname, ".state.json"), "utf8")) as {
    databaseUrl: string;
  };
  const client = new Client({ connectionString: state.databaseUrl });
  await client.connect();
  try {
    const result = await client.query(sql, params);
    return result.rows as T[];
  } finally {
    await client.end();
  }
}
