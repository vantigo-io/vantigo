import { apiError, requireSession } from "@vantigo/customers/lib/api";
import {
  isOrgnrQuery,
  mapBrregEnhet,
  mapBrregSearchResponse,
  normalizeOrgnr,
} from "@vantigo/customers/lib/customers/brreg";
import { type NextRequest, NextResponse } from "next/server";

const BRREG_BASE = "https://data.brreg.no/enhetsregisteret/api";

export async function GET(request: NextRequest) {
  const { response } = await requireSession();
  if (response) return response;

  const query = request.nextUrl.searchParams.get("q")?.trim() ?? "";
  if (query.length < 2) return NextResponse.json({ results: [] });

  try {
    if (isOrgnrQuery(query)) {
      const upstream = await fetch(`${BRREG_BASE}/enheter/${normalizeOrgnr(query)}`, {
        headers: { accept: "application/json" },
        signal: AbortSignal.timeout(5000),
      });
      if (upstream.status === 404) return NextResponse.json({ results: [] });
      if (!upstream.ok) throw new Error(`brreg responded ${upstream.status}`);
      const company = mapBrregEnhet(await upstream.json());
      return NextResponse.json({ results: company ? [company] : [] });
    }

    const params = new URLSearchParams({ navn: query, size: "8" });
    const upstream = await fetch(`${BRREG_BASE}/enheter?${params}`, {
      headers: { accept: "application/json" },
      signal: AbortSignal.timeout(5000),
    });
    if (!upstream.ok) throw new Error(`brreg responded ${upstream.status}`);
    return NextResponse.json({ results: mapBrregSearchResponse(await upstream.json()) });
  } catch (error) {
    console.error("[brreg] lookup failed:", error);
    return apiError(502, "BRREG_UNAVAILABLE", "Company register lookup failed");
  }
}
