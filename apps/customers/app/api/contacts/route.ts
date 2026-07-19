import { apiError, requireSession, validationError } from "@vantigo/customers/lib/api";
import { createContact, listContacts } from "@vantigo/customers/lib/contacts/queries";
import {
  contactCreateSchema,
  contactListQuerySchema,
} from "@vantigo/customers/lib/contacts/schemas";
import { type NextRequest, NextResponse } from "next/server";

export async function GET(request: NextRequest) {
  const { response } = await requireSession();
  if (response) return response;

  const params = Object.fromEntries(request.nextUrl.searchParams);
  const parsed = contactListQuerySchema.safeParse(params);
  if (!parsed.success) return validationError(parsed.error);

  const result = await listContacts(parsed.data);
  return NextResponse.json(result);
}

export async function POST(request: NextRequest) {
  const { response } = await requireSession();
  if (response) return response;

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return apiError(400, "INVALID_JSON", "Request body must be valid JSON");
  }

  const parsed = contactCreateSchema.safeParse(body);
  if (!parsed.success) return validationError(parsed.error);

  const contact = await createContact(parsed.data);
  return NextResponse.json({ contact }, { status: 201 });
}
