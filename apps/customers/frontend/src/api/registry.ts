import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

export { ApiConflictError, NotFoundError } from "./request";

/** The 409 a refresh answers when the customer has no Norwegian organisation number to look up (design D2). */
export const NO_REGISTRY_IDENTITY_CODE = "no_registry_identity";

/**
 * One of the two addresses Enhetsregisteret holds for an entity (design D1).
 * `lines` is the registry's own free-form array — one or more street lines,
 * never a single string — and `postalCode`/`municipality` are simply not
 * there on a foreign address, whose postal district is part of `city`
 * instead ("81-336 GDYNIA"). This is what the registry says, not an address
 * on file: nothing here is ever written to the customer's own addresses
 * (design D3), it is only offered.
 */
export interface CustomerRegistryAddress {
  lines: string[];
  postalCode: string | null;
  city: string | null;
  municipality: string | null;
  countryCode: string;
}

/**
 * What Enhetsregisteret says about this customer, as of `fetchedAt` (design
 * D1): the registry's own view, kept beside the customer and never edited by
 * hand. A difference between it and the legal identity the user asserted is
 * reported — on the timeline, and by the card's rename notice — rather than
 * written over either.
 */
export interface CustomerRegistryRecord {
  organisationNumber: string;
  name: string;
  organisationFormCode: string | null;
  organisationForm: string | null;
  industryCode: string | null;
  industry: string | null;
  /** Null when the entity has not registered an employee count at all, which is not the same as zero. */
  employees: number | null;
  vatRegistered: boolean;
  bankrupt: boolean;
  underLiquidation: boolean;
  underForcedLiquidation: boolean;
  /** Set for an entity struck from the register; one removed from open data has no record at all. */
  deletedOn: string | null;
  foundedOn: string | null;
  website: string | null;
  email: string | null;
  phone: string | null;
  mobile: string | null;
  parentOrganisationNumber: string | null;
  businessAddress: CustomerRegistryAddress | null;
  postalAddress: CustomerRegistryAddress | null;
  fetchedAt: string;
}

/** The four answers a refresh can give (design D2). */
export type CustomerRegistryRefreshStatus = "found" | "deleted" | "removed" | "unknown";

/**
 * One field the last refresh found different (design D4). `field` is the
 * record's own field name ("name", "employees", "businessAddress", …), or
 * "removedFromOpenData" when the registry answered 410; `from`/`to` are the
 * two values as text, either side null when it was empty.
 */
export interface CustomerRegistryChange {
  field: string;
  from: string | null;
  to: string | null;
}

/**
 * What one `POST .../registry-refresh` found (design D2, D4). `record` is the
 * stored record after the write — present for `found` and `deleted` only,
 * since `removed` deletes the stored record and `unknown` stores nothing.
 */
export interface CustomerRegistryRefreshResult {
  status: CustomerRegistryRefreshStatus;
  record: CustomerRegistryRecord | null;
  changes: CustomerRegistryChange[];
}

/**
 * The record as it actually arrives: every optional field is `omitempty` on
 * the wire (`registryRecordResponse` in
 * `apps/server/internal/customers/registry.go`), so a company with no mobile
 * number comes back without `mobile` rather than with null. Absent and null
 * mean the same thing here — the registry holds nothing for that field — so
 * the boundary maps them to the one shape the rest of the package reads, as
 * addresses and the billing profile already do.
 */
type RawCustomerRegistryAddress = Pick<CustomerRegistryAddress, "lines" | "countryCode"> &
  Partial<Pick<CustomerRegistryAddress, "postalCode" | "city" | "municipality">>;

type RawCustomerRegistryRecord = Pick<
  CustomerRegistryRecord,
  | "organisationNumber"
  | "name"
  | "vatRegistered"
  | "bankrupt"
  | "underLiquidation"
  | "underForcedLiquidation"
  | "fetchedAt"
