import { db } from "@vantigo/customers/database/db";
import { users } from "@vantigo/customers/database/schema";
import { apiError, requireSession, validationError } from "@vantigo/customers/lib/api";
import {
  createCustomer,
  findDuplicateWarnings,
  listCustomers,
} from "@vantigo/customers/lib/customers/queries";
import {
  customerCreateSchema,
  customerListQuerySchema,
} from "@vantigo/customers/lib/customers/schemas";
import { eq } from "drizzle-orm";
import { type NextRequest, NextResponse } from "next/server";

export async function GET(request: NextRequest) {
  const { response } = await requireSession();
  if (response) return response;

  const params = Object.fromEntries(request.nextUrl.searchParams);
  const parsed = customerListQuerySchema.safeParse(params);
  if (!parsed.success) return validationError(parsed.error);

  const result = await listCustomers(parsed.data);
  return NextResponse.json(result);
}

export async function POST(request: NextRequest) {
  const { session, response } = await requireSession();
  if (response) return response;

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return apiError(400, "INVALID_JSON", "Request body must be valid JSON");
  }

  const parsed = customerCreateSchema.safeParse(body);
  if (!parsed.success) return validationError(parsed.error);

  const warnings = await findDuplicateWarnings(parsed.data);
  const customer = await createCustomer(parsed.data);

  // Remember the legal type so the "last used" preference can default to it.
  await db
    .update(users)
    .set({ lastCustomerType: customer.legalType })
    .where(eq(users.id, session.user.id));

  return NextResponse.json({ customer, warnings }, { status: 201 });
}
