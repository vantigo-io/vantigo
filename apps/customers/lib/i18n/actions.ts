"use server";

import { db } from "@vantigo/customers/database/db";
import { users } from "@vantigo/customers/database/schema";
import { auth } from "@vantigo/customers/lib/auth";
import { isLocale, LOCALE_COOKIE } from "@vantigo/customers/lib/i18n/locales";
import { eq } from "drizzle-orm";
import { revalidatePath } from "next/cache";
import { cookies, headers } from "next/headers";

const ONE_YEAR_SECONDS = 60 * 60 * 24 * 365;

export async function setLanguage(locale: string): Promise<void> {
  if (!isLocale(locale)) return;

  (await cookies()).set(LOCALE_COOKIE, locale, {
    maxAge: ONE_YEAR_SECONDS,
    path: "/",
    sameSite: "lax",
  });

  const session = await auth.api.getSession({ headers: await headers() });
  if (session?.user) {
    await db.update(users).set({ language: locale }).where(eq(users.id, session.user.id));
  }

  revalidatePath("/", "layout");
}
