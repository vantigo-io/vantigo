"use server";

import { db } from "@vantigo/customers/database/db";
import { users } from "@vantigo/customers/database/schema";
import { auth } from "@vantigo/customers/lib/auth";
import { isPreferredCustomerType } from "@vantigo/customers/lib/settings/preferences";
import { eq } from "drizzle-orm";
import { revalidatePath } from "next/cache";
import { headers } from "next/headers";

export async function setPreferredCustomerType(value: string): Promise<void> {
  if (!isPreferredCustomerType(value)) return;

  const session = await auth.api.getSession({ headers: await headers() });
  if (!session?.user) return;

  await db.update(users).set({ preferredCustomerType: value }).where(eq(users.id, session.user.id));

  revalidatePath("/", "layout");
}
