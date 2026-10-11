import type { StaffCapabilities, StaffMeta, StaffStructure } from "../api/meta";
import type { StaffPersonListItem } from "../api/people";
import type { StaffSettings } from "../api/settings";
import type { StaffBusinessUnit, StaffDepartment, StaffLegalEntity, StaffLocation } from "../api/structure";

/** The business zone's day every fixture is judged on — what `/meta` answers as `today`. */
export const TODAY = "2026-10-11";

/**
 * Who is asking. `manage` is HR (`staff:manage` with `staff:view`), `view` a
 * colleague holding `staff:view` alone, `manageOnly` an HR role without
 * `staff:view` — it still lists people and reads the structure — and
 * `access` somebody holding `staff:access` alone, who reads neither.
 */
export type Role = "manage" | "view" | "manageOnly" | "access";

const manages = (role: Role) => role === "manage" || role === "manageOnly";

export const capabilitiesOf = (role: Role): StaffCapabilities => ({
  canEdit: manages(role),
  canSeePay: manages(role),
  canRevealIdentifiers: false,
  canViewDirectory: role === "manage" || role === "view",
});

export const legalEntities = (): StaffLegalEntity[] => [
  { id: 1, name: "Fjord Systems AS", orgNumber: "914778271", isDefault: true, revision: 1 },
  { id: 2, name: "Fjord Holding AS", isDefault: false, revision: 4 },
];

export const businessUnits = (): StaffBusinessUnit[] => [
  { id: 11, legalEntityId: 1, name: "Oslo office", orgNumber: "974760673", isDefault: true, revision: 1 },
  { id: 12, legalEntityId: 2, name: "Bergen office", isDefault: false, revision: 2 },
  {
    id: 13,
    legalEntityId: 1,
    name: "Old Drammen office",
    isDefault: false,
    revision: 3,
    archivedAt: "2026-01-01T10:00:00Z",
  },
];

export const departments = (): StaffDepartment[] => [
  { id: 21, code: "ENG", name: "Engineering", managerPersonId: 101, revision: 1 },
  { id: 22, code: "OPS", name: "Operations", parentId: 21, revision: 1 },
];

export const locations = (): StaffLocation[] => [
  {
    id: 31,
    name: "Oslo HQ",
    street: "Karl Johans gate 1",
    postalCode: "0154",
    city: "Oslo",
    country: "NO",
    revision: 1,
  },
  { id: 32, name: "Bergen", city: "Bergen", country: "NO", revision: 1 },
];

export const structure = (): StaffStructure => ({
  legalEntities: legalEntities(),
  businessUnits: businessUnits(),
  departments: departments(),
  locations: locations(),
});

export const meta = (role: Role = "manage", overrides: Partial<StaffMeta> = {}): StaffMeta => ({
  capabilities: capabilitiesOf(role),
  timeZone: "Europe/Oslo",
  today: TODAY,
  // The structure is staff:view's and staff:manage's (plan reading 15).
  ...(role === "access" ? {} : { structure: structure() }),
  ...overrides,
});

export const settings = (overrides: Partial<StaffSettings> = {}): StaffSettings => ({
  employeeNumberPrefix: "E-",
  nextEmployeeNumber: 8,
  timeZone: "Europe/Oslo",
  pensionProvider: "Storebrand",
  revision: 3,
  ...overrides,
});

/**
 * A person as the directory lists them, plus where their employment in
 * force sits — the business unit and the location are not on the wire, but
 * the server filters on them, so the fake has to know them.
 */
export interface StoredPerson extends StaffPersonListItem {
  unitId?: number;
  locationId?: number;
}

export const person = (overrides: Partial<StoredPerson> = {}): StoredPerson => ({
  id: 101,
  givenName: "Ada",
  familyName: "Lovelace",
  displayName: "Ada Lovelace",
  employeeNumber: "E-1",
  workEmail: "ada@fjord.example",
  workCountry: "NO",
  preferredLanguage: "en",
  status: { state: "active" },
  department: { id: 21, code: "ENG", name: "Engineering" },
  unitId: 11,
  locationId: 31,
  ...overrides,
});

/** One of each standing: active with a manager, upcoming, ended, and nobody's employee yet. */
export const people = (): StoredPerson[] => [
  person(),
  person({
    id: 102,
    givenName: "Grace",
    familyName: "Hopper",
    displayName: "Grace Hopper",
    employeeNumber: "E-2",
    workEmail: "grace@fjord.example",
    manager: { id: 101, displayName: "Ada Lovelace", employeeNumber: "E-1" },
    department: { id: 22, code: "OPS", name: "Operations" },
    unitId: 12,
    locationId: 32,
  }),
  person({
    id: 103,
    givenName: "Alan",
    familyName: "Turing",
    displayName: "Alan Turing",
    employeeNumber: "E-3",
    workEmail: "alan@fjord.example",
    status: { state: "upcoming", date: "2026-11-01" },
  }),
  person({
    id: 104,
    givenName: "Edsger",
    familyName: "Dijkstra",
    displayName: "Edsger Dijkstra",
    employeeNumber: "E-4",
    workEmail: undefined,
    status: { state: "ended", date: "2026-06-30" },
    department: undefined,
  }),
  person({
    id: 105,
    givenName: "Barbara",
    familyName: "Liskov",
    displayName: "Barbara Liskov",
    employeeNumber: "E-5",
    workEmail: undefined,
    status: { state: "none" },
    department: undefined,
    unitId: undefined,
    locationId: undefined,
  }),
];
