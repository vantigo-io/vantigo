import type { StaffEmployment, StaffTerms } from "../api/employments";
import type { StaffCapabilities, StaffMeta, StaffStructure } from "../api/meta";
import type {
  StaffAssignableUser,
  StaffBankAccountKind,
  StaffEvent,
  StaffNationalIdKind,
  StaffNextOfKin,
  StaffPerson,
  StaffPersonListItem,
  StaffPersonRole,
} from "../api/people";
import type { StaffSettings } from "../api/settings";
import type { StaffBusinessUnit, StaffDepartment, StaffLegalEntity, StaffLocation } from "../api/structure";

/** The business zone's day every fixture is judged on — what `/meta` answers as `today`. */
export const TODAY = "2026-10-11";

/**
 * Who is asking. `manage` is HR (`staff:manage` with `staff:view`), `view` a
 * colleague holding `staff:view` alone, `manageOnly` an HR role without
 * `staff:view` — it still lists people and reads the structure —
 * `personalData` HR who also holds `staff:personal-data`,
 * `personalDataOnly` a role holding `staff:personal-data` alone — it reads
 * every group of anyone and reveals and sets the sealed fields, but edits
 * nothing else, lists nobody and reads no structure — and `access` somebody
 * holding `staff:access` alone, who reads neither. Being the person
 * themselves or their manager is not a role but a link (the stub's `me`).
 */
export type Role = "manage" | "view" | "manageOnly" | "personalData" | "personalDataOnly" | "access";

/** Whether the role holds `staff:manage`: the one that writes a person, an employment and the structure. */
export const manages = (role: Role) => role === "manage" || role === "manageOnly" || role === "personalData";

/** Whether the role holds `staff:personal-data`. */
export const holdsPersonalData = (role: Role) => role === "personalData" || role === "personalDataOnly";

export const capabilitiesOf = (role: Role): StaffCapabilities => ({
  canEdit: manages(role),
  canSeePay: manages(role) || holdsPersonalData(role),
  canRevealIdentifiers: holdsPersonalData(role),
  canSeeHealth: holdsPersonalData(role),
  canViewDirectory: role === "manage" || role === "view" || role === "personalData",
});

/** Whether the role lists people and reads the structure: `staff:view`'s and `staff:manage`'s. */
export const readsDirectory = (role: Role) => capabilitiesOf(role).canViewDirectory || manages(role);

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
  ...(readsDirectory(role) ? { structure: structure() } : {}),
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
 * A person as the directory lists them, plus where their employment sits for
 * the structure filters — today's, or a starter's first terms' — which the
 * server filters on but does not answer, so the fake has to know them.
 */
export interface StoredPerson extends StaffPersonListItem {
  unitId?: number;
  locationId?: number;
  /** What the record holds beyond the directory's columns — the private group, HR's notes and the rest. */
  record?: PersonRecord;
}

/** The sealed values as the fake keeps them — what a reveal opens. */
export interface Sealed {
  nationalId?: { kind: StaffNationalIdKind; value: string; country?: string };
  bankAccount?: { kind: StaffBankAccountKind; value: string };
}

export type PersonRecord = Pick<
  StaffPerson,
  | "birthDate"
  | "gender"
  | "phoneMobile"
  | "phonePrivate"
  | "privateEmail"
  | "street"
  | "postalCode"
  | "city"
  | "country"
  | "notes"
> & {
  revision?: number;
  sealed?: Sealed;
  nextOfKin?: StaffNextOfKin[];
  roles?: StaffPersonRole[];
  /** The user the self-service link names. */
  user?: { id: string; displayName: string; active: boolean };
};

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
  title: "Senior engineer",
  businessUnit: { id: 11, name: "Oslo office" },
  location: { id: 31, name: "Oslo HQ" },
  unitId: 11,
  locationId: 31,
  ...overrides,
});

/** Nothing in force today: no place to name. */
const unplaced = { title: undefined, businessUnit: undefined, location: undefined } as const;

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
    title: "Operator",
    businessUnit: { id: 12, name: "Bergen office" },
    location: { id: 32, name: "Bergen" },
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
    ...unplaced,
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
    ...unplaced,
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
    ...unplaced,
    unitId: undefined,
    locationId: undefined,
  }),
];

/** Ada's record: everything the private group, HR's notes and the sealed fields hold. */
export const adaRecord = (): PersonRecord => ({
  birthDate: "1990-12-10",
  gender: "female",
  phoneMobile: "+47 912 34 567",
  privateEmail: "ada@home.example",
  street: "Storgata 1",
  postalCode: "0155",
  city: "Oslo",
  country: "NO",
  notes: "Asked for a standing desk",
  revision: 3,
  sealed: {
    nationalId: { kind: "fodselsnummer", value: "10129049905" },
    bankAccount: { kind: "bban", value: "12345678903" },
  },
  nextOfKin: [{ name: "Byron Lovelace", relationship: "spouse", phone: "+47 400 00 000" }],
  roles: [{ role: "verneombud", fromDate: "2026-01-01" }],
  user: { id: "8d5c6a3e-0000-4000-8000-000000000101", displayName: "Ada at Fjord", active: true },
});

