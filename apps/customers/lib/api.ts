import { auth } from "@vantigo/customers/lib/auth";
import { headers } from "next/headers";
import { NextResponse } from "next/server";
import type { ZodError } from "zod";

export interface ApiError {
  error: {
    code: string;
    message: string;
    details?: unknown;
  };
}

export function apiError(status: number, code: string, message: string, details?: unknown) {
  return NextResponse.json<ApiError>({ error: { code, message, details } }, { status });
}

export function validationError(error: ZodError) {
  return apiError(400, "VALIDATION_ERROR", "Request validation failed", error.issues);
}

/** Returns the session or a ready-to-return 401 response. */
export async function requireSession() {
  const session = await auth.api.getSession({ headers: await headers() });
  if (!session?.user) {
    return { session: null, response: apiError(401, "UNAUTHORIZED", "Authentication required") };
  }
  return { session, response: null };
}
