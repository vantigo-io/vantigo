import { requireSession } from "@vantigo/customers/lib/api";
import { getCustomerStats } from "@vantigo/customers/lib/customers/queries";
import { NextResponse } from "next/server";

export async function GET() {
  const { response } = await requireSession();
  if (response) return response;

  const stats = await getCustomerStats();
  return NextResponse.json({ stats });
}