/** The directory with Ada's record filled in — the person most page tests open. */
export const peopleWithRecords = (): StoredPerson[] =>
  people().map((one) =>
    one.id === 101
      ? { ...one, record: adaRecord() }
      : one.id === 102
        ? { ...one, record: { revision: 1, birthDate: "1985-06-01", phoneMobile: "+47 999 88 777" } }
        : one,
  );

/** The users the link picker finds. */
export const assignableUsers = (): (StaffAssignableUser & { active: boolean })[] => [
  { id: "8d5c6a3e-0000-4000-8000-000000000101", displayName: "Ada at Fjord", active: true },
  { id: "8d5c6a3e-0000-4000-8000-000000000102", displayName: "Grace at Fjord", active: true },
  { id: "8d5c6a3e-0000-4000-8000-000000000199", displayName: "Gone Gunnar", active: false },
];

/**
 * Ada's employment with two terms rows — a raise and a new title from 1 March —
 * and Grace's, whose terms name Ada as her manager.
 */
export const employments = (): StaffEmployment[] => [
  {
    id: 201,
    personId: 101,
    employmentNumber: "E-1-1",
    startDate: "2025-01-01",
    businessUnitId: 11,
    relationshipType: "ordinary",
    contractSignedAt: "2024-12-15",
    fteChangedAt: "2026-03-01",
    salaryChangedAt: "2026-03-01",
    revision: 2,
  },
  {
    id: 202,
    personId: 102,
    employmentNumber: "E-2-1",
    startDate: "2026-01-15",
    businessUnitId: 12,
    relationshipType: "ordinary",
    fteChangedAt: "2026-01-15",
    salaryChangedAt: "2026-01-15",
    revision: 1,
  },
];

export const terms = (): StaffTerms[] => [
  {
    id: 301,
    employmentId: 201,
    validFrom: "2025-01-01",
    employmentType: "fast",
    appointmentForm: "fast",
    ftePercent: 100,
    workingHoursScheme: "not_shift",
    fullTimeHoursPerWeek: 37.5,
    title: "Engineer",
    departmentId: 21,
    locationId: 31,
    holidayTermsText: "Five weeks",
    payComponents: [{ name: "Base salary", amount: 650000, currency: "NOK", period: "yearly" }],
    payMethod: "Bank transfer",
    paydayText: "The 20th",
    note: "Hired through the referral programme",
    revision: 1,
  },
  {
    id: 302,
    employmentId: 201,
    validFrom: "2026-03-01",
    employmentType: "fast",
    appointmentForm: "fast",
    ftePercent: 80,
    workingHoursScheme: "not_shift",
    fullTimeHoursPerWeek: 37.5,
    title: "Senior engineer",
    occupationCode: "2512101",
    departmentId: 21,
    locationId: 31,
    holidayTermsText: "Five weeks",
    noticeEmployee: 3,
    noticeEmployer: 3,
    noticeUnit: "months",
    payComponents: [{ name: "Base salary", amount: 700000, currency: "NOK", period: "yearly" }],
    payMethod: "Bank transfer",
    paydayText: "The 20th",
    seniorityDate: "2020-01-01",
    payGrade: "L7",
    revision: 4,
  },
  {
    id: 303,
    employmentId: 202,
    validFrom: "2026-01-15",
    employmentType: "midlertidig",
    temporaryGround: "b",
    expectedEndDate: "2026-12-31",
    appointmentForm: "midlertidig",
    ftePercent: 50,
    workingHoursScheme: "not_shift",
    fullTimeHoursPerWeek: 37.5,
    title: "Operator",
    departmentId: 22,
    locationId: 32,
    managerPersonId: 101,
    payComponents: [{ name: "Hourly wage", amount: 320, currency: "NOK", period: "hourly" }],
    revision: 1,
  },
];

/** Ada's history, newest first: HR's change to her private and work fields, the pay correction, the note. */
export const events = (): Record<number, StaffEvent[]> => ({
  101: [
    {
      id: 403,
      kind: "national_id_revealed",
      at: "2026-10-10T09:30:00Z",
      actor: { kind: "user", displayName: "Hilde HR", userId: "8d5c6a3e-0000-4000-8000-000000000900" },
      payload: {},
    },
    {
      id: 402,
      kind: "terms_corrected",
      at: "2026-09-01T08:00:00Z",
      actor: { kind: "user", displayName: "Hilde HR", userId: "8d5c6a3e-0000-4000-8000-000000000900" },
      payload: {
        employmentId: 201,
        termsId: 302,
        fields: { payGrade: { from: "L6", to: "L7" }, title: { from: "Engineer II", to: "Senior engineer" } },
      },
    },
    {
      id: 401,
      kind: "person_updated",
      at: "2026-08-15T12:00:00Z",
      actor: { kind: "user", displayName: "Hilde HR", userId: "8d5c6a3e-0000-4000-8000-000000000900" },
      payload: {
        fields: {
          workEmail: { from: "ada@old.example", to: "ada@fjord.example" },
          birthDate: { from: "1990-12-01", to: "1990-12-10" },
          notes: { to: "Asked for a standing desk" },
        },
      },
    },
  ],
});
