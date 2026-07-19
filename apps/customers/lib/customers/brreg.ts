/**
 * Brønnøysundregistrene (brreg) company search.
 * Open data API, no key required: https://data.brreg.no/enhetsregisteret/api
 */

export interface BrregCompany {
  orgnr: string;
  name: string;
  orgForm?: string;
  city?: string;
  /** Voluntarily registered — often missing. */
  email?: string;
  /** Voluntarily registered — often missing. Landline preferred over mobile. */
  phone?: string;
}

/** Shape of the relevant parts of brreg's enhetsregisteret responses. */
interface BrregEnhet {
  organisasjonsnummer?: string;
  navn?: string;
  organisasjonsform?: { kode?: string };
  forretningsadresse?: { poststed?: string };
  epostadresse?: string | null;
  telefon?: string | null;
  mobil?: string | null;
}

/**
 * Parses a query as a Norwegian organisation number, tolerating whitespace.
 * Returns the orgnr as a number (orgnrs are 9 digits and never start with 0),
 * or null when the query is not orgnr-shaped. Interpolating the numeric value
 * into URLs is inherently injection-safe.
 */
export function parseOrgnrQuery(query: string): number | null {
  const compact = query.replace(/\s/g, "");
  if (!/^\d{9}$/.test(compact)) return null;
  const orgnr = Number(compact);
  return orgnr >= 100_000_000 ? orgnr : null;
}

/**
 * Brreg phone numbers are unnormalized (e.g. "51 99 00 00"). Strips spaces
 * and prefixes +47 for bare 8-digit Norwegian numbers.
 */
export function normalizeBrregPhone(value: string | null | undefined): string | undefined {
  if (!value) return undefined;
  const compact = value.replace(/\s/g, "");
  if (/^\d{8}$/.test(compact)) return `+47${compact}`;
  return compact;
}

export function mapBrregEnhet(enhet: BrregEnhet): BrregCompany | null {
  if (!enhet.organisasjonsnummer || !enhet.navn) return null;
  return {
    orgnr: enhet.organisasjonsnummer,
    name: enhet.navn,
    orgForm: enhet.organisasjonsform?.kode,
    city: enhet.forretningsadresse?.poststed,
    email: enhet.epostadresse ?? undefined,
    phone: normalizeBrregPhone(enhet.telefon) ?? normalizeBrregPhone(enhet.mobil),
  };
}

export function mapBrregSearchResponse(body: unknown): BrregCompany[] {
  const enheter = (body as { _embedded?: { enheter?: BrregEnhet[] } })?._embedded?.enheter;
  if (!Array.isArray(enheter)) return [];
  return enheter.map(mapBrregEnhet).filter((company): company is BrregCompany => company !== null);
}
