import { apiError, requireSession, validationError } from "@vantigo/customers/lib/api";
import {
  archiveCustomer,
  findDuplicateWarnings,
  getCustomerById,
  updateCustomer,
} from "@vantigo/customers/lib/customers/queries";
import { customerUpdateSchema } from "@vantigo/customers/lib/customers/schemas";
import { type NextRequest, NextResponse } from "next/server";
import { z } from "zod";

const idSchema = z.coerce.number().int().positive();

type RouteContext = { params: Promise<{ customerId: string }> };

async function parseId(context: RouteContext) {
  const { customerId } = await context.params;
  const parsed = idSchema.safeParse(customerId);
  return parsed.success ? parsed.data : null;
}

export async function GET(_request: NextRequest, context: RouteContext) {
  const { response } = await requireSession();
  if (response) return response;

  const id = await parseId(context);
  if (id === null) return apiError(400, "INVALID_ID", "Customer id must be a positive integer");

  const customer = await getCustomerById(id);
  if (!customer) return apiError(404, "NOT_FOUND", `Customer ${id} does not exist`);

  return NextResponse.json({ customer });
}

export async function PATCH(request: NextRequest, context: RouteContext) {
  const { response } = await requireSession();
  if (response) return response;

  const id = await parseId(context);
  if (id === null) return apiError(400, "INVALID_ID", "Customer id must be a positive integer");

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return apiError(400, "INVALID_JSON", "Request body must be valid JSON");
  }

  const parsed = customerUpdateSchema.safeParse(body);
  if (!parsed.success) return validationError(parsed.error);

  const warnings = await findDuplicateWarnings(
    { legalId: parsed.data.legalId, email: parsed.data.email },
    id,
  );
  const customer = await updateCustomer(id, parsed.data);
  if (!customer) return apiError(404, "NOT_FOUND", `Customer ${id} does not exist`);

  return NextResponse.json({ customer, warnings });
}

export async function DELETE(_request: NextRequest, context: RouteContext) {
  const { response } = await requireSession();
  if (response) return response;

  const id = await parseId(context);
  if (id === null) return apiError(400, "INVALID_ID", "Customer id must be a positive integer");

  const customer = await archiveCustomer(id);
  if (!customer) return apiError(404, "NOT_FOUND", `Customer ${id} does not exist`);

  return NextResponse.json({ customer });
}
