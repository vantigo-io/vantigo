import { apiError, requireSession, validationError } from "@vantigo/customers/lib/api";
import { unlinkContact, updateLink } from "@vantigo/customers/lib/contacts/queries";
import { customerContactUpdateSchema } from "@vantigo/customers/lib/contacts/schemas";
import { type NextRequest, NextResponse } from "next/server";
import { z } from "zod";

const idSchema = z.coerce.number().int().positive();

type RouteContext = { params: Promise<{ customerId: string; contactId: string }> };

async function parseIds(context: RouteContext) {
  const { customerId, contactId } = await context.params;
  const parsedCustomer = idSchema.safeParse(customerId);
  const parsedContact = idSchema.safeParse(contactId);
  if (!parsedCustomer.success || !parsedContact.success) return null;
  return { customerId: parsedCustomer.data, contactId: parsedContact.data };
}

export async function PATCH(request: NextRequest, context: RouteContext) {
  const { response } = await requireSession();
  if (response) return response;

  const ids = await parseIds(context);
  if (!ids) return apiError(400, "INVALID_ID", "Ids must be positive integers");

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return apiError(400, "INVALID_JSON", "Request body must be valid JSON");
  }

  const parsed = customerContactUpdateSchema.safeParse(body);
  if (!parsed.success) return validationError(parsed.error);

  const updated = await updateLink(ids.customerId, ids.contactId, parsed.data);
  if (!updated) return apiError(404, "NOT_FOUND", "Contact is not linked to this customer");

  return NextResponse.json({ ok: true });
}

export async function DELETE(_request: NextRequest, context: RouteContext) {
  const { response } = await requireSession();
  if (response) return response;

  const ids = await parseIds(context);
  if (!ids) return apiError(400, "INVALID_ID", "Ids must be positive integers");

  const removed = await unlinkContact(ids.customerId, ids.contactId);
  if (!removed) return apiError(404, "NOT_FOUND", "Contact is not linked to this customer");

  return NextResponse.json({ ok: true });
}
