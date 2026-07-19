import { apiError, requireSession, validationError } from "@vantigo/customers/lib/api";
import {
  createContact,
  linkContact,
  listCustomerContacts,
} from "@vantigo/customers/lib/contacts/queries";
import { customerContactCreateSchema } from "@vantigo/customers/lib/contacts/schemas";
import { getCustomerById } from "@vantigo/customers/lib/customers/queries";
import { type NextRequest, NextResponse } from "next/server";
import { z } from "zod";

const idSchema = z.coerce.number().int().positive();

type RouteContext = { params: Promise<{ customerId: string }> };

async function parseCustomerId(context: RouteContext) {
  const { customerId } = await context.params;
  const parsed = idSchema.safeParse(customerId);
  return parsed.success ? parsed.data : null;
}

export async function GET(_request: NextRequest, context: RouteContext) {
  const { response } = await requireSession();
  if (response) return response;

  const customerId = await parseCustomerId(context);
  if (customerId === null) {
    return apiError(400, "INVALID_ID", "Customer id must be a positive integer");
  }

  const contacts = await listCustomerContacts(customerId);
  return NextResponse.json({ contacts });
}

export async function POST(request: NextRequest, context: RouteContext) {
  const { response } = await requireSession();
  if (response) return response;

  const customerId = await parseCustomerId(context);
  if (customerId === null) {
    return apiError(400, "INVALID_ID", "Customer id must be a positive integer");
  }

  const customer = await getCustomerById(customerId);
  if (!customer) return apiError(404, "NOT_FOUND", `Customer ${customerId} does not exist`);

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return apiError(400, "INVALID_JSON", "Request body must be valid JSON");
  }

  const parsed = customerContactCreateSchema.safeParse(body);
  if (!parsed.success) return validationError(parsed.error);

  const input = parsed.data;
  const contactId =
    "contactId" in input ? input.contactId : (await createContact(input.contact)).id;

  try {
    await linkContact(customerId, contactId, { role: input.role, isPrimary: input.isPrimary });
  } catch {
    // Composite PK violation (already linked) or missing contact.
    return apiError(409, "ALREADY_LINKED", "Contact is already linked to this customer");
  }

  return NextResponse.json({ contactId }, { status: 201 });
}