> &
  Partial<
    Omit<
      CustomerRegistryRecord,
      | "organisationNumber"
      | "name"
      | "vatRegistered"
      | "bankrupt"
      | "underLiquidation"
      | "underForcedLiquidation"
      | "fetchedAt"
      | "businessAddress"
      | "postalAddress"
    >
  > & {
    businessAddress?: RawCustomerRegistryAddress;
    postalAddress?: RawCustomerRegistryAddress;
  };

type RawCustomerRegistryRefreshResult = {
  status: CustomerRegistryRefreshStatus;
  record?: RawCustomerRegistryRecord;
  changes?: (Pick<CustomerRegistryChange, "field"> & Partial<Omit<CustomerRegistryChange, "field">>)[];
};

const normalizeAddress = (raw?: RawCustomerRegistryAddress): CustomerRegistryAddress | null =>
  raw
    ? {
        lines: raw.lines ?? [],
        postalCode: raw.postalCode ?? null,
        city: raw.city ?? null,
        municipality: raw.municipality ?? null,
        countryCode: raw.countryCode,
      }
    : null;

const normalizeRecord = (raw: RawCustomerRegistryRecord): CustomerRegistryRecord => ({
  organisationNumber: raw.organisationNumber,
  name: raw.name,
  organisationFormCode: raw.organisationFormCode ?? null,
  organisationForm: raw.organisationForm ?? null,
  industryCode: raw.industryCode ?? null,
  industry: raw.industry ?? null,
  employees: raw.employees ?? null,
  vatRegistered: raw.vatRegistered,
  bankrupt: raw.bankrupt,
  underLiquidation: raw.underLiquidation,
  underForcedLiquidation: raw.underForcedLiquidation,
  deletedOn: raw.deletedOn ?? null,
  foundedOn: raw.foundedOn ?? null,
  website: raw.website ?? null,
  email: raw.email ?? null,
  phone: raw.phone ?? null,
  mobile: raw.mobile ?? null,
  parentOrganisationNumber: raw.parentOrganisationNumber ?? null,
  businessAddress: normalizeAddress(raw.businessAddress),
  postalAddress: normalizeAddress(raw.postalAddress),
  fetchedAt: raw.fetchedAt,
});

/**
 * The stored record, or null when there is none. A 204 means both "nothing
 * has ever been fetched" and "the caller may not see the legal identity this
 * record repeats" (design D2) — deliberately indistinguishable, the way the
 * legal identity's own GET already is, so the card says the honest thing for
 * either: nothing fetched yet.
 */
async function fetchRegistryRecord(customerId: number, signal?: AbortSignal): Promise<CustomerRegistryRecord | null> {
  const raw = await request<RawCustomerRegistryRecord | undefined>(`/api/v1/customers/${customerId}/registry-record`, {
    signal,
  });
  return raw ? normalizeRecord(raw) : null;
}

export const customerRegistryRecordQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: ["customers", customerId, "registry-record"],
    queryFn: ({ signal }) => fetchRegistryRecord(customerId, signal),
  });

/**
 * Re-reads Enhetsregisteret for this customer (design D2) — always the user's
 * own click. The answer's `changes` are what differed from the record on
 * file, already written to the timeline server-side as one `registry.change`
 * event, so a caller that shows them also has a stale timeline query on its
 * hands. Nothing here touches the customer row, and so nothing touches its
 * revision.
 */
export const refreshRegistryRecord = async (customerId: number): Promise<CustomerRegistryRefreshResult> => {
  const raw = await request<RawCustomerRegistryRefreshResult>(`/api/v1/customers/${customerId}/registry-refresh`, {
    method: "POST",
  });
  return {
    status: raw.status,
    record: raw.record ? normalizeRecord(raw.record) : null,
    // Go marshals a nil slice as null, so an empty diff can arrive either way.
    changes: (raw.changes ?? []).map((change) => ({
      field: change.field,
      from: change.from ?? null,
      to: change.to ?? null,
    })),
  };
};
