import type { StaffImportResult } from "../api/data";
import type { StaffDocument, StaffDocumentCategory } from "../api/documents";
import type { StaffEmployment, StaffTerms } from "../api/employments";
import type { StaffAbsenceType, StaffCapabilities, StaffHolidayScheme, StaffMeta, StaffStructure } from "../api/meta";
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
import type { StaffRetentionRun } from "../api/retention";
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
 * holding `staff:access` alone, who reads neither; `approve` a holder of
 * `staff:view` and `staff:approve` — the Approver, who decides everyone's
 * absence and writes nothing else. Being the person
 * themselves or their manager is not a role but a link (the stub's `me`).
 */
export type Role = "manage" | "view" | "manageOnly" | "personalData" | "personalDataOnly" | "access" | "approve";

/** Whether the role holds `staff:manage`: the one that writes a person, an employment and the structure. */
export const manages = (role: Role) => role === "manage" || role === "manageOnly" || role === "personalData";

/** Whether the role holds `staff:personal-data`. */
export const holdsPersonalData = (role: Role) => role === "personalData" || role === "personalDataOnly";

export const capabilitiesOf = (role: Role): StaffCapabilities => ({
  canEdit: manages(role),
  canSeePay: manages(role) || holdsPersonalData(role),
  canRevealIdentifiers: holdsPersonalData(role),
  canSeeHealth: holdsPersonalData(role),
  canViewDirectory: role === "manage" || role === "view" || role === "personalData" || role === "approve",
  // staff:manage and staff:approve decide absence; no fixture role manages anyone by the terms.
  canDecide: manages(role) || role === "approve",
  managesAnyone: false,
  // Running and disabling accounts is staff:manage's (delivery C).
  canRunAccounts: manages(role),
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

/** Some of the seeded absence types, as `/meta` answers the ones a caller may pick: a day type drawing on the holiday account, the sick kinds, an HR-only leave, a leave allowing a percentage and an hour type. */
export const absenceTypes = (): StaffAbsenceType[] => [
  {
    id: 1001,
    key: "ferie",
    names: { en: "Holiday", nb: "Ferie" },
    kind: "holiday",
    unit: "days",
    paid: true,
    drawsHoliday: true,
    allowsPercent: false,
    needsDecision: true,
    visibility: "absent",
    seeded: true,
    hidden: false,
    reserved: false,
    sort: 1,
    revision: 1,
  },
  {
    id: 1002,
    key: "egenmelding",
    names: { en: "Self-certified sick leave", nb: "Egenmelding" },
    kind: "sick_self",
    unit: "days",
    paid: true,
    drawsHoliday: false,
    allowsPercent: false,
    needsDecision: false,
    visibility: "absent",
    seeded: true,
    hidden: false,
    reserved: false,
    sort: 2,
    revision: 1,
  },
  {
    id: 1003,
    key: "sykmelding",
    names: { en: "Certified sick leave", nb: "Sykmelding" },
    kind: "sick_certified",
    unit: "days",
    paid: true,
    drawsHoliday: false,
    allowsPercent: false,
    needsDecision: false,
    visibility: "absent",
    seeded: true,
    hidden: false,
    reserved: false,
    sort: 3,
    revision: 1,
  },
  {
    id: 1005,
    key: "svangerskapspermisjon",
    names: { en: "Pregnancy leave", nb: "Svangerskapspermisjon" },
    kind: "leave",
    unit: "days",
    paid: false,
    drawsHoliday: false,
    allowsPercent: false,
    needsDecision: true,
    visibility: "hr_only",
    ameldingLeave: "permisjonMedForeldrepenger",
    seeded: true,
    hidden: false,
    reserved: false,
    sort: 5,
    revision: 1,
  },
  {
    id: 1007,
    key: "foreldrepermisjon",
    names: { en: "Parental leave", nb: "Foreldrepermisjon" },
    kind: "leave",
    unit: "days",
    paid: false,
    drawsHoliday: false,
    allowsPercent: true,
    needsDecision: true,
    visibility: "absent",
    ameldingLeave: "permisjonMedForeldrepenger",
    seeded: true,
    hidden: false,
    reserved: false,
    sort: 7,
    revision: 1,
  },
  {
    id: 1010,
    key: "sykt_barn",
    names: { en: "Sick child", nb: "Sykt barn" },
    kind: "child_sick",
    unit: "days",
    paid: true,
    drawsHoliday: false,
    allowsPercent: false,
    needsDecision: false,
    visibility: "absent",
    seeded: true,
    hidden: false,
    reserved: false,
    sort: 10,
    revision: 1,
  },
  {
    id: 1020,
    key: "helse",
    names: { en: "Doctor or dentist", nb: "Lege eller tannlege" },
    kind: "other",
    unit: "hours",
    paid: true,
    drawsHoliday: false,
    allowsPercent: false,
    needsDecision: false,
    visibility: "absent",
    seeded: true,
    hidden: false,
    reserved: false,
    sort: 20,
    revision: 1,
  },
];

/** The statutory holiday scheme, the default. */
export const holidaySchemes = (): StaffHolidayScheme[] => [
  {
    id: 1001,
    name: "Statutory (ferieloven)",
    entitlementVirkedager: 25,
    extraFrom60Virkedager: 6,
    mainPeriodVirkedager: 18,
    afterSeptemberVirkedager: 6,
    maxCarryOverVirkedager: 12,
    converted: { entitlement: 21, extraFrom60: 5, mainPeriod: 15, afterSeptember: 5, maxCarryOver: 10 },
    isDefault: true,
    seeded: true,
    revision: 1,
  },
];

export const meta = (role: Role = "manage", overrides: Partial<StaffMeta> = {}): StaffMeta => ({
  capabilities: capabilitiesOf(role),
  timeZone: "Europe/Oslo",
  today: TODAY,
  absenceTypes: absenceTypes(),
  holidaySchemes: holidaySchemes(),
  // The structure is staff:view's and staff:manage's (plan reading 15).
  ...(readsDirectory(role) ? { structure: structure() } : {}),
  ...overrides,
});

export const settings = (overrides: Partial<StaffSettings> = {}): StaffSettings => ({
  employeeNumberPrefix: "E-",
  nextEmployeeNumber: 8,
  timeZone: "Europe/Oslo",
  pensionProvider: "Storebrand",
  retentionGraceDays: 90,
  healthRetentionDays: 365,
  retention: { pendingDeletions: 0, failingDeletions: 0 },
  thresholdsAcknowledged: {},
  selfCertPolicy: "minimum",
  selfCertDaysPerOccurrence: 3,
  selfCertOccurrencesPerYear: 4,
  selfCertDaysPerYear: 24,
  selfCertAfterMonths: 2,
  autoCarryOver: false,
  createBeforeDays: 14,
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
  | "childrenUnder12"
  | "soleCarer"
  | "chronicallyIllChildren"
  | "legalHold"
  | "reassessedAt"
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
  onLeaveToday: false,
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

/**
 * Charles Babbage, a former employee the retention run erased in July 2025:
 * his last employment ended on 31 March 2025, so his reassessment has been
 * due since 31 March 2026. What is left is the C class — his names, his
 * birth date, his employment and the history; his contact details, next of
 * kin and sealed values are gone. Overrides give the held and the
 * reassessed variants.
 */
export const erasedPerson = (overrides: Partial<StoredPerson> = {}, record: PersonRecord = {}): StoredPerson =>
  person({
    id: 106,
    givenName: "Charles",
    familyName: "Babbage",
    displayName: "Charles Babbage",
    employeeNumber: "E-6",
    workEmail: undefined,
    status: { state: "ended", date: "2025-03-31" },
    erasedAt: "2025-07-01T02:00:00Z",
    department: undefined,
    ...unplaced,
    record: { revision: 7, birthDate: "1971-12-26", legalHold: { held: false }, ...record },
    ...overrides,
  });

/** Charles Babbage's one employment, ended on 31 March 2025, with its one terms row. */
export const erasedEmployment = (): StaffEmployment => ({
  id: 206,
  personId: 106,
  employmentNumber: "E-6-1",
  startDate: "2019-02-01",
  endDate: "2025-03-31",
  endReason: "employee_resigned",
  businessUnitId: 11,
  relationshipType: "ordinary",
  fteChangedAt: "2019-02-01",
  revision: 3,
});

export const erasedTerms = (): StaffTerms => ({
  id: 306,
  employmentId: 206,
  validFrom: "2019-02-01",
  employmentType: "fast",
  appointmentForm: "fast",
  ftePercent: 100,
  workingHoursScheme: "not_shift",
  fullTimeHoursPerWeek: 37.5,
  title: "Analyst",
  revision: 1,
});

/** The register with Charles Babbage erased among the others, for `stubStaffApi`. */
export const withErased = (person: StoredPerson = erasedPerson()) => ({
  people: [...peopleWithRecords(), person],
  employments: [...employments(), erasedEmployment()],
  terms: [...terms(), erasedTerms()],
});

/** Ada under a legal hold HR set in September. */
export const heldPeople = (): StoredPerson[] =>
  peopleWithRecords().map((one) =>
    one.id === 101
      ? {
          ...one,
          record: {
            ...(one.record as PersonRecord),
            legalHold: { held: true, reason: "Wage claim before the court", at: "2026-09-02T10:00:00Z" },
          },
        }
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

/** The six seeded categories (classes A, A, D, D, E, D; restricted ⇔ E), a company one and an archived one. */
export const documentCategories = (): StaffDocumentCategory[] => [
  ...(
    [
      [1, "contract", "Contract", "Arbeidsavtale", "A"],
      [2, "attest", "Attest", "Attest", "A"],
      [3, "certificate", "Certificate", "Sertifikat", "D"],
      [4, "correspondence", "Correspondence", "Korrespondanse", "D"],
      [5, "health", "Health", "Helse", "E"],
      [6, "other", "Other", "Annet", "D"],
    ] as const
  ).map(([id, key, en, nb, documentClass]) => ({
    id,
    key,
    names: { en, nb },
    class: documentClass,
    restricted: documentClass === "E",
    seeded: true,
    sort: id,
    revision: 1,
  })),
  {
    id: 7,
    key: "training",
    names: { en: "Training record", nb: "Opplæringsbevis" },
    class: "D",
    restricted: false,
    seeded: false,
    sort: 7,
    revision: 2,
  },
  {
    id: 8,
    key: "old-forms",
    names: { en: "Old forms", nb: "Gamle skjema" },
    class: "D",
    restricted: false,
    seeded: false,
    sort: 8,
    revision: 3,
    archivedAt: "2026-02-01T09:00:00Z",
  },
];

/** A document filed under one of `documentCategories()`, as every document carries its category. */
const filed = (
  categoryId: number,
  document: Omit<StaffDocument, "category" | "revision" | "contentType"> &
    Partial<Pick<StaffDocument, "revision" | "contentType">>,
): StaffDocument => {
  const category = documentCategories().find((one) => one.id === categoryId) as StaffDocumentCategory;
  return {
    contentType: "application/pdf",
    revision: 1,
    ...document,
    category: {
      id: category.id,
      key: category.key,
      names: category.names,
      class: category.class,
      restricted: category.restricted,
    },
  };
};

/**
 * Ada's archive, newest first — her signed contract, a certificate that has
 * expired, one valid until next year and a sick note (a health document) —
 * and one document of Grace's.
 */
export const documents = (): StaffDocument[] => [
  filed(5, {
    id: 504,
    personId: 101,
    fileName: "sick-note.pdf",
    sizeBytes: 48_000,
    uploadedAt: "2026-09-20T10:00:00Z",
  }),
  filed(3, {
    id: 503,
    personId: 101,
    fileName: "førstehjelp.png",
    contentType: "image/png",
    sizeBytes: 2_400_000,
    validTo: "2027-05-01",
    uploadedAt: "2026-05-02T10:00:00Z",
  }),
  filed(3, {
    id: 502,
    personId: 101,
    fileName: "forklift-licence.pdf",
    sizeBytes: 310_000,
    validTo: "2026-06-30",
    uploadedAt: "2025-03-01T10:00:00Z",
  }),
  filed(1, {
    id: 501,
    personId: 101,
    employmentId: 201,
    fileName: "employment-contract.pdf",
    sizeBytes: 125_000,
    signedAt: "2024-12-15",
    uploadedAt: "2024-12-16T10:00:00Z",
    revision: 2,
  }),
  filed(6, {
    id: 505,
    personId: 102,
    fileName: "grace-reference.pdf",
    sizeBytes: 90_000,
    uploadedAt: "2026-02-01T10:00:00Z",
  }),
];

/**
 * What the fake's import answers for any file, dry or real (the `dryRun` its
 * own): three rows, one created, one updated and one failed on its work
 * email — row 2 of the import modal's test file.
 */
export const importTally = (): Omit<StaffImportResult, "dryRun"> => ({
  rows: 3,
  created: 1,
  updated: 1,
  failed: 1,
  errors: [{ row: 2, column: "workEmail", message: "A work email must look like name@example.com, but was 'nope'" }],
});

/** A run by hand that erased one person's personnel record and another's health documents and pay. */
export const retentionRun = (): StaffRetentionRun => ({
  erased: {
    kinds: [
      { kind: "D", count: 3 },
      { kind: "health", count: 1 },
      { kind: "A", count: 2 },
    ],
  },
  deletions: { attempted: 4, succeeded: 4 },
});
