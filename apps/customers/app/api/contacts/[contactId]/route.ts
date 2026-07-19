import { apiError, requireSession, validationError } from "@vantigo/customers/lib/api";
import {
  deleteContact,
  getContactWithCustomers,
  updateContact,
} from "@vantigo/customers/lib/contacts/queries";
import { contactUpdateSchema } from "@vantigo/customers/lib/contacts/schemas";
import { type NextRequest, NextResponse } from "next/server";
import { z } from "zod";

const idSchema = z.coerce.number().int().positive();

type RouteContext = { params: Promise<{ contactId: string }> };

async function parseId(context: RouteContext) {
  const { contactId } = await context.params;
  const parsed = idSchema.safeParse(contactId);
  return parsed.success ? parsed.data : null;
}

export async function GET(_request: NextRequest, context: RouteContext) {
  const { response } = await requireSession();
  if (response) return response;

  const id = await parseId(context);
  if (id === null) return apiError(400, "INVALID_ID", "Contact id must be a positive integer");

  const contact = await getContactWithCustomers(id);
  if (!contact) return apiError(404, "NOT_FOUND", `Contact ${id} does not exist`);

  return NextResponse.json({ contact });
}

export async function PATCH(request: NextRequest, context: RouteContext) {
  const { response } = await requireSession();
  if (response) return response;

  const id = await parseId(context);
  if (id === null) return apiError(400, "INVALID_ID", "Contact id must be a positive integer");

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return apiError(400, "INVALID_JSON", "Request body must be valid JSON");
  }

  const parsed = contactUpdateSchema.safeParse(body);
  if (!parsed.success) return validationError(parsed.error);

  const contact = await updateContact(id, parsed.data);
  if (!contact) return apiError(404, "NOT_FOUND", `Contact ${id} does not exist`);

  return NextResponse.json({ contact });
}

export async function DELETE(_request: NextRequest, context: RouteContext) {
  const { response } = await requireSession();
  if (response) return response;

  const id = await parseId(context);
  if (id === null) return apiError(400, "INVALID_ID", "Contact id must be a positive integer");

  // Links are removed by ON DELETE CASCADE; the UI warns about them first.
  const deleted = await deleteContact(id);
  if (!deleted) return apiError(404, "NOT_FOUND", `Contact ${id} does not exist`);

  return NextResponse.json({ ok: true });
}
