import type {
  StaffAvailabilityDeclaration,
  StaffCompetence,
  StaffCompetenceCategory,
  StaffCompetenceChange,
  StaffCompetenceGapItem,
  StaffCompetenceGapStatus,
  StaffCompetenceProfile,
  StaffCompetenceProfileItemInput,
  StaffCompetenceSummary,
  StaffPersonCompetence,
  StaffPersonInterest,
  StaffPersonPreference,
} from "../api/competences";
import type { StaffImportResult } from "../api/data";
import type { StaffDocument, StaffDocumentCategory } from "../api/documents";
import type {
  StaffContractCheckItem,
  StaffEmployment,
  StaffEmploymentInput,
  StaffTerms,
  StaffTermsInput,
} from "../api/employments";
import type { StaffGroup, StaffGroupProvider, StaffPersonGroup } from "../api/groups";
import type { StaffMeta } from "../api/meta";
import type { StaffAssignableUser, StaffEvent, StaffPerson, StaffPersonInput } from "../api/people";
import type { StaffRetentionRun } from "../api/retention";
import type { StaffSettings, StaffSettingsInput, StaffThreshold } from "../api/settings";
import type { StaffBusinessUnit, StaffDepartment, StaffLegalEntity, StaffLocation } from "../api/structure";
import type { components } from "../api-schema";
import {
  availabilityKinds,
  daysUntil,
  preferenceKinds,
  proposedExpiry,
  remoteShares,
  sameDeclaration,
  timeOfDayParts,
  travelExtents,
} from "../lib/competences";
import { isIsoDate } from "../lib/dates";
import { defaultFullTimeHours, fullTimeHoursMismatch } from "../lib/terms";
import {
  allAbsenceTypes,
  approvals as defaultApprovals,
  calendarPeople as defaultCalendarPeople,
  careDays as defaultCareDays,
  cases as defaultCases,
  holidayAccount as defaultHolidayAccount,
  myAbsences as defaultMyAbsences,
  selfCertification as defaultSelfCertification,
  type PersonAbsence,
} from "./absence-fixtures";
import { jsonResponse } from "./api";
import {
  availabilityDeclarations as defaultAvailability,
  competenceCategories as defaultCompetenceCategories,
  competenceProfiles as defaultCompetenceProfiles,
  competenceReviews as defaultCompetenceReviews,
  competences as defaultCompetences,
  personCompetences as defaultPersonCompetences,
  personInterests as defaultPersonInterests,
  personPreferences as defaultPersonPreferences,
  personProfiles as defaultPersonProfiles,
  type StoredDeclaration,
  type StoredInterest,
  type StoredPersonProfile,
  type StoredPreference,
  type StoredReview,
} from "./competence-fixtures";
import { type StubbedFetch, stubFetch } from "./fetch";
import {
  capabilitiesOf,
  assignableUsers as defaultAssignableUsers,
  businessUnits as defaultBusinessUnits,
  departments as defaultDepartments,
  defaultDirectoryFields,
  documentCategories as defaultDocumentCategories,
  documents as defaultDocuments,
  employments as defaultEmployments,
  events as defaultEvents,
  holidaySchemes as defaultHolidaySchemes,
  importTally as defaultImportTally,
  legalEntities as defaultLegalEntities,
  locations as defaultLocations,
  meta as defaultMeta,
  peopleWithRecords as defaultPeople,
  retentionRun as defaultRetentionRun,
  settings as defaultSettings,
  terms as defaultTerms,
  holdsPersonalData,
  manages,
  type PersonRecord,
  type Role,
  readsDirectory,
  type StoredPerson,
  TODAY,
} from "./fixtures";
import {
  groups as defaultGroups,
  groupTraits as defaultGroupTraits,
  myGroups as defaultMyGroups,
} from "./group-fixtures";

export interface StaffServer {
  /** Who is asking; HR by default. It decides `/meta`'s capabilities and who `GET /people` lists. */
  role?: Role;
  meta?: StaffMeta | Response;
  /** The settings row, replaced by a successful PUT; read it back from the stub's `state`. */
  settings?: StaffSettings;
  legalEntities?: StaffLegalEntity[];
  businessUnits?: StaffBusinessUnit[];
  departments?: StaffDepartment[];
  locations?: StaffLocation[];
  /** The directory, the array the test passes, so a create shows up in the next list read. */
  people?: StoredPerson[];
  /** The person the caller's self-service link names: it makes them Self to that person and Manager to their reports. */
  me?: number;
  employments?: StaffEmployment[];
  terms?: StaffTerms[];
  /** Each person's history, newest first, as HR reads it; the fake shapes it for everyone else. */
  events?: Record<number, StaffEvent[]>;
  users?: (StaffAssignableUser & { active: boolean })[];
  /** Every person's documents, newest first, as `staff:personal-data` reads them; the fake shapes them per reader. */
  documents?: StaffDocument[];
  documentCategories?: StaffDocumentCategory[];
  /** What an import answers for any file, dry or real; the `dryRun` is the request's. */
  importTally?: Omit<StaffImportResult, "dryRun">;
  /** What `POST /retention/run` answers. */
  retentionRun?: StaffRetentionRun;
  /** What `POST /accounts/run` answers (delivery C); nothing attempted by default. */
  accountsRun?: Schemas["StaffAccountsRun"];
  /**
   * Each person's account (delivery C) as `GET /people/{id}/account` answers
   * it; a person not here has the flag off, the work address and — for one
   * HR linked by hand — the self-service link. Read the writes back from
   * the stub's `state`.
   */
  accounts?: Record<number, Schemas["StaffPersonAccount"]>;
  /** What `POST /people/{id}/account/pass` answers the once it answers (Entra B); a fake pass by default. */
  pass?: Schemas["StaffAccountPass"];
  /** Absence as the server answers it for the caller (delivery B): the fake hands it out as it is. */
  absence?: AbsenceServer;
  pageSize?: number;
  /** The groups (Entra delivery D), each with its members and owners; the fixture's three by default. */
  groups?: StaffGroup[];
  /** Each group's sections, by the group's id, as `GET /groups/{id}/traits` answers them. */
  groupTraits?: Record<number, StaffGroupProvider[]>;
  /** What `GET /me/groups` answers the caller linked to a person. */
  myGroups?: StaffPersonGroup[];
  /** The competence categories (staff phase 2); the five seeds and a company one by default. */
  competenceCategories?: StaffCompetenceCategory[];
  /** The competence catalogue as staff:manage reads it, hidden and archived included. */
  competences?: StaffCompetence[];
  /** Every person's competence rows with every detail field; the fake shapes them per reader. */
  personCompetences?: StaffPersonCompetence[];
  /** What each person wants (delivery B): their interests, preferences and availability, with the Seen stamps. */
  interests?: StoredInterest[];
  preferences?: StoredPreference[];
  availability?: StoredDeclaration[];
  /** The competence reviews recorded, any person's; the latest by day is the person's lastCompetenceReview. */
  reviews?: StoredReview[];
  /** The competence profiles (delivery C) with their items, archived ones included; the fixture's two by default. */
  competenceProfiles?: StaffCompetenceProfile[];
  /** The profiles HR assigned people; Grace's field electrician by default. */
  personProfiles?: StoredPersonProfile[];
  /** Answers a write instead of the fake's own; undefined falls through to it. */
  write?: (method: string, path: string, body: unknown) => Response | undefined;
  /** Answers a read instead of the fake's own, as `write` does a write. */
  read?: (path: string) => Response | undefined;
}

type Schemas = components["schemas"];

/**
 * Absence (delivery B), already shaped for the caller: the fake keeps the
 * rows and the figures the test gives and answers them as they are — the
 * server's judgment of who reads what is its own, and these tests prove the
 * pages draw what it sends.
 */
export interface AbsenceServer {
  /** The caller's holiday accounts, one per year; a year not here is the fixture's with that year. */
  holidayAccounts?: Schemas["StaffHolidayAccount"][];
  /** The caller's own absences, newest first, as `GET /me/absences` answers them. */
  myAbsences?: Schemas["StaffAbsence"][];
  selfCertification?: Schemas["StaffSelfCertification"];
  careDays?: Schemas["StaffCareDays"];
  /** The public holidays `GET /calendar` answers for any range, each kept when it falls in the range. */
  publicHolidays?: string[];
  calendarPeople?: Schemas["StaffCalendarPerson"][];
  /** The requests the caller decides, oldest first. */
  approvals?: Schemas["StaffApproval"][];
  /** The cases the caller reads, open and closed; `GET /cases` answers the open ones unless asked for all. */
  cases?: Schemas["StaffCase"][];
  /**
   * What the person page's Absence tab reads of each person, as the caller
   * reads it. A person not here is one whose absence the caller may not
   * read: their holiday account is the 404 of an unknown id.
   */
  people?: Record<number, PersonAbsence>;
  /** `GET /absence-types` as staff:manage reads it; the meta's types with a hidden seed, the reserved one and a company type by default. */
  types?: Schemas["StaffAbsenceType"][];
  /** `GET /holiday-schemes`; the meta's by default. */
  schemes?: Schemas["StaffHolidayScheme"][];
}

/** What the fake holds after the writes, for a test to read back. */
export interface StaffState {
  settings: StaffSettings;
  legalEntities: StaffLegalEntity[];
  businessUnits: StaffBusinessUnit[];
  departments: StaffDepartment[];
  locations: StaffLocation[];
  people: StoredPerson[];
  employments: StaffEmployment[];
  terms: StaffTerms[];
  events: Record<number, StaffEvent[]>;
  documents: StaffDocument[];
  documentCategories: StaffDocumentCategory[];
  myAbsences: Schemas["StaffAbsence"][];
  approvals: Schemas["StaffApproval"][];
  cases: Schemas["StaffCase"][];
  personAbsence: Record<number, PersonAbsence>;
  absenceTypes: Schemas["StaffAbsenceType"][];
  holidaySchemes: Schemas["StaffHolidayScheme"][];
  accounts: Record<number, Schemas["StaffPersonAccount"]>;
  /** How many passes `POST …/account/pass` has answered (Entra B). */
  passesRevealed: number;
  groups: StaffGroup[];
  groupTraits: Record<number, StaffGroupProvider[]>;
  competenceCategories: StaffCompetenceCategory[];
  competences: StaffCompetence[];
  personCompetences: StaffPersonCompetence[];
  interests: StoredInterest[];
  preferences: StoredPreference[];
  availability: StoredDeclaration[];
  reviews: StoredReview[];
  competenceProfiles: StaffCompetenceProfile[];
  personProfiles: StoredPersonProfile[];
}

export type StaffStub = StubbedFetch & { state: StaffState };

/** A refusal in the module's problem shape: the code, the field errors, the sentence. */
export const staffProblem = (
  status: number,
  { code, title, detail, errors }: { code?: string; title: string; detail?: string; errors?: Record<string, string[]> },
) =>
  jsonResponse(status, {
    title,
    status,
    ...(code ? { code } : {}),
    ...(detail ? { detail } : {}),
    ...(errors ? { errors } : {}),
  });

const notFound = () => new Response(null, { status: 404 });

/** A write on an erased person (design S15): read-only but for the hold, the reassessment, an unlink and a rehire. */
const erasedRefusal = () =>
  staffProblem(409, {
    code: "person_erased",
    title: "Person erased",
    detail:
      "This person was erased under the retention rules; their record is read-only but for a legal hold, the reassessment, an unlink and a rehire.",
  });

/** The calendar day before `date` (YYYY-MM-DD), in UTC as every Staff date is. */
const dayBefore = (date: string): string => {
  const day = new Date(`${date}T00:00:00.000Z`);
  day.setUTCDate(day.getUTCDate() - 1);
  return day.toISOString().slice(0, 10);
};

/** The self-service link's refusal of a user Staff's accounts manage (delivery C, plan reading 8). */
export const linkManaged = () =>
  staffProblem(409, {
    code: "link_managed",
    title: "Managed by Staff",
    detail:
      "This account is managed by Staff's accounts, so it is not linked or unlinked by hand. Use the person's Account tab.",
  });

/** Disable now's refusals once the flag is off (delivery C): the user is not Staff's to disable. */
const disableRefusal = (code: "account_not_managed" | "account_privileged" | "account_mismatch") =>
  staffProblem(409, {
    code,
    title: "Account not disabled",
    detail: "The account flag is off; the user was not disabled.",
  });

/** The access layer's uniform 403. */
const forbidden = () => jsonResponse(403, { error: { code: "forbidden", message: "Forbidden" } });

const page = <T>(data: T[], pageNumber: number, pageSize: number) => {
  const totalPages = Math.ceil(data.length / pageSize);
  const start = (pageNumber - 1) * pageSize;
  return {
    data: data.slice(start, start + pageSize),
    pagination: {
      page: pageNumber,
      pageSize,
      totalCount: data.length,
      totalPages,
      hasNextPage: pageNumber < totalPages,
      hasPreviousPage: pageNumber > 1,
    },
  };
};

/** What the wire carries of a stored person: the unit, the location and the record are the fake's own. */
const listItem = (stored: StoredPerson) => {
  const item: Partial<StoredPerson> = { ...stored };
  delete item.unitId;
  delete item.locationId;
  delete item.record;
  return item as Omit<StoredPerson, "unitId" | "locationId" | "record">;
};

/**
 * Who the caller is to one person (design §5): themselves through the `me`
 * link, their manager when the person's manager today is `me`, HR, and a
 * colleague holding the directory — who sees only the people active today.
 */
interface Audience {
  self: boolean;
  manager: boolean;
  hr: boolean;
  personalData: boolean;
  colleague: boolean;
}

type Group = "work" | "dates" | "employment" | "pay" | "private" | "notes" | "sealed" | "hold" | "careDays" | "links";

/** Design §5's field groups, as `shaping.go` keys them; a field not named is the employment group's. */
const groupOf: Record<string, Group> = {
  ...Object.fromEntries(
    [
      "employeeNumber",
      "givenName",
      "familyName",
      "displayName",
      "workEmail",
      "phoneWork",
      "workCountry",
      "preferredLanguage",
      "roles",
      "manager",
      "department",
      "businessUnit",
      "location",
    ].map((field) => [field, "work" as Group]),
  ),
  ...Object.fromEntries(
    ["status", "startDate", "endDate", "validFrom", "validTo", "employmentType", "ftePercent", "fteChangedAt"].map(
      (field) => [field, "dates" as Group],
    ),
  ),
  ...Object.fromEntries(
    [
      "remunerationType",
      "payComponents",
      "payMethod",
      "paydayText",
      "seniorityDate",
      "payGrade",
      "salaryChangedAt",
    ].map((field) => [field, "pay" as Group]),
  ),
  ...Object.fromEntries(
    [
      "birthDate",
      "gender",
      "phoneMobile",
      "phonePrivate",
      "privateEmail",
      "street",
      "postalCode",
      "city",
      "country",
      "nextOfKin",
    ].map((field) => [field, "private" as Group]),
  ),
  notes: "notes",
  note: "notes",
  nationalId: "sealed",
  bankAccount: "sealed",
  user: "sealed",
  // The account flag's fields are the links group's (plan reading 11).
  provisionAccount: "links",
  provisionFrom: "links",
  accountEmailKind: "links",
  // The hold group (design §5): staff:manage's and staff:personal-data's alone.
  erasedAt: "hold",
  legalHold: "hold",
  legalHoldReason: "hold",
  reassessedAt: "hold",
  // The care-day fields (design §5): the person's, HR's and staff:personal-data's.
  childrenUnder12: "careDays",
  soleCarer: "careDays",
  chronicallyIllChildren: "careDays",
};

/**
 * The person's own response where it names a field as a terms row does: the
 * title in force today is the work group's, while a terms row's title stays
 * the employment group's (`shaping.go`'s personFieldGroups).
 */
const personGroupOf: Record<string, Group> = { ...groupOf, title: "work" };

const reads = (audience: Audience, group: Group): boolean => {
  // HR's reading: staff:manage, or staff:personal-data alone.
  const hr = audience.hr || audience.personalData;
  switch (group) {
    case "work":
    case "dates":
      return true;
    case "employment":
      return audience.self || audience.manager || hr;
    case "pay":
    case "private":
    case "sealed":
    case "careDays":
    case "links":
      return audience.self || hr;
    case "notes":
    case "hold":
      return hr;
  }
};

/** Whether the audience is `staff:view` and nothing more: shown only the people active today. */
const colleagueOnly = (audience: Audience) =>
  audience.colleague && !audience.self && !audience.manager && !audience.hr && !audience.personalData;

/** A row with every field the audience may not read left out — never null. */
const shape = <T extends object>(
  audience: Audience,
  row: T,
  keep: readonly string[] = [],
  groups: Record<string, Group> = groupOf,
): T =>
  Object.fromEntries(
    Object.entries(row).filter(
      ([field, value]) =>
        value !== undefined && (keep.includes(field) || reads(audience, groups[field] ?? "employment")),
    ),
  ) as T;

/** The request properties `PUT /people/{id}` and `POST /people` take (`StaffPersonRequest`). */
const personRequestProperties = [
  "birthDate",
  "childrenUnder12",
  "chronicallyIllChildren",
  "city",
  "country",
  "displayName",
  "employeeNumber",
  "familyName",
  "gender",
  "givenName",
  "notes",
  "phoneMobile",
  "phonePrivate",
  "phoneWork",
  "postalCode",
  "preferredLanguage",
  "privateEmail",
  "soleCarer",
  "street",
  "workCountry",
  "workEmail",
];

/** The request properties `PUT /me` takes (`StaffMeRequest`). */
const meRequestProperties = [
  "city",
  "country",
  "nextOfKin",
  "phoneMobile",
  "phonePrivate",
  "postalCode",
  "privateEmail",
  "street",
];

/**
 * The server's strict body (`strictbody.go`): a top-level property the
 * request does not take is refused, named, and nothing is applied.
 */
const unknownProperties = (
  body: unknown,
  allowed: readonly string[],
  title = "Invalid person",
): Response | undefined => {
  const unknown = Object.keys((body ?? {}) as Record<string, unknown>)
    .filter((key) => !allowed.includes(key))
    .sort();
  if (unknown.length === 0) return undefined;
  return staffProblem(400, {
    title,
    errors: Object.fromEntries(
      unknown.map((key) => [key, ["This property is not one this request takes; the request was not applied"]]),
    ),
  });
};

/**
 * Why `value` is no value of the preference kind (`canonicalPreference`), or
 * nothing when it is one: the days 1 to 7, the parts, the extent, the share
 * and the limits of their kinds, every list one to its most, each value once.
 * Whether a location exists is judged under the lock, on the new and changed rows.
 */
const preferenceValueRefusal = (kind: string, value: unknown): string | undefined => {
  const fields = (value ?? {}) as Record<string, unknown>;
  const only = (key: string) => Object.keys(fields).every((one) => one === key);
  const list = (key: string, most: number, valid: (one: unknown) => boolean, message: string) => {
    const values = fields[key];
    if (!only(key) || !Array.isArray(values)) return message;
    if (values.length === 0 || values.length > most) return `The ${key} are 1 to ${most} values`;
    if (new Set(values).size !== values.length) return `The ${key} name a value twice`;
    return values.every(valid) ? undefined : message;
  };
  const whole = (key: string, most: number, message: string) => {
    const number = fields[key];
    return only(key) && Number.isInteger(number) && (number as number) >= 1 && (number as number) <= most
      ? undefined
      : message;
  };
  const of = (key: string, allowed: readonly string[], message: string) =>
    only(key) && allowed.includes(fields[key] as string) ? undefined : message;
  switch (kind) {
    case "weekdays":
      return list(
        "days",
        7,
        (one) => Number.isInteger(one) && (one as number) >= 1 && (one as number) <= 7,
        "The days are 1 to 7",
      );
    case "time_of_day":
      return list(
        "parts",
        timeOfDayParts.length,
        (one) => (timeOfDayParts as readonly unknown[]).includes(one),
        "The parts are of morning, day, evening, night",
      );
    case "location": {
      const ids = fields.locationIds;
      if (!only("locationIds") || !Array.isArray(ids) || !ids.every(Number.isInteger))
        return 'A location preference is {"locationIds": [...]}';
      if (ids.length === 0 || ids.length > 10) return "A location preference names 1 to 10 locations";
      return new Set(ids).size === ids.length ? undefined : "A location is named twice";
    }
    case "travel":
      return of(
        "extent",
        travelExtents,
        'A travel preference is {"extent": "none" | "regional" | "national" | "international"}',
      );
    case "remote":
      return of("share", remoteShares, 'A remote preference is {"share": "never" | "some" | "mostly" | "only"}');
    case "max_consecutive_days":
      return whole("days", 14, 'A most-consecutive-days preference is {"days": 1..14}');
    case "max_hours_per_week":
      return whole("hours", 60, 'A most-hours-a-week preference is {"hours": 1..60}');
  }
  return `${kind} is no preference kind`;
};

/**
 * An event's payload as the audience reads it (design S21): a field of a
 * group it may not read is "changed" — except the pay and HR's notes, which
 * are left out altogether.
 */
const shapeEvent = (audience: Audience, event: StaffEvent): StaffEvent => {
  const fields = (event.payload as { fields?: Record<string, unknown> }).fields;
  if (!fields) return event;
  const shaped: Record<string, unknown> = {};
  for (const [field, change] of Object.entries(fields)) {
    const group = groupOf[field] ?? "employment";
    if (reads(audience, group)) shaped[field] = change;
    else if (group !== "pay" && group !== "notes") shaped[field] = "changed";
  }
  return { ...event, payload: { ...event.payload, fields: shaped } };
};

/**
 * The Staff API as the pages of this package read and write it, from an
 * in-memory store: `/meta`, `/settings`, the four structure tables (list,
 * add, replace, archive), `/people` (list, read, create) and an employment's
 * create. `write` lets a test put a refusal in the place of any write.
 *
 * It models the rules a page has to show, as the server applies them: the
 * revision on every replace (`revision_conflict`), the one default per table
 * and its move, the default never archived (`structure_default`), the
 * employee number's 16 characters, the directory per audience (a colleague
 * lists the people active today whatever `status` says), the generated
 * employee and employment numbers, the required terms fields and the hours
 * rule; the person page's reads shaped per audience (Self and Manager by
 * the `me` link, the colleague, HR, personal data alone) with the field
 * groups of design §5 — the manager on a row, a person or up the org chain
 * whoever manages them today by the server's one rule — every write but the
 * sealed fields' refused without `staff:manage` (the uniform 403), a request
 * property `PUT /me` or a person's write does not take refused by name, the
 * next of kin, roles and link with the person's revision,
 * the sealed fields' set and reveal, the org view, the history shaped per
 * field, the terms chain's dated rules, end and reopen, a § 14-6 check from
 * the row in force, and `/me`. **What it does not model**: the
 * archived-reference rule (`structure_archived`), the cycles, the probation
 * and the derived dates, the events a write records, and the structure
 * filters across several employments — a test that needs one of those stages
 * it with `write`.
 */
export const stubStaffApi = (server: StaffServer = {}): StaffStub => {
  const role = server.role ?? "manage";
  const pageSize = server.pageSize ?? 25;
  const state: StaffState = {
    settings: server.settings ?? defaultSettings(),
    legalEntities: server.legalEntities ?? defaultLegalEntities(),
    businessUnits: server.businessUnits ?? defaultBusinessUnits(),
    departments: server.departments ?? defaultDepartments(),
    locations: server.locations ?? defaultLocations(),
    people: server.people ?? defaultPeople(),
    employments: server.employments ?? defaultEmployments(),
    terms: server.terms ?? defaultTerms(),
    events: server.events ?? defaultEvents(),
    documents: server.documents ?? defaultDocuments(),
    documentCategories: server.documentCategories ?? defaultDocumentCategories(),
    myAbsences: server.absence?.myAbsences ?? defaultMyAbsences(),
    approvals: server.absence?.approvals ?? defaultApprovals(),
    cases: server.absence?.cases ?? defaultCases(),
    personAbsence: server.absence?.people ?? {},
    absenceTypes: server.absence?.types ?? allAbsenceTypes(defaultMeta(role).absenceTypes),
    holidaySchemes: server.absence?.schemes ?? defaultHolidaySchemes(),
    accounts: server.accounts ?? {},
    passesRevealed: 0,
    groups: server.groups ?? defaultGroups(),
    groupTraits: server.groupTraits ?? defaultGroupTraits(),
    competenceCategories: server.competenceCategories ?? defaultCompetenceCategories(),
    competences: server.competences ?? defaultCompetences(),
    personCompetences: server.personCompetences ?? defaultPersonCompetences(),
    interests: server.interests ?? defaultPersonInterests(),
    preferences: server.preferences ?? defaultPersonPreferences(),
    availability: server.availability ?? defaultAvailability(),
    reviews: server.reviews ?? defaultCompetenceReviews(),
    competenceProfiles: server.competenceProfiles ?? defaultCompetenceProfiles(),
    personProfiles: server.personProfiles ?? defaultPersonProfiles(),
  };
  const users = server.users ?? defaultAssignableUsers();
  let nextId = 900;

  const metaOf = (): StaffMeta => {
    const meta = defaultMeta(role);
    // Being a manager is not a role but the terms: the caller manages
    // anyone when their linked person is somebody's manager today.
    const managesAnyone = server.me !== undefined && state.people.some((one) => managerToday(one)?.id === server.me);
    const base = {
      ...meta,
      // Every caller's: the locations not archived, as the table holds them now.
      locations: state.locations.filter((one) => !one.archivedAt),
      capabilities: {
        ...meta.capabilities,
        managesAnyone,
        canDecide: meta.capabilities.canDecide || managesAnyone,
      },
    };
    return base.structure
      ? {
          ...base,
          structure: {
            legalEntities: state.legalEntities,
            businessUnits: state.businessUnits,
            departments: state.departments,
            locations: state.locations,
          },
        }
      : base;
  };

  /** The providers the fake's `/meta` lists (Entra B): a provider's kind is refused without its provider. */
  const providersOffered = () =>
    (server.meta instanceof Response ? undefined : (server.meta ?? metaOf()).providers) ?? [];

  const staleRevision = (init: RequestInit | undefined, current: number, what: string): Response | undefined => {
    const supplied = new Headers(init?.headers).get("If-Match");
    if (supplied === null) return staffProblem(428, { title: "If-Match required" });
    if (Number(supplied.replaceAll('"', "")) === current) return undefined;
    return staffProblem(409, {
      code: "revision_conflict",
      title: "Revision conflict",
      detail: `The ${what} has revision ${current}; the supplied revision was ${supplied}.`,
    });
  };

  const tables = {
    "legal-entities": { rows: () => state.legalEntities, what: "legal entity", defaults: true },
    "business-units": { rows: () => state.businessUnits, what: "business unit", defaults: true },
    departments: { rows: () => state.departments, what: "department", defaults: false },
    locations: { rows: () => state.locations, what: "location", defaults: false },
  } as const;
  type Row = { id: number; revision: number; isDefault?: boolean; archivedAt?: string };

  const structureDefault = (what: string) =>
    staffProblem(409, {
      code: "structure_default",
      title: `The default ${what}`,
      detail: `This is the default ${what}, and there is always exactly one. Make another ${what} the default first.`,
    });

  /** The flag moves to `row` and leaves every other row of the table in one step, as the server's transaction does. */
  const makeDefault = (rows: Row[], row: Row) => {
    for (const other of rows) {
      if (other !== row && other.isDefault) {
        other.isDefault = false;
        other.revision += 1;
      }
    }
    row.isDefault = true;
  };

  const settingsRefusal = (body: StaffSettingsInput): Response | undefined => {
    const errors: Record<string, string[]> = {};
    const outside = (value: number | undefined, min: number) =>
      value !== undefined && (!Number.isInteger(value) || value < min || value > 3650);
    if (outside(body.retentionGraceDays, 0)) errors.retentionGraceDays = ["The grace period is 0 to 3650 days"];
    if (outside(body.healthRetentionDays, 30))
      errors.healthRetentionDays = ["The health documents' period is 30 to 3650 days"];
    // An erase setting is true or false; a null is refused, never read as false (Task 5b).
    for (const key of ["erasePregnancyHealthTypes", "eraseSickCaseDates"])
      if ((body as Record<string, unknown>)[key] === null) errors[key] = ["An erase setting is true or false"];
    // A provider's kind needs its provider; the switches are the switchable fields' (Entra B).
    const kind = body.defaultAccountKind;
    if (kind && kind !== "local" && kind !== "external" && !providersOffered().some((one) => one.key === kind))
      return staffProblem(400, {
        code: "provider_missing",
        title: "Invalid Staff settings",
        errors: { defaultAccountKind: ["No enabled module offers this provider"] },
      });
    for (const key of Object.keys(body.directoryFields ?? {}))
      if (!(key in defaultDirectoryFields)) errors[`directoryFields.${key}`] = ["Not a switchable field"];
    if (Object.keys(errors).length > 0) return staffProblem(400, { title: "Invalid Staff settings", errors });
    const longest = `${body.employeeNumberPrefix ?? ""}${body.nextEmployeeNumber}`;
    if (longest.length <= 16) return undefined;
    const message = `The prefix and the next number make ${longest}, which is longer than 16 characters`;
    return staffProblem(400, {
      code: "employee_number_too_long",
      title: "Invalid Staff settings",
      errors: { employeeNumberPrefix: [message], nextEmployeeNumber: [message] },
    });
  };

  const taken = (number: string) =>
    state.people.some((one) => one.employeeNumber.toUpperCase() === number.toUpperCase());

  const createPerson = (body: StaffPersonInput): Response => {
    let employeeNumber = body.employeeNumber;
    if (!employeeNumber) {
      let next = state.settings.nextEmployeeNumber;
      while (taken(`${state.settings.employeeNumberPrefix}${next}`)) next += 1;
      employeeNumber = `${state.settings.employeeNumberPrefix}${next}`;
      state.settings = { ...state.settings, nextEmployeeNumber: next + 1, revision: state.settings.revision + 1 };
    } else if (taken(employeeNumber)) {
      return staffProblem(400, {
        code: "employee_number_taken",
        title: "Invalid person",
        errors: { employeeNumber: [`Another person has the employee number ${employeeNumber}`] },
      });
    }
    const id = nextId++;
    const stored: StoredPerson = {
      id,
      givenName: body.givenName,
      familyName: body.familyName,
      displayName: body.displayName ?? `${body.givenName} ${body.familyName}`,
      employeeNumber,
      workEmail: body.workEmail,
      phoneWork: body.phoneWork,
      workCountry: body.workCountry ?? "NO",
      preferredLanguage: body.preferredLanguage ?? "nb",
      onLeaveToday: false,
      status: { state: "none" },
    };
    state.people.push(stored);
    return jsonResponse(201, personOf(stored));
  };

  const audienceOf = (stored: StoredPerson): Audience | undefined => {
    const audience: Audience = {
      self: server.me === stored.id,
      manager: server.me !== undefined && managerToday(stored)?.id === server.me,
      hr: manages(role),
      personalData: holdsPersonalData(role),
      // Reading 15: the directory reaches the people active today alone.
      colleague: capabilitiesOf(role).canViewDirectory && stored.status.state === "active",
    };
    return audience.self || audience.manager || audience.hr || audience.personalData || audience.colleague
      ? audience
      : undefined;
  };

  /**
   * Whom a stored person's manager today is, by the server's one manager rule
   * (`terms_derived.go`'s effectiveManager): the manager their terms name when
   * active today, else the manager of their department or of the first one
   * above it who is someone else and active today — a manager who has left is
   * skipped, for every audience alike; nobody for a person not active today.
   */
  const managerToday = (stored: StoredPerson): StoredPerson | undefined => {
    if (stored.status.state !== "active") return undefined;
    const present = (id: number | undefined) => {
      const found = id === undefined || id === stored.id ? undefined : findPerson(id);
      return found?.status.state === "active" ? found : undefined;
    };
    const named = present(stored.manager?.id);
    if (named) return named;
    const visited = new Set<number>();
    let department = state.departments.find((one) => one.id === stored.department?.id);
    while (department && !visited.has(department.id)) {
      visited.add(department.id);
      const head = present(department.managerPersonId);
      if (head) return head;
      const parentId = department.parentId;
      department = state.departments.find((one) => one.id === parentId);
    }
    return undefined;
  };

  /** A stored person's wire row, with the manager who manages them today. */
  const placed = (stored: StoredPerson, onlyColleague: boolean) => {
    const item = listItem(stored);
    const manager = managerToday(stored);
    if (manager) item.manager = summaryOf(manager);
    else delete item.manager;
    // `erasedAt` is the hold group's: staff:manage's on a list row.
    if (onlyColleague) delete item.erasedAt;
    return item;
  };

  /** A person as the audience reads them, with what the caller may do with them. */
  const personOf = (stored: StoredPerson, audience: Audience = audienceOf(stored) as Audience): StaffPerson => {
    const record: PersonRecord = stored.record ?? {};
    const { sealed, user, revision = 1, legalHold, ...rest } = record;
    // The next of kin and the roles are read through their own endpoints.
    const fields: Partial<PersonRecord> = { ...rest };
    delete fields.nextOfKin;
    delete fields.roles;
    const whole: StaffPerson = {
      ...placed(stored, colleagueOnly(audience)),
      ...fields,
      nationalId: sealed?.nationalId ? { kind: sealed.nationalId.kind, onFile: true } : { onFile: false },
      ...(sealed?.bankAccount
        ? { bankAccount: { kind: sealed.bankAccount.kind, last4: sealed.bankAccount.value.slice(-4) } }
        : {}),
      ...(user ? { user } : {}),
      // `{held: false}` when there is none — the hold group's readers always get it.
      legalHold: legalHold ?? { held: false },
      revision,
      capabilities: {
        canEdit: audience.hr,
        canManageAccount: audience.hr,
        canRevealIdentifiers: audience.personalData,
        canSeePay: audience.self || audience.hr || audience.personalData,
        canSeeHealth: audience.self || audience.personalData,
        canHold: audience.personalData,
        // Who decides the person's absence requests: their manager, an
        // Approver or HR, never they themselves (the stub knows no self-approver).
        canDecide: (audience.manager || audience.hr || role === "approve") && !audience.self,
        // Who verifies the person's competences: their manager, an Approver
        // or HR, never they themselves (staff competence design S4).
        canVerifyCompetence: (audience.manager || audience.hr || role === "approve") && !audience.self,
        // Who reads what the person wants, and who stamps it Seen: the
        // manager and HR, never the Approver alone (plan reading 16).
        canReadWishes: audience.self || audience.manager || audience.hr || audience.personalData,
        canStampWishesSeen: (audience.manager || audience.hr) && !audience.self,
        // Who assigns the person's competence profiles: HR alone (plan reading 32).
        canAssignProfiles: audience.hr,
        // Who the caller is to the person: themselves, or their manager today.
        self: audience.self,
        isManager: audience.manager && !audience.self,
      },
    };
    const shaped = shape(audience, whole, ["id", "revision", "capabilities", "status"], personGroupOf);
    // The reviews group (delivery B): the person's, their manager's, an
    // Approver's and HR's; absent when they were never reviewed.
    const last = latestReview(stored.id);
    return last && readsReviews(audience)
      ? { ...shaped, lastCompetenceReview: { reviewedOn: last.reviewedOn, reviewer: last.reviewer } }
      : shaped;
  };

  /** Whether the audience reads the person's competence reviews: all but a colleague as such. */
  const readsReviews = (audience: Audience) =>
    audience.self || audience.manager || audience.hr || audience.personalData || role === "approve";

  /** The person's latest review by day, the later recorded on a tie. */
  const latestReview = (personId: number): StoredReview | undefined =>
    state.reviews
      .filter((one) => one.personId === personId)
      .sort((a, b) => b.reviewedOn.localeCompare(a.reviewedOn) || b.id - a.id)[0];

  const findPerson = (id: number) => state.people.find((one) => one.id === id);

  /** Who the caller is by name: their linked person's, HR's otherwise. */
  const callerDisplayName = () =>
    (server.me === undefined ? undefined : findPerson(server.me)?.displayName) ?? "Harriet HR";

  /** A stored wish or review as the wire carries it, without whose it is. */
  const unowned = <T extends { personId: number }>(row: T): Omit<T, "personId"> => {
    const copy: Partial<T> = { ...row };
    delete copy.personId;
    return copy as Omit<T, "personId">;
  };

  /**
   * An account with what the server derives (design S22): the address the
   * kind names and whether the person has it, and the day the account is
   * enabled — provisionFrom, else the first day of the earliest employment
   * that has not ended by today.
   */
  const derivedAccount = (
    stored: StoredPerson,
    account: Omit<Schemas["StaffPersonAccount"], "address" | "provisionFromEffective">,
  ): Schemas["StaffPersonAccount"] => {
    const kind = account.accountEmailKind;
    const present = Boolean(kind === "work" ? stored.workEmail : stored.record?.privateEmail);
    const earliest = state.employments
      .filter((one) => one.personId === stored.id && (!one.endDate || one.endDate >= TODAY))
      .map((one) => one.startDate)
      .sort()[0];
    const effective = account.provisionFrom ?? earliest;
    const derived: Schemas["StaffPersonAccount"] = { ...account, address: { kind, present } };
    delete derived.provisionFromEffective;
    if (effective) derived.provisionFromEffective = effective;
    return derived;
  };

  /** A person's account as stored, or the flag off with HR's self-service link, if any. */
  const accountOf = (stored: StoredPerson): Schemas["StaffPersonAccount"] => {
    const kept = state.accounts[stored.id];
    if (kept) return kept;
    const user = stored.record?.user;
    return derivedAccount(stored, {
      provisionAccount: false,
      accountEmailKind: "work",
      accountKind: state.settings.defaultAccountKind,
      revision: 1,
      ...(user ? { link: { state: "linked", managed: false, attempts: 0, user } } : {}),
    });
  };

  /** The person and who the caller is to them, or nothing — the unknown id's 404 — for an outsider. */
  const seen = (id: number) => {
    const stored = findPerson(id);
    const audience = stored ? audienceOf(stored) : undefined;
    return stored && audience ? { stored, audience } : undefined;
  };

  const recordOf = (stored: StoredPerson): PersonRecord => {
    stored.record = stored.record ?? {};
    return stored.record;
  };

  const bumpRevision = (stored: StoredPerson) => {
    const record = recordOf(stored);
    record.revision = (record.revision ?? 1) + 1;
  };

  /** A terms chain, oldest first, each row's `validTo` the day before the next row's. */
  const chainOf = (employmentId: number): StaffTerms[] => {
    const rows = state.terms
      .filter((row) => row.employmentId === employmentId)
      .sort((a, b) => a.validFrom.localeCompare(b.validFrom));
    return rows.map((row, index) => {
      const next = rows[index + 1];
      const copy: StaffTerms = { ...row };
      delete copy.validTo;
      if (next) copy.validTo = dayBefore(next.validFrom);
      return copy;
    });
  };

  const inForce = (employment: StaffEmployment): StaffTerms | undefined => {
    const chain = chainOf(employment.id);
    const day = employment.endDate && employment.endDate < TODAY ? employment.endDate : TODAY;
    return [...chain].reverse().find((row) => row.validFrom <= day) ?? chain[0];
  };

  /** Design §4's § 14-6 table, judged on the row in force today. */
  const contractCheckOf = (employment: StaffEmployment) => {
    const row = inForce(employment);
    const has = (value: unknown) => value !== undefined && value !== null && value !== "";
    const hiredOut = row?.appointmentForm === "fast_utleid" || row?.appointmentForm === "midlertidig_utleid";
    const judge = (applies: boolean, present: boolean): StaffContractCheckItem["state"] =>
      !applies ? "not_applicable" : present ? "present" : "missing";
    const items: [string, string, StaffContractCheckItem["state"]][] = [
      ["a", "employment.businessUnitId", "present"],
      ["b", "terms.locationId", judge(true, has(row?.locationId))],
      ["c", "terms.title", judge(true, has(row?.title) || has(row?.occupationCode))],
      ["d", "employment.startDate", "present"],
      [
        "e",
        "terms.expectedEndDate",
        judge(row?.employmentType === "midlertidig", has(row?.expectedEndDate) && has(row?.temporaryGround)),
      ],
      ["f", "terms.probationEndDate", judge(has(row?.probationEndDate), true)],
      ["g", "terms.holidayTermsText", judge(true, has(row?.holidayTermsText))],
      ["h", "terms.noticeEmployee", judge(true, has(row?.noticeEmployee) && has(row?.noticeUnit))],
      [
        "i",
        "terms.payComponents",
        judge(true, (row?.payComponents?.length ?? 0) > 0 && has(row?.payMethod) && has(row?.paydayText)),
      ],
      ["j", "terms.workingTimeText", judge(true, has(row?.workingTimeText))],
      ["k", "terms.breakMinutes", judge(true, has(row?.breakMinutes))],
      [
        "l",
        "terms.specialHoursText",
        judge(row?.workingHoursScheme !== "not_shift" || has(row?.specialHoursText), has(row?.specialHoursText)),
      ],
      ["m", "terms.overtimeRuleText", judge(true, has(row?.overtimeRuleText))],
      ["n", "terms.tariffReference", judge(has(row?.tariffReference) || hiredOut, has(row?.tariffReference))],
      ["o", "terms.hiredOutTo", judge(hiredOut, has(row?.hiredOutTo))],
      ["p", "terms.competenceDevelopmentText", judge(true, has(row?.competenceDevelopmentText))],
      [
        "q",
        "terms.socialSecurityText",
        judge(true, has(row?.socialSecurityText) || has(state.settings.pensionProvider)),
      ],
    ];
    return {
      employmentId: employment.id,
      ...(row ? { termsId: row.id } : {}),
      contractSigned: employment.contractSignedAt !== undefined,
      ...(employment.contractSignedAt ? { contractSignedAt: employment.contractSignedAt } : {}),
      items: items.map(([item, source, itemState]) => ({ item, source, state: itemState })),
    };
  };

  const summaryOf = (stored: StoredPerson) => ({
    id: stored.id,
    displayName: stored.displayName,
    employeeNumber: stored.employeeNumber,
  });

  /** The org view: the chain and the reports by the one manager rule (`org.go`). */
  const orgOf = (stored: StoredPerson) => {
    const chain = [];
    const visited = new Set([stored.id]);
    let manager = managerToday(stored);
    while (manager && !visited.has(manager.id)) {
      visited.add(manager.id);
      chain.push(summaryOf(manager));
      manager = managerToday(manager);
    }
    const reports = state.people.filter((one) => managerToday(one)?.id === stored.id).map(summaryOf);
    return { person: summaryOf(stored), chain, reports };
  };

  /** The private group's replace, as `PUT /people/{id}` and `PUT /me` both do it. */
  const privateFields = ["phoneMobile", "phonePrivate", "privateEmail", "street", "postalCode", "city", "country"];

  const meProblem = () =>
    staffProblem(404, {
      code: "no_person",
      title: "No person",
      detail: "You are linked to no person in Staff. Ask HR to link your user to your record.",
    });

  const termsConflict = (code: string, title: string, detail: string) => staffProblem(409, { code, title, detail });

  /** The required fields after the defaults, as the server judges every row. */
  const termsRefusal = (row: Partial<StaffTerms>, prefix = ""): Response | undefined => {
    const errors: Record<string, string[]> = {};
    if (!row.employmentType) errors[`${prefix}employmentType`] = ["Every terms row states the employment type"];
    if (row.ftePercent === undefined || row.ftePercent === null)
      errors[`${prefix}ftePercent`] = ["Every terms row states the percentage of a full position"];
    if (Object.keys(errors).length > 0)
      return staffProblem(400, { code: "terms_required_fields", title: "Invalid terms", errors });
    const scheme = row.workingHoursScheme ?? "not_shift";
    if (fullTimeHoursMismatch(scheme, row.fullTimeHoursPerWeek ?? defaultFullTimeHours[scheme]))
      return staffProblem(400, {
        code: "full_time_hours_mismatch",
        title: "Invalid terms",
        errors: { [`${prefix}fullTimeHoursPerWeek`]: [`The hours do not fit ${scheme}`] },
      });
    return undefined;
  };

  /** A request's fields onto a row: left out keeps `base`'s value, null clears it. */
  const merged = (base: Partial<StaffTerms>, body: StaffTermsInput): Partial<StaffTerms> => {
    const row: Record<string, unknown> = { ...base };
    for (const [field, value] of Object.entries(body)) {
      if (field === "correction") continue;
      if (value === null) delete row[field];
      else row[field] = value;
    }
    return row as Partial<StaffTerms>;
  };

  const createEmployment = (personId: number, body: StaffEmploymentInput): Response => {
    const stored = state.people.find((one) => one.id === personId);
    if (!stored) return notFound();
    const terms = body.terms;
    const errors: Record<string, string[]> = {};
    let code: string | undefined;
    const coded = (field: string, rule: string, message: string) => {
      errors[field] = [...(errors[field] ?? []), message];
      code = code ?? rule;
    };
    if (!terms.employmentType)
      coded(
        "terms.employmentType",
        "terms_required_fields",
        "Every terms row states the employment type (fast or midlertidig); the law presumes the employee's claim when it is missing",
      );
    if (terms.ftePercent === undefined || terms.ftePercent === null)
      coded(
        "terms.ftePercent",
        "terms_required_fields",
        "Every terms row states the percentage of a full position; the law presumes the employee's claim when it is missing",
      );
    const scheme = terms.workingHoursScheme ?? "not_shift";
    const hours = terms.fullTimeHoursPerWeek ?? defaultFullTimeHours[scheme];
    if (fullTimeHoursMismatch(scheme, hours))
      coded("terms.fullTimeHoursPerWeek", "full_time_hours_mismatch", `The hours do not fit ${scheme}`);
    if (Object.keys(errors).length > 0) return staffProblem(400, { code, title: "Invalid employment", errors });

    // The first free `<employee number>-n`, as the server generates it.
    let n = 1;
    const taken = (number: string) =>
      state.employments.some((one) => one.employmentNumber?.toLowerCase() === number.toLowerCase());
    while (taken(`${stored.employeeNumber}-${n}`)) n += 1;
    const employmentNumber = body.employmentNumber ?? `${stored.employeeNumber}-${n}`;
    // On an erased person this is a rehire: the erasure and the reassessment are cleared.
    delete stored.erasedAt;
    if (stored.record) delete stored.record.reassessedAt;
    const employment: StaffEmployment = {
      id: nextId++,
      personId,
      employmentNumber,
      startDate: body.startDate,
      businessUnitId: body.businessUnitId ?? state.businessUnits.find((one) => one.isDefault)?.id,
      relationshipType: body.relationshipType ?? "ordinary",
      revision: 1,
    };
    state.employments.push(employment);
    state.terms.push({
      ...(merged({}, terms) as StaffTerms),
      id: nextId++,
      employmentId: employment.id,
      validFrom: body.startDate,
      revision: 1,
    });
    stored.status = body.startDate > TODAY ? { state: "upcoming", date: body.startDate } : { state: "active" };
    stored.unitId = employment.businessUnitId;
    stored.locationId = terms.locationId ?? undefined;
    const department = state.departments.find((one) => one.id === terms.departmentId);
    stored.department = department ? { id: department.id, code: department.code, name: department.name } : undefined;
    stored.title = terms.title ?? undefined;
    const unit = state.businessUnits.find((one) => one.id === employment.businessUnitId);
    stored.businessUnit = unit ? { id: unit.id, name: unit.name } : undefined;
    const location = state.locations.find((one) => one.id === terms.locationId);
    stored.location = location ? { id: location.id, name: location.name } : undefined;
    return jsonResponse(201, employment);
  };

  const listPeople = (url: URL): Response => {
    const params = url.searchParams;
    const status = params.get("status") ?? "current";
    if (!["current", "ended", "all"].includes(status)) return staffProblem(400, { title: "Invalid list filter" });
    const words = (params.get("q") ?? "").toLowerCase().split(/\s+/).filter(Boolean);
    const id = (name: string) => (params.has(name) ? Number(params.get(name)) : undefined);
    const unitId = id("unitId");
    const departmentId = id("departmentId");
    const locationId = id("locationId");
    const standing = (one: StoredPerson) => {
      // A colleague lists the people active today, whatever status says.
      if (role === "view" || role === "approve") return one.status.state === "active";
      if (status === "all") return true;
      if (status === "ended") return one.status.state === "ended";
      return one.status.state !== "ended";
    };
    const rows = state.people
      .filter(standing)
      .filter((one) =>
        words.every((word) =>
          [one.givenName, one.familyName, one.displayName, one.employeeNumber, one.workEmail ?? ""].some((field) =>
            field.toLowerCase().includes(word),
          ),
        ),
      )
      .filter((one) => unitId === undefined || one.unitId === unitId)
      .filter((one) => departmentId === undefined || one.department?.id === departmentId)
      .filter((one) => locationId === undefined || one.locationId === locationId)
      .sort((a, b) => a.displayName.localeCompare(b.displayName))
      // The directory's audience is the role alone: staff:view without
      // staff:manage is the colleague.
      .map((one) => placed(one, !manages(role)));
    return jsonResponse(200, page(rows, Math.max(1, Number(params.get("page")) || 1), pageSize));
  };

  /** The person page's and My page's endpoints; undefined falls through to a 404. */
  const personRoutes = (
    method: string,
    path: string,
    url: URL,
    init: RequestInit | undefined,
    body: Record<string, unknown> | undefined,
  ): Response | undefined => {
    const hr = manages(role);

    if (path === "/api/v1/staff/assignable-users" && method === "GET") {
      if (!hr) return forbidden();
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      return jsonResponse(
        200,
        users
          .filter((user) => user.active && user.displayName.toLowerCase().includes(q))
          .map(({ id, displayName }) => ({ id, displayName })),
      );
    }

    if (path === "/api/v1/staff/me" || path === "/api/v1/staff/me/events") {
      const strict = method === "PUT" ? unknownProperties(body, meRequestProperties) : undefined;
      if (strict) return strict;
      const stored = server.me === undefined ? undefined : findPerson(server.me);
      if (!stored) return meProblem();
      // Whatever else the caller holds, My page reads them as themselves.
      const self: Audience = { self: true, manager: false, hr: false, personalData: false, colleague: false };
      if (path.endsWith("/events")) {
        const all = (state.events[stored.id] ?? []).map((event) => shapeEvent(self, event));
        return jsonResponse(200, page(all, Math.max(1, Number(url.searchParams.get("page")) || 1), pageSize));
      }
      if (method === "GET") return jsonResponse(200, personOf(stored, self));
      if (method === "PUT") {
        const record = recordOf(stored);
        const stale = staleRevision(init, record.revision ?? 1, "person");
        if (stale) return stale;
        for (const field of privateFields) delete (record as Record<string, unknown>)[field];
        const { nextOfKin, ...rest } = body as Record<string, unknown>;
        Object.assign(record, rest);
        record.nextOfKin = nextOfKin as PersonRecord["nextOfKin"];
        bumpRevision(stored);
        return jsonResponse(200, personOf(stored, self));
      }
    }

    const person = /^\/api\/v1\/staff\/people\/(\d+)(?:\/(.+))?$/.exec(path);
    if (person) {
      const found = seen(Number(person[1]));
      if (!found) return notFound();
      const { stored, audience } = found;
      const record = recordOf(stored);
      const sub = person[2];
      const privateReader = reads(audience, "private");
      // Every write on a person but the sealed fields' and the hold's is
      // staff:manage's (the access layer's uniform 403); those check their own.
      if (method !== "GET" && !/^(national-id|bank-account|legal-hold|reassessed)/.test(sub ?? "") && !audience.hr)
        return forbidden();
      // An erased person is read-only (design S15) but for the hold, the
      // reassessment, an unlink and the reads — a reveal among them, which
      // finds nothing. The rehire is `POST …/employments`, routed before.
      const unlink = sub === "user" && method === "PUT" && (body as { userId?: unknown })?.userId === null;
      // Disable now is allowed on an erased person, whose access must end too.
      if (
        stored.erasedAt &&
        method !== "GET" &&
        sub !== "legal-hold" &&
        sub !== "reassessed" &&
        sub !== "account/disable" &&
        !unlink &&
        !/\/reveal$/.test(sub ?? "")
      )
        return erasedRefusal();

      if (sub === "legal-hold" && method === "PUT") {
        if (!audience.personalData) return forbidden();
        const input = body as { held?: boolean; reason?: string | null };
        if (typeof input.held !== "boolean")
          return staffProblem(400, { title: "Invalid legal hold", errors: { held: ["held is true or false"] } });
        const reason = input.reason?.trim() ?? "";
        if (input.held && (reason === "" || reason.length > 500))
          return staffProblem(400, {
            title: "Invalid legal hold",
            errors: { reason: ["A legal hold states its reason, 1 to 500 characters"] },
          });
        if (!input.held && input.reason !== undefined && input.reason !== null)
          return staffProblem(400, {
            title: "Invalid legal hold",
            errors: { reason: ["A hold is lifted without a reason"] },
          });
        record.legalHold = input.held ? { held: true, reason, at: "2026-10-11T08:00:00Z" } : { held: false };
        return jsonResponse(200, personOf(stored, audience));
      }
      if (sub === "reassessed" && method === "POST") {
        if (!audience.personalData) return forbidden();
        const own = state.employments.filter((one) => one.personId === stored.id);
        const lastEnd = own.every((one) => one.endDate)
          ? own.map((one) => one.endDate as string).sort()[own.length - 1]
          : undefined;
        const due = lastEnd ? `${Number(lastEnd.slice(0, 4)) + 1}${lastEnd.slice(4)}` : undefined;
        if (!stored.erasedAt || record.reassessedAt || !due || due > TODAY)
          return staffProblem(409, {
            code: "reassessment_not_due",
            title: "Reassessment not due",
            detail:
              "A reassessment is due for an erased person not yet reassessed, twelve months after their last employment ended.",
          });
        record.reassessedAt = "2026-10-11T08:00:00Z";
        return jsonResponse(200, personOf(stored, audience));
      }
      if (sub === "personal-data" && method === "GET") {
        if (!audience.personalData) return forbidden();
        return new Response(
          // The file's absence arrays (delivery B, Task 6) are empty here:
          // the fake keeps no absence.
          JSON.stringify({
            exportedAt: "2026-10-11T08:00:00Z",
            person: personOf(stored, audience),
            account: { provisionAccount: false, accountEmailKind: "work" },
            absences: [],
            holidayAdjustments: [],
            cases: [],
            events: [],
          }),
          {
            status: 200,
            headers: {
              "Content-Type": "application/json",
              "Content-Disposition": `attachment; filename="staff-${stored.employeeNumber}-personal-data.json"`,
              "Cache-Control": "private, no-store",
            },
          },
        );
      }

      if (sub === undefined) {
        if (method === "GET") return jsonResponse(200, personOf(stored, audience));
        if (method === "PUT") {
          const strict = unknownProperties(body, personRequestProperties);
          if (strict) return strict;
          const stale = staleRevision(init, record.revision ?? 1, "person");
          if (stale) return stale;
          const { givenName, familyName, displayName, workEmail, phoneWork, workCountry, preferredLanguage, ...rest } =
            body as StaffPersonInput;
          Object.assign(stored, {
            givenName,
            familyName,
            displayName: displayName ?? stored.displayName,
            workEmail,
            phoneWork,
            workCountry: workCountry ?? "NO",
            preferredLanguage: preferredLanguage ?? "nb",
          });
          for (const field of [
            ...privateFields,
            "birthDate",
            "gender",
            "notes",
            "childrenUnder12",
            "soleCarer",
            "chronicallyIllChildren",
          ])
            delete (record as Record<string, unknown>)[field];
          Object.assign(record, rest);
          bumpRevision(stored);
          return jsonResponse(200, personOf(stored, audience));
        }
      }
      if (sub === "next-of-kin" && privateReader) {
        if (method === "GET")
          return jsonResponse(200, { nextOfKin: record.nextOfKin ?? [], revision: record.revision ?? 1 });
        if (method === "PUT") {
          const stale = staleRevision(init, record.revision ?? 1, "person");
          if (stale) return stale;
          record.nextOfKin = (body as { nextOfKin: PersonRecord["nextOfKin"] }).nextOfKin;
          bumpRevision(stored);
          return jsonResponse(200, { nextOfKin: record.nextOfKin, revision: record.revision });
        }
      }
      if (sub === "roles") {
        if (method === "GET") return jsonResponse(200, { roles: record.roles ?? [], revision: record.revision ?? 1 });
        if (method === "PUT") {
          const stale = staleRevision(init, record.revision ?? 1, "person");
          if (stale) return stale;
          record.roles = (body as { roles: PersonRecord["roles"] }).roles;
          bumpRevision(stored);
          return jsonResponse(200, { roles: record.roles, revision: record.revision });
        }
      }
      if (sub === "user" && reads(audience, "sealed")) {
        // The worker's link (delivery C) is the same row: read here as it
        // stands, and never linked or unlinked by hand (link_managed).
        const workers = state.accounts[stored.id]?.link;
        const managedLink = workers && workers.state !== "linked" ? workers : undefined;
        // A link naming no user yet (preparing) reads as no link (links.go linkResponse).
        const linkOf = () =>
          managedLink
            ? managedLink.user
              ? { linked: true, state: managedLink.state, managed: managedLink.managed, user: managedLink.user }
              : { linked: false }
            : record.user
              ? { linked: true, state: "linked", managed: false, user: record.user }
              : { linked: false };
        if (method === "GET") return jsonResponse(200, linkOf());
        if (method === "PUT") {
          const userId = (body as { userId: string | null }).userId;
          const managedElsewhere = Object.values(state.accounts).some(
            (one) => one.link?.managed && one.link.user?.id === userId,
          );
          if (managedLink || managedElsewhere) return linkManaged();
          if (userId === null) {
            delete record.user;
            return jsonResponse(200, linkOf());
          }
          const user = users.find((one) => one.id === userId);
          if (!user)
            return staffProblem(400, { title: "Invalid user link", errors: { userId: ["No user has this id"] } });
          if (!user.active)
            return staffProblem(400, {
              code: "user_inactive",
              title: "Invalid user link",
              errors: { userId: ["This user is disabled; only an active user can be linked"] },
            });
          if (state.people.some((one) => one.id !== stored.id && one.record?.user?.id === userId))
            return staffProblem(400, {
              code: "user_linked_elsewhere",
              title: "Invalid user link",
              errors: { userId: ["This user is linked to another person; unlink them there first"] },
            });
          record.user = { id: user.id, displayName: user.displayName, active: true };
          return jsonResponse(200, linkOf());
        }
      }
      if (sub === "account" || sub === "account/run" || sub === "account/disable" || sub === "account/pass") {
        // The links group's (plan reading 11): the person, HR and personal-data; anyone else the 404.
        if (!audience.self && !audience.hr && !audience.personalData) return notFound();
        // Every write is staff:access+staff:manage's: the access layer's 403 for the person and personal-data alone.
        if (method !== "GET" && !audience.hr) return forbidden();
        const current = accountOf(stored);
        if (sub === "account" && method === "GET") return jsonResponse(200, current);
        if (sub === "account" && method === "PUT") {
          const strict = unknownProperties(body, [
            "provisionAccount",
            "provisionFrom",
            "accountEmailKind",
            "accountKind",
          ]);
          if (strict) return strict;
          const stale = staleRevision(init, current.revision, "account flag");
          if (stale) return stale;
          const input = body as Schemas["StaffPersonAccountRequest"];
          if (typeof input.provisionAccount !== "boolean")
            return staffProblem(400, {
              title: "Invalid account",
              errors: { provisionAccount: ["provisionAccount is true or false"] },
            });
          // The kind left out is kept (Entra B); a provider's kind needs its
          // provider, and leaving entra waits while its link holds an account.
          const kind = input.accountKind ?? current.accountKind;
          if (kind !== current.accountKind) {
            if (kind !== "local" && kind !== "external" && !providersOffered().some((one) => one.key === kind))
              return staffProblem(400, {
                code: "provider_missing",
                title: "Invalid account",
                errors: { accountKind: ["No enabled module offers this provider"] },
              });
            const held = current.providerLink?.state === "enabled" || current.providerLink?.state === "disabled";
            if (current.accountKind === "entra" && held)
              return staffProblem(409, {
                code: "account_kind_locked",
                title: "Account kind locked",
                detail: "The person's entra account is enabled or disabled.",
              });
          }
          // A full replace: a day left out is cleared.
          const kept: Partial<Schemas["StaffPersonAccount"]> = { ...current };
          delete kept.provisionFrom;
          state.accounts[stored.id] = derivedAccount(stored, {
            ...(kept as Schemas["StaffPersonAccount"]),
            provisionAccount: input.provisionAccount,
            ...(input.provisionFrom ? { provisionFrom: input.provisionFrom } : {}),
            accountEmailKind: input.accountEmailKind ?? "work",
            accountKind: kind,
            revision: current.revision + 1,
          });
          return jsonResponse(200, state.accounts[stored.id]);
        }
        if (sub === "account/run" && method === "POST") {
          // A fresh invitation for a pending user whose last one cannot be
          // accepted; identity never re-sends an open one.
          const link = current.link;
          const due = current.provisionAccount && link?.state === "pending" && link.invitation?.state !== "active";
          if (!due || !link) return jsonResponse(200, { invitationSent: false, account: current });
          const fresh = link.invitation?.state === "revoked_by_owner" || link.invitation?.state === "exhausted";
          const { reason, ...rest } = link;
          const after: Schemas["StaffPersonAccount"] = {
            ...current,
            link: {
              ...rest,
              ...(reason === "invitation_revoked" || reason === "invitation_unanswered" ? {} : { reason }),
              attempts: 0,
              invitation: { state: "active", issued: fresh ? 1 : (link.invitation?.issued ?? 0) + 1 },
            },
          };
          state.accounts[stored.id] = after;
          return jsonResponse(200, { invitationSent: true, account: after });
        }
        if (sub === "account/pass" && method === "POST") {
          // The pass once (Entra B): a second call is pass_gone; never cached.
          const link = current.providerLink;
          if (!link) return notFound();
          if (!link.hasPass)
            return staffProblem(409, { code: "pass_gone", title: "Pass gone", detail: "The pass was shown already." });
          const shown: Schemas["StaffProviderLink"] = { ...link, hasPass: false };
          delete shown.passIssuedAt;
          state.accounts[stored.id] = { ...current, providerLink: shown };
          state.passesRevealed += 1;
          const answer = jsonResponse(
            200,
            server.pass ?? { pass: "Fjord-7Q2k-9xPa", expiresAt: "2026-10-12T14:00:00Z" },
          );
          answer.headers.set("Cache-Control", "no-store");
          return answer;
        }
        if (sub === "account/disable" && method === "POST") {
          // The flag goes off first, committed whatever identity answers then.
          const off: Schemas["StaffPersonAccount"] = {
            ...current,
            provisionAccount: false,
            revision: current.revision + (current.provisionAccount ? 1 : 0),
            // The provider's account is disabled with the flag, whatever the Vantigo user does.
            ...(current.providerLink?.state === "enabled"
              ? { providerLink: { ...current.providerLink, state: "disabled", attempts: 0 } }
              : {}),
          };
          state.accounts[stored.id] = off;
          const link = off.link;
          if (!link?.user) return jsonResponse(200, off);
          if (!link.managed) return disableRefusal("account_not_managed");
          if (link.reason === "account_privileged") return disableRefusal("account_privileged");
          if (link.reason === "mismatch") return disableRefusal("account_mismatch");
          const disabled: Schemas["StaffAccountLink"] = { ...link, state: "disabled", attempts: 0 };
          delete disabled.reason;
          state.accounts[stored.id] = { ...off, link: disabled };
          return jsonResponse(200, state.accounts[stored.id]);
        }
      }
      if (sub === "org" && method === "GET") return jsonResponse(200, orgOf(stored));
      if (sub === "events" && method === "GET") {
        // §5's Events row: the person, a manager of theirs and HR — a colleague gets the unknown id's 404.
        if (!audience.self && !audience.manager && !audience.hr && !audience.personalData) return notFound();
        // The account events are the links group's: listed to none else.
        const all = (state.events[stored.id] ?? [])
          .filter((event) => !event.kind.startsWith("account_") || reads(audience, "links"))
          .map((event) => shapeEvent(audience, event));
        return jsonResponse(200, page(all, Math.max(1, Number(url.searchParams.get("page")) || 1), pageSize));
      }
      if (sub === "employments" && method === "GET")
        return jsonResponse(
          200,
          state.employments
            .filter((one) => one.personId === stored.id)
            .map((one) => shape(audience, one, ["id", "personId", "revision"])),
        );

      const sealedKind = /^(national-id|bank-account)(\/reveal)?$/.exec(sub ?? "");
      if (sealedKind) {
        if (!audience.personalData) return forbidden();
        record.sealed = record.sealed ?? {};
        const sealed = record.sealed;
        const nationalId = sealedKind[1] === "national-id";
        if (sealedKind[2] && method === "POST") {
          const value = nationalId ? sealed.nationalId : sealed.bankAccount;
          if (!value) return notFound();
          return new Response(JSON.stringify(value), {
            status: 200,
            headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
          });
        }
        if (!sealedKind[2] && method === "PUT") {
          const input = body as { kind: string; value: string; country?: string };
          const digits = input.value.replace(/[\s.-]/g, "");
          if (nationalId) {
            if (input.kind !== "foreign" && !/^\d{11}$/.test(digits))
              return staffProblem(400, {
                code: "national_id_invalid",
                title: "Invalid national identity number",
                errors: { value: ["Not eleven digits"] },
              });
            // The six first digits are the birth date, ddmmyy.
            const encoded = `${digits.slice(4, 6)}-${digits.slice(2, 4)}-${digits.slice(0, 2)}`;
            if (input.kind !== "foreign" && record.birthDate && record.birthDate.slice(2) !== encoded)
              return staffProblem(400, {
                code: "national_id_birth_date_mismatch",
                title: "Invalid national identity number",
                errors: {
                  value: ["The number encodes another birth date than the person's; correct whichever is wrong"],
                },
              });
            sealed.nationalId = {
              kind: input.kind as "fodselsnummer",
              value: digits,
              ...(input.country ? { country: input.country } : {}),
            };
          } else {
            sealed.bankAccount = { kind: input.kind as "bban", value: digits };
          }
          return jsonResponse(200, personOf(stored, audience));
        }
      }
      return undefined;
    }

    const employment = /^\/api\/v1\/staff\/employments\/(\d+)(?:\/(terms|contract-check|end|reopen))?$/.exec(path);
    if (employment) {
      const row = state.employments.find((one) => one.id === Number(employment[1]));
      const found = row ? seen(row.personId) : undefined;
      if (!row || !found) return notFound();
      const { stored: owner, audience } = found;
      const sub = employment[2];
      if (method === "GET" && sub === undefined) return jsonResponse(200, shape(audience, row, ["id", "personId"]));
      if (method === "GET" && sub === "terms")
        return jsonResponse(
          200,
          chainOf(row.id).map((one) => shape(audience, one, ["id", "employmentId", "revision"])),
        );
      if (method === "GET" && sub === "contract-check") {
        if (!reads(audience, "employment")) return notFound();
        // The server leaves item i (pay) out for a reader who cannot read pay, so the
        // stub does the same: a manager gets sixteen items, not seventeen.
        const check = contractCheckOf(row);
        const items = reads(audience, "pay") ? check.items : check.items.filter((one) => one.item !== "i");
        return jsonResponse(200, { ...check, items });
      }
      if (!audience.hr) return forbidden();
      // An erased person's employments and terms are read-only (design S15).
      if (owner.erasedAt) return erasedRefusal();
      if (method === "PUT" && sub === undefined) {
        const stale = staleRevision(init, row.revision, "employment");
        if (stale) return stale;
        const input = body as Partial<StaffEmployment>;
        const first = chainOf(row.id)[0];
        const second = chainOf(row.id)[1];
        if (input.startDate && second && input.startDate >= second.validFrom)
          return termsConflict(
            "terms_before_start",
            "Terms before the start",
            "A later terms row would lie before the new start.",
          );
        row.startDate = input.startDate ?? row.startDate;
        row.businessUnitId = input.businessUnitId ?? row.businessUnitId;
        row.relationshipType = input.relationshipType ?? row.relationshipType;
        if (input.contractSignedAt) row.contractSignedAt = input.contractSignedAt;
        else delete row.contractSignedAt;
        // The holiday scheme is replaced too: left out, the employment follows the default.
        if (input.holidaySchemeId) row.holidaySchemeId = input.holidaySchemeId;
        else delete row.holidaySchemeId;
        const stored = state.terms.find((one) => one.id === first?.id);
        if (stored) stored.validFrom = row.startDate;
        row.revision += 1;
        return jsonResponse(200, row);
      }
      if (method === "POST" && sub === "end") {
        const input = body as { endDate?: string; endReason?: string };
        if (!input.endReason)
          return staffProblem(400, {
            code: "end_reason_required",
            title: "Invalid end of employment",
            errors: { endReason: ["An ended employment states why"] },
          });
        if (input.endDate && chainOf(row.id).some((one) => one.validFrom > (input.endDate as string)))
          return termsConflict("terms_after_end", "Terms after the end", "A terms row takes effect after this end.");
        row.endDate = input.endDate;
        row.endReason = input.endReason as StaffEmployment["endReason"];
        row.revision += 1;
        return jsonResponse(200, row);
      }
      if (method === "POST" && sub === "reopen") {
        delete row.endDate;
        delete row.endReason;
        row.revision += 1;
        return jsonResponse(200, row);
      }
      if (method === "POST" && sub === "terms") {
        const input = body as StaffTermsInput;
        const validFrom = input.validFrom;
        if (!validFrom)
          return staffProblem(400, {
            title: "Invalid terms",
            errors: { validFrom: ["Give the day the row takes effect"] },
          });
        const chain = chainOf(row.id);
        if (chain.some((one) => one.validFrom === validFrom))
          return termsConflict(
            "terms_valid_from_taken",
            "Day taken",
            `Another terms row of this employment takes effect on ${validFrom}.`,
          );
        if (validFrom < row.startDate)
          return termsConflict("terms_before_start", "Terms before the start", "The row is dated before the start.");
        if (row.endDate && validFrom > row.endDate)
          return termsConflict("terms_after_end", "Terms after the end", "The row is dated after the end.");
        const before = [...chain].reverse().find((one) => one.validFrom < validFrom) ?? chain[0];
        const base: Partial<StaffTerms> = { ...before };
        delete base.validTo;
        const added = merged(base, input);
        const refused = termsRefusal(added);
        if (refused) return refused;
        const saved = { ...added, id: nextId++, employmentId: row.id, validFrom, revision: 1 } as StaffTerms;
        state.terms.push(saved);
        return jsonResponse(201, saved);
      }
      return undefined;
    }

    const termsRow = /^\/api\/v1\/staff\/terms\/(\d+)$/.exec(path);
    if (termsRow) {
      const stored = state.terms.find((one) => one.id === Number(termsRow[1]));
      const row = stored ? state.employments.find((one) => one.id === stored.employmentId) : undefined;
      const found = row ? seen(row.personId) : undefined;
      if (!stored || !row || !found) return notFound();
      if (!found.audience.hr) return forbidden();
      if (found.stored.erasedAt) return erasedRefusal();
      const chain = chainOf(row.id);
      const latest = chain[chain.length - 1].id === stored.id;
      const first = chain[0].id === stored.id;
      if (method === "DELETE") {
        if (first)
          return termsConflict("terms_first_row_start", "The first row stays", "The first terms row is never deleted.");
        if (!latest)
          return termsConflict("terms_not_latest", "Not the latest row", "Only the latest terms row is deleted.");
        state.terms.splice(state.terms.indexOf(stored), 1);
        return new Response(null, { status: 204 });
      }
      if (method === "PUT") {
        const stale = staleRevision(init, stored.revision, "terms row");
        if (stale) return stale;
        const input = body as StaffTermsInput;
        if (!latest && input.correction !== true)
          return termsConflict(
            "terms_not_latest",
            "Not the latest row",
            "Only the latest row is replaced; a past row is corrected.",
          );
        const validFrom = input.validFrom ?? stored.validFrom;
        if (first && validFrom !== row.startDate)
          return termsConflict(
            "terms_first_row_start",
            "The first row is dated the start",
            "The first terms row takes effect on the employment's start.",
          );
        if (chain.some((one) => one.id !== stored.id && one.validFrom === validFrom))
          return termsConflict(
            "terms_valid_from_taken",
            "Day taken",
            `Another terms row takes effect on ${validFrom}.`,
          );
        // A replace: every field left out is cleared.
        const replaced = merged({}, input);
        const refused = termsRefusal(replaced);
        if (refused) return refused;
        const revision = stored.revision + 1;
        for (const key of Object.keys(stored)) delete (stored as Record<string, unknown>)[key];
        Object.assign(stored, replaced, { id: Number(termsRow[1]), employmentId: row.id, validFrom, revision });
        return jsonResponse(200, stored);
      }
    }
    return undefined;
  };

  /**
   * The documents and their categories (design S14), as the server judges
   * them: a person's archive is theirs, `staff:manage`'s without the health
   * documents and `staff:personal-data`'s — anyone else the unknown id's 404;
   * a write is `staff:manage`'s or `staff:personal-data`'s, a restricted
   * category the latter's alone (`category_restricted`); the type, the size
   * and the count refused as the server words them; the categories read by
   * view, manage and personal data, written by manage, a seed never changed
   * (`seed_immutable`) and a category in use never archived
   * (`category_in_use`). It does not model the signing date a contract sets.
   */
  const documentRoutes = (
    method: string,
    path: string,
    init: RequestInit | undefined,
    body: Record<string, unknown> | undefined,
  ): Response | undefined => {
    const writer = manages(role) || holdsPersonalData(role);
    const readerOf = (personId: number) => {
      const stored = findPerson(personId);
      if (!stored) return undefined;
      const self = server.me === personId;
      if (!self && !manages(role) && !holdsPersonalData(role)) return undefined;
      return { stored, health: self || holdsPersonalData(role) };
    };
    const visible = (document: StaffDocument, health: boolean) => health || !document.category.restricted;
    const restrictedRefusal = () =>
      staffProblem(403, {
        code: "category_restricted",
        title: "Restricted category",
        detail:
          "Documents in this category are health documents, which only staff:personal-data files, moves or removes.",
      });
    const summaryOf = (category: StaffDocumentCategory) => ({
      id: category.id,
      key: category.key,
      names: category.names,
      class: category.class,
      restricted: category.restricted,
    });

    // The fake cannot read the bytes: a file is taken when its type or its
    // extension names one of the four, or when neither names anything — the
    // server's sniff would decide that one — as `documentTypeOf` takes it.
    const sniffsAsDocument = (file: File) => {
      const type = file.type.toLowerCase();
      const extension = file.name.includes(".") ? file.name.split(".").pop()?.toLowerCase() : undefined;
      const knownType = ["application/pdf", "image/jpeg", "image/png", "image/heic", "image/heif"].includes(type);
      const knownExtension = ["pdf", "jpg", "jpeg", "png", "heic", "heif"].includes(extension ?? "");
      const namesNothing = (type === "" || type === "application/octet-stream") && extension === undefined;
      return knownType || knownExtension || namesNothing;
    };

    if (path === "/api/v1/staff/me/documents" || path === "/api/v1/staff/me/export") {
      const stored = server.me === undefined ? undefined : findPerson(server.me);
      if (!stored) return meProblem();
      const own = state.documents.filter((one) => one.personId === stored.id);
      if (path.endsWith("/documents")) return jsonResponse(200, own);
      return new Response(
        JSON.stringify({ exportedAt: "2026-10-11T08:00:00Z", person: personOf(stored), documents: own }),
        {
          status: 200,
          headers: {
            "Content-Type": "application/json",
            "Content-Disposition": `attachment; filename="staff-${stored.employeeNumber}-personal-data.json"`,
          },
        },
      );
    }

    const archive = /^\/api\/v1\/staff\/people\/(\d+)\/documents$/.exec(path);
    if (archive) {
      const reader = readerOf(Number(archive[1]));
      if (method === "GET") {
        if (!reader) return notFound();
        return jsonResponse(
          200,
          state.documents.filter((one) => one.personId === reader.stored.id && visible(one, reader.health)),
        );
      }
      if (method === "POST") {
        if (!writer) return forbidden();
        if (!findPerson(Number(archive[1]))) return notFound();
        if (findPerson(Number(archive[1]))?.erasedAt) return erasedRefusal();
        const form = init?.body as FormData;
        const file = form.get("file");
        const category = state.documentCategories.find((one) => one.id === Number(form.get("categoryId")));
        if (category?.restricted && !holdsPersonalData(role)) return restrictedRefusal();
        const errors: Record<string, string[]> = {};
        let code: string | undefined;
        if (!(file instanceof File) || file.size === 0) errors.file = ["A document file is required"];
        else if (file.size > 10 * 1024 * 1024) {
          errors.file = ["A document is at most 10 MiB"];
          code = "document_too_large";
        } else if (!sniffsAsDocument(file)) {
          errors.file = [
            "A document must be a PDF, JPEG, PNG or HEIC file, and its contents must match the type and file name it is sent under",
          ];
          code = "document_type_unsupported";
        }
        if (!category) errors.categoryId = [`No document category has id ${form.get("categoryId")}`];
        if (Object.keys(errors).length > 0) return staffProblem(400, { code, title: "Invalid document", errors });
        const personId = Number(archive[1]);
        if (state.documents.filter((one) => one.personId === personId).length >= 200)
          return staffProblem(409, {
            code: "document_count_exceeded",
            title: "Too many documents",
            detail: "A person has at most 200 documents, counted whatever you may read. Remove one first.",
          });
        const upload = file as File;
        const created: StaffDocument = {
          id: nextId++,
          personId,
          category: summaryOf(category as StaffDocumentCategory),
          fileName: upload.name,
          contentType: upload.type,
          sizeBytes: upload.size,
          uploadedAt: "2026-10-11T08:00:00Z",
          revision: 1,
          ...(form.get("validTo") ? { validTo: String(form.get("validTo")) } : {}),
          ...(form.get("signedAt") ? { signedAt: String(form.get("signedAt")) } : {}),
        };
        state.documents.unshift(created);
        return jsonResponse(201, created);
      }
    }

    const one = /^\/api\/v1\/staff\/documents\/(\d+)$/.exec(path);
    if (one) {
      const document = state.documents.find((row) => row.id === Number(one[1]));
      const reader = document ? readerOf(document.personId) : undefined;
      if (!document || !reader || !visible(document, reader.health)) return notFound();
      // A string body, not a Blob: the suite's Blob is jsdom's, which the
      // undici of Node 22 (CI's) takes for a blob and calls a stream() it lacks.
      if (method === "GET")
        return new Response(`bytes of ${document.fileName}`, {
          status: 200,
          headers: {
            "Content-Type": document.contentType,
            "Content-Disposition": `inline; filename="${document.fileName.replace(/[^\x20-\x7e]/g, "_")}"; filename*=UTF-8''${encodeURIComponent(document.fileName)}`,
          },
        });
      if (!writer) return forbidden();
      if (document.category.restricted && !holdsPersonalData(role)) return restrictedRefusal();
      if (reader.stored.erasedAt) return erasedRefusal();
      if (method === "DELETE") {
        state.documents.splice(state.documents.indexOf(document), 1);
        return new Response(null, { status: 204 });
      }
      if (method === "PUT") {
        const stale = staleRevision(init, document.revision, "document");
        if (stale) return stale;
        const input = body as {
          categoryId: number;
          validTo?: string;
          signedAt?: string;
          caseId?: number | null;
        };
        const category = state.documentCategories.find((row) => row.id === input.categoryId);
        if (!category || category.archivedAt)
          return staffProblem(400, {
            title: "Invalid document",
            errors: { categoryId: ["Choose a category that is in use"] },
          });
        if (category.restricted && !holdsPersonalData(role)) return restrictedRefusal();
        delete document.validTo;
        delete document.signedAt;
        // caseId: written only when named — null clears it — and a document
        // moved out of a restricted category leaves its case.
        if (input.caseId !== undefined && !category.restricted)
          return staffProblem(400, {
            title: "Invalid document",
            errors: { caseId: ["Only a document of a restricted category belongs to a case"] },
          });
        if (input.caseId === null || !category.restricted) delete document.caseId;
        else if (input.caseId !== undefined) document.caseId = input.caseId;
        Object.assign(document, {
          category: summaryOf(category),
          ...(input.validTo ? { validTo: input.validTo } : {}),
          ...(input.signedAt ? { signedAt: input.signedAt } : {}),
          revision: document.revision + 1,
        });
        return jsonResponse(200, document);
      }
    }

    const categories = /^\/api\/v1\/staff\/document-categories(?:\/(\d+))?$/.exec(path);
    if (categories) {
      if (method === "GET")
        return readsDirectory(role) || writer ? jsonResponse(200, state.documentCategories) : forbidden();
      if (!manages(role)) return forbidden();
      if (method === "POST" && !categories[1]) {
        const input = body as { key: string; names: { en: string; nb: string }; class: "A" | "D" | "E" };
        if (!/^[a-z0-9-]{1,40}$/.test(input.key ?? ""))
          return staffProblem(400, {
            title: "Invalid document category",
            errors: { key: ["A key is 1 to 40 characters of a–z, 0–9 and hyphens"] },
          });
        if (state.documentCategories.some((row) => row.key === input.key))
          return staffProblem(400, {
            title: "Invalid document category",
            errors: { key: ["Another category already has this key"] },
          });
        const created: StaffDocumentCategory = {
          id: nextId++,
          key: input.key,
          names: input.names,
          class: input.class,
          restricted: input.class === "E",
          seeded: false,
          sort: state.documentCategories.length + 1,
          revision: 1,
        };
        state.documentCategories.push(created);
        return jsonResponse(201, created);
      }
      const category = state.documentCategories.find((row) => row.id === Number(categories[1]));
      if (!category) return notFound();
      if (method === "PUT") {
        const stale = staleRevision(init, category.revision, "document category");
        if (stale) return stale;
        if (category.seeded)
          return staffProblem(409, {
            code: "seed_immutable",
            title: "A seeded category",
            detail: "The six seeded categories never change.",
          });
        const input = body as { names: { en: string; nb: string }; archived: boolean };
        if (input.archived && !category.archivedAt && state.documents.some((row) => row.category.id === category.id))
          return staffProblem(409, {
            code: "category_in_use",
            title: "Category in use",
            detail: "Documents are filed under this category. Move them first.",
          });
        category.names = input.names;
        if (input.archived) category.archivedAt = category.archivedAt ?? "2026-10-11T08:00:00Z";
        else delete category.archivedAt;
        category.revision += 1;
        return jsonResponse(200, category);
      }
    }
    return undefined;
  };

  /**
   * Competence (staff phase 2, delivery A), as the server judges it: the
   * catalogue read by anyone with the app — the hidden and the archived
   * rows to `staff:manage` alone, when asked — and written by
   * `staff:manage`, a seed's fixed fields refused `seed_immutable`, a
   * category in use never archived (`category_in_use`), a held competence's
   * level fixed (`competence_in_use`); a person's rows read by everyone who
   * sees the person, the detail fields left out for a colleague; declared by
   * the person, added verified by a manager, an Approver or HR; one current
   * row per competence (`competence_held`), the level required or refused
   * by the competence (`level_required`, `level_not_allowed`), a verified
   * row not the person's to change or remove (`verified_row`) — the person
   * first, whatever else they hold — a renewed one nobody's (`superseded`),
   * never verified by the person (`own_record`), a skill never given an
   * expiry nor renewed (`skill_no_validity`); renewal superseding, its
   * removal bringing the previous row back, the chain removed by HR, the
   * evidence filed under the seeded `certificate` category and read back
   * through the row by the detail's readers. It does not model the events.
   */
  const competenceRoutes = (
    method: string,
    path: string,
    url: URL,
    init: RequestInit | undefined,
    body: Record<string, unknown> | undefined,
  ): Response | undefined => {
    const hr = manages(role);
    const problem = (status: number, code: string, detail: string, field?: string) =>
      staffProblem(status, {
        code,
        title: "Competence refused",
        detail,
        ...(field ? { errors: { [field]: [detail] } } : {}),
      });
    const invalid = (errors: Record<string, string[]>) => staffProblem(400, { title: "Invalid competence", errors });

    const categories = /^\/api\/v1\/staff\/competence-categories(?:\/(\d+))?$/.exec(path);
    if (categories) {
      if (method === "GET" && !categories[1]) return jsonResponse(200, state.competenceCategories);
      if (!hr) return forbidden();
      if (method === "POST" && !categories[1]) {
        const input = body as { key: string; names: { en: string; nb: string } };
        if (!/^[a-z0-9-]{1,40}$/.test(input.key ?? ""))
          return invalid({ key: ["A key is 1 to 40 characters of a–z, 0–9 and hyphens"] });
        if (state.competenceCategories.some((row) => row.key === input.key))
          return invalid({ key: ["Another category already has this key"] });
        const created: StaffCompetenceCategory = {
          id: nextId++,
          key: input.key,
          names: input.names,
          seeded: false,
          sort: state.competenceCategories.length + 1,
          revision: 1,
        };
        state.competenceCategories.push(created);
        return jsonResponse(201, created);
      }
      const category = state.competenceCategories.find((row) => row.id === Number(categories[1]));
      if (!category) return notFound();
      if (method === "PUT") {
        const stale = staleRevision(init, category.revision, "competence category");
        if (stale) return stale;
        if (category.seeded) return problem(409, "seed_immutable", "The five seeded categories never change.");
        const input = body as { names: { en: string; nb: string }; archived: boolean };
        if (
          input.archived &&
          !category.archivedAt &&
          state.competences.some((row) => row.categoryId === category.id && !row.archivedAt)
        )
          return problem(409, "category_in_use", "A competence not archived is filed under this category.");
        category.names = input.names;
        if (input.archived) category.archivedAt = category.archivedAt ?? "2026-10-11T08:00:00Z";
        else delete category.archivedAt;
        category.revision += 1;
        return jsonResponse(200, category);
      }
    }

    const catalogue = /^\/api\/v1\/staff\/competences(?:\/(\d+))?$/.exec(path);
    if (catalogue) {
      if (method === "GET" && !catalogue[1]) {
        const hidden = hr && url.searchParams.get("includeHidden") === "true";
        const archived = hr && url.searchParams.get("includeArchived") === "true";
        const categoryId = url.searchParams.get("categoryId");
        return jsonResponse(
          200,
          state.competences.filter(
            (row) =>
              (hidden || !row.hidden) &&
              (archived || !row.archivedAt) &&
              (categoryId === null || row.categoryId === Number(categoryId)),
          ),
        );
      }
      if (!hr) return forbidden();
      const lead = (input: { validityMonths?: number; reminderLeadDays?: number }) =>
        input.validityMonths === undefined
          ? {}
          : {
              reminderLeadDays:
                input.reminderLeadDays ?? (input.validityMonths <= 12 ? 30 : input.validityMonths <= 24 ? 60 : 180),
            };
      if (method === "POST" && !catalogue[1]) {
        const input = body as unknown as Omit<StaffCompetence, "id" | "seeded" | "hidden" | "sort" | "revision">;
        if (!/^[a-z0-9-]{1,60}$/.test(input.key ?? ""))
          return invalid({ key: ["A key is 1 to 60 characters of a–z, 0–9 and hyphens"] });
        if (state.competences.some((row) => row.key === input.key))
          return invalid({ key: ["Another competence already has this key"] });
        if (input.kind === "skill" && input.validityMonths !== undefined)
          return invalid({ validityMonths: ["A skill has no validity"] });
        const created: StaffCompetence = {
          ...input,
          ...lead(input),
          id: nextId++,
          seeded: false,
          hidden: false,
          sort: state.competences.length + 1,
          revision: 1,
        };
        state.competences.push(created);
        return jsonResponse(201, created);
      }
      const row = state.competences.find((one) => one.id === Number(catalogue[1]));
      if (!row) return notFound();
      if (method === "PUT") {
        const stale = staleRevision(init, row.revision, "competence");
        if (stale) return stale;
        const input = body as unknown as StaffCompetenceChange;
        if (
          row.seeded &&
          (input.categoryId !== row.categoryId ||
            input.hasLevel !== row.hasLevel ||
            input.validityMonths !== row.validityMonths ||
            input.archived)
        )
          return problem(
            409,
            "seed_immutable",
            "A seeded competence changes its names, reminderLeadDays, issuerHint and hidden alone.",
          );
        if (
          input.hasLevel !== row.hasLevel &&
          (state.personCompetences.some((one) => one.competence.id === row.id) ||
            // A profile item's minimum level is a use too (plan reading 28).
            state.competenceProfiles.some((profile) =>
              profile.items.some((item) => item.competenceId === row.id && item.minLevel !== undefined),
            ))
        )
          return problem(409, "competence_in_use", "A person's row names this competence; hasLevel is fixed.");
        delete row.validityMonths;
        delete row.reminderLeadDays;
        delete row.issuerHint;
        const { archived, ...fields } = input;
        Object.assign(row, fields, lead(input));
        if (archived) row.archivedAt = row.archivedAt ?? "2026-10-11T08:00:00Z";
        else delete row.archivedAt;
        row.revision += 1;
        return jsonResponse(200, row);
      }
    }

    // A person's competences: who the caller is to them decides everything.
    const callerName = () => (server.me === undefined ? undefined : findPerson(server.me)?.displayName) ?? "Harriet HR";
    const judge = (personId: number) => {
      const stored = findPerson(personId);
      const audience = stored ? audienceOf(stored) : undefined;
      if (!stored || !audience) return undefined;
      const verifier = (audience.manager || audience.hr || role === "approve") && !audience.self;
      // The person writes as themselves; a manager, an Approver or HR as the verifier.
      return { stored, audience, verifier, writer: audience.self || verifier };
    };
    const shaped = (row: StaffPersonCompetence, audience: Audience): StaffPersonCompetence => {
      const leadDays = state.competences.find((one) => one.id === row.competence.id)?.reminderLeadDays;
      const current = !row.superseded && row.validTo !== undefined;
      const expired = current && (row.validTo as string) < TODAY;
      const expiring =
        current && !expired && leadDays !== undefined && daysUntil(TODAY, row.validTo as string) <= leadDays;
      const whole = { ...row, expiring, expired };
      if (!(colleagueOnly(audience) && role !== "approve")) return whole;
      const detail = [
        "issuedOn",
        "issuer",
        "reference",
        "evidence",
        "source",
        "verifiedAt",
        "verifiedBy",
        "verifiedBasis",
        "verifiedNote",
        "seenAt",
      ];
      return Object.fromEntries(
        Object.entries(whole).filter(([field]) => !detail.includes(field)),
      ) as StaffPersonCompetence;
    };
    const levelRefusal = (hasLevel: boolean, level: unknown) => {
      if (hasLevel && level === undefined)
        return problem(400, "level_required", "This competence has a level; give one from 0 to 4", "level");
      if (!hasLevel && level !== undefined)
        return problem(400, "level_not_allowed", "This competence takes no level", "level");
      return undefined;
    };
    const skillRefusal = (kind: string, validTo: unknown) =>
      kind === "skill" && validTo !== undefined
        ? problem(400, "skill_no_validity", "A skill has no validity", "validTo")
        : undefined;
    const datesRefusal = (validFrom: string, validTo: unknown) =>
      typeof validTo === "string" && validTo < validFrom
        ? invalid({ validTo: ["validTo is before validFrom"] })
        : undefined;
    const stamp = (row: StaffPersonCompetence, basis: string, note?: string) => {
      Object.assign(row, {
        verified: true,
        verifiedAt: "2026-10-11T08:00:00Z",
        verifiedBy: callerName(),
        verifiedBasis: basis,
      });
      delete row.verifiedNote;
      if (note) row.verifiedNote = note;
    };

    const list = /^\/api\/v1\/staff\/people\/(\d+)\/competences$/.exec(path);
    if (list) {
      const reader = judge(Number(list[1]));
      if (!reader) return notFound();
      if (method === "GET") {
        const history = url.searchParams.get("history") === "true";
        const rows = state.personCompetences.filter(
          (row) => row.personId === reader.stored.id && (history || !row.superseded),
        );
        return jsonResponse(
          200,
          (history ? [...rows].sort((a, b) => b.declaredAt.localeCompare(a.declaredAt)) : rows).map((row) =>
            shaped(row, reader.audience),
          ),
        );
      }
      if (method === "POST") {
        if (!reader.writer) return forbidden();
        if (reader.stored.erasedAt) return erasedRefusal();
        const input = body as {
          competenceId: number;
          level?: number;
          issuedOn?: string;
          validFrom?: string;
          validTo?: string;
          issuer?: string;
          reference?: string;
        };
        const picked = state.competences.find((one) => one.id === input.competenceId);
        if (!picked || picked.hidden || picked.archivedAt)
          return problem(400, "competence_unavailable", "Choose a competence that is offered", "competenceId");
        const refused = levelRefusal(picked.hasLevel, input.level) ?? skillRefusal(picked.kind, input.validTo);
        if (refused) return refused;
        if (
          state.personCompetences.some(
            (row) => row.personId === reader.stored.id && row.competence.id === picked.id && !row.superseded,
          )
        )
          return problem(409, "competence_held", "The person holds a current row of this competence.");
        const validFrom = input.validFrom ?? input.issuedOn ?? TODAY;
        const dates = datesRefusal(validFrom, input.validTo);
        if (dates) return dates;
        const created: StaffPersonCompetence = {
          id: nextId++,
          personId: reader.stored.id,
          competence: {
            id: picked.id,
            key: picked.key,
            names: picked.names,
            kind: picked.kind,
            hasLevel: picked.hasLevel,
            categoryId: picked.categoryId,
            archived: false,
          },
          ...(input.level !== undefined ? { level: input.level } : {}),
          ...(input.issuedOn ? { issuedOn: input.issuedOn } : {}),
          validFrom,
          ...(input.validTo ? { validTo: input.validTo } : {}),
          ...(input.issuer ? { issuer: input.issuer } : {}),
          ...(input.reference ? { reference: input.reference } : {}),
          source: reader.audience.self ? "self" : hr ? "hr" : "manager",
          verified: false,
          superseded: false,
          expiring: false,
          expired: false,
          declaredAt: "2026-10-11T08:00:00Z",
          revision: 1,
        };
        if (!reader.audience.self) stamp(created, "observed");
        state.personCompetences.push(created);
        return jsonResponse(201, shaped(created, reader.audience));
      }
    }

    const one = /^\/api\/v1\/staff\/person-competences\/(\d+)(?:\/(verify|renew|evidence))?$/.exec(path);
    if (one) {
      const row = state.personCompetences.find((candidate) => candidate.id === Number(one[1]));
      const reader = row ? judge(row.personId) : undefined;
      if (!row || !reader) return notFound();
      const action = one[2];
      // The evidence's bytes, to the detail's readers alone — a colleague gets the unknown row's 404.
      if (action === "evidence" && method === "GET") {
        if ((colleagueOnly(reader.audience) && role !== "approve") || !row.evidence) return notFound();
        return new Response(`bytes of ${row.evidence.fileName}`, {
          status: 200,
          headers: {
            "Content-Type": row.evidence.contentType,
            "Content-Disposition": `inline; filename="${row.evidence.fileName.replace(/[^\x20-\x7e]/g, "_")}"; filename*=UTF-8''${encodeURIComponent(row.evidence.fileName)}`,
          },
        });
      }
      if (!reader.writer && !(action === "evidence" && holdsPersonalData(role))) return forbidden();
      if (reader.stored.erasedAt) return erasedRefusal();
      // Self first: on their own record HR is the person.
      const own = reader.audience.self;
      if (row.superseded && (action !== undefined || method !== "DELETE" || url.searchParams.get("chain") !== "true"))
        return problem(409, "superseded", "This row was renewed; only the current one changes.");

      if (action === "verify" && method === "POST") {
        if (reader.audience.self)
          return problem(403, "own_record", "Your own competence is verified by somebody else.");
        const input = body as { level?: number; basis: string; note?: string };
        const refused = levelRefusal(row.competence.hasLevel, input.level);
        if (refused) return refused;
        if (input.level !== undefined) row.level = input.level;
        stamp(row, input.basis, input.note);
        row.revision += 1;
        return jsonResponse(200, shaped(row, reader.audience));
      }
      if (action === "renew" && method === "POST") {
        if (row.competence.kind === "skill")
          return problem(409, "skill_no_validity", "A skill has no validity; it is changed, not renewed.");
        const input = body as {
          issuedOn?: string;
          validFrom?: string;
          validTo?: string;
          issuer?: string;
          reference?: string;
        };
        const validFrom = input.validFrom ?? input.issuedOn ?? TODAY;
        const months = state.competences.find((candidate) => candidate.id === row.competence.id)?.validityMonths;
        const validTo = input.validTo ?? proposedExpiry(input.issuedOn, months);
        const dates = datesRefusal(validFrom, validTo);
        if (dates) return dates;
        const renewed: StaffPersonCompetence = {
          id: nextId++,
          personId: row.personId,
          competence: row.competence,
          ...(row.level !== undefined ? { level: row.level } : {}),
          ...(input.issuedOn ? { issuedOn: input.issuedOn } : {}),
          validFrom,
          ...(validTo ? { validTo } : {}),
          ...(input.issuer ? { issuer: input.issuer } : {}),
          ...(input.reference ? { reference: input.reference } : {}),
          source: reader.audience.self ? "self" : hr ? "hr" : "manager",
          verified: false,
          superseded: false,
          expiring: false,
          expired: false,
          declaredAt: "2026-10-11T08:00:00Z",
          revision: 1,
        };
        if (!reader.audience.self) stamp(renewed, "observed");
        row.superseded = true;
        row.revision += 1;
        state.personCompetences.push(renewed);
        return jsonResponse(201, shaped(renewed, reader.audience));
      }
      if (action === "evidence" && method === "POST") {
        if (!reader.audience.self && !hr && !holdsPersonalData(role)) return forbidden();
        const file = (init?.body as FormData | undefined)?.get("file");
        if (!(file instanceof File) || file.size === 0) return invalid({ file: ["A document file is required"] });
        if (file.size > 10 * 1024 * 1024)
          return staffProblem(400, {
            code: "document_too_large",
            title: "Invalid document",
            errors: { file: ["A document is at most 10 MiB"] },
          });
        const category = state.documentCategories.find((candidate) => candidate.key === "certificate");
        const document: StaffDocument = {
          id: nextId++,
          personId: row.personId,
          category: {
            id: category?.id ?? 3,
            key: "certificate",
            names: category?.names ?? { en: "Certificate", nb: "Sertifikat" },
            class: "D",
            restricted: false,
          },
          fileName: file.name,
          contentType: file.type,
          sizeBytes: file.size,
          uploadedAt: "2026-10-11T08:00:00Z",
          revision: 1,
        };
        state.documents.unshift(document);
        row.evidence = {
          documentId: document.id,
          fileName: document.fileName,
          contentType: document.contentType,
          sizeBytes: document.sizeBytes,
        };
        row.revision += 1;
        return jsonResponse(200, shaped(row, reader.audience));
      }
      if (!action && method === "PUT") {
        if (own && row.verified)
          return problem(409, "verified_row", "A verified row is renewed, not changed, by the person.");
        const stale = staleRevision(init, row.revision, "competence row");
        if (stale) return stale;
        const input = body as {
          level?: number;
          issuedOn?: string;
          validFrom?: string;
          validTo?: string;
          issuer?: string;
          reference?: string;
        };
        const refused =
          levelRefusal(row.competence.hasLevel, input.level) ?? skillRefusal(row.competence.kind, input.validTo);
        if (refused) return refused;
        const validFrom = input.validFrom ?? row.validFrom;
        const dates = datesRefusal(validFrom, input.validTo);
        if (dates) return dates;
        for (const field of ["level", "issuedOn", "validTo", "issuer", "reference"] as const) delete row[field];
        Object.assign(row, input, { validFrom });
        row.revision += 1;
        return jsonResponse(200, shaped(row, reader.audience));
      }
      if (!action && method === "DELETE") {
        if (own && row.verified) return problem(409, "verified_row", "A verified row is not the person's to remove.");
        const chain = url.searchParams.get("chain") === "true";
        if (chain && (!hr || own)) return forbidden();
        const sameChain = (candidate: StaffPersonCompetence) =>
          candidate.personId === row.personId && candidate.competence.id === row.competence.id;
        state.personCompetences = state.personCompetences.filter(
          (candidate) => candidate !== row && !(chain && sameChain(candidate)),
        );
        // The renewal undone: the row it superseded, the newest, is the current one again.
        const previous = state.personCompetences
          .filter((candidate) => sameChain(candidate) && candidate.superseded)
          .sort((a, b) => b.declaredAt.localeCompare(a.declaredAt))[0];
        if (!row.superseded && previous) {
          previous.superseded = false;
          previous.revision += 1;
        }
        return new Response(null, { status: 204 });
      }
    }
    return undefined;
  };

  /**
   * What the person wants and the reviews (delivery B, design S8–S11), as
   * the server judges them: the three lists read by the person, their
   * manager, HR and staff:personal-data — anyone else the unknown person's
   * 404 (a colleague, an Approver) — and written whole by the person alone
   * (the uniform 403 for anyone else who sees them, a colleague and an
   * Approver included; no If-Match). A write is judged as the server judges
   * it: a property the request or a row does not take (`interests[0].level`),
   * then the list's rules — every refusal collected, keyed by the row's
   * place, a duplicate declaration on the row alone (`declarations[1]`) —
   * then the erased person (409), then, on the new and changed rows, a
   * competence or a location no longer offered. A list replaced keeps a row
   * equal to one it had, with its Seen stamp; a new or changed row is
   * unstamped (plan reading 18). The interests read in the catalogue's
   * order. A new interest in a hidden or archived competence is
   * `competence_unavailable` at its index.
   * The Seen is the manager's and HR's, never the person's (403), and stamps
   * every unstamped wish and current competence row, answering the counts.
   * A review is the verifier's — `own_record` for the person, the uniform
   * 403 for anyone else who reads the reviews, the 404 for a colleague —
   * dated today unless the body says an earlier day. It does not model the
   * events.
   */
  const wishesRoutes = (method: string, path: string, body: unknown): Response | undefined => {
    const lists = /^\/api\/v1\/staff\/people\/(\d+)\/(interests|preferences|availability)$/.exec(path);
    const seenPath = /^\/api\/v1\/staff\/people\/(\d+)\/wishes\/seen$/.exec(path);
    const reviewsPath = /^\/api\/v1\/staff\/people\/(\d+)\/competence-reviews$/.exec(path);
    const match = lists ?? seenPath ?? reviewsPath;
    if (!match) return undefined;
    const found = seen(Number(match[1]));
    if (!found) return notFound();
    const { stored, audience } = found;
    const readsWishes = audience.self || audience.manager || audience.hr || audience.personalData;
    const now = "2026-10-11T09:00:00Z";
    const invalid = (title: string, field: string, message: string, code?: string) =>
      staffProblem(400, { title, ...(code ? { code } : {}), errors: { [field]: [message] } });

    if (reviewsPath) {
      if (!readsReviews(audience)) return notFound();
      if (method === "GET")
        return jsonResponse(
          200,
          state.reviews
            .filter((one) => one.personId === stored.id)
            .sort((a, b) => b.reviewedOn.localeCompare(a.reviewedOn) || b.id - a.id)
            .map(unowned),
        );
      if (method !== "POST") return undefined;
      if (audience.self)
        return staffProblem(403, {
          code: "own_record",
          title: "Own record",
          detail: "A review of one's own competences is recorded by somebody else.",
        });
      if (!(audience.manager || audience.hr || role === "approve")) return forbidden();
      if (stored.erasedAt) return erasedRefusal();
      const reviewedOn = (body as { reviewedOn?: string } | undefined)?.reviewedOn ?? TODAY;
      if (reviewedOn > TODAY)
        return invalid("Invalid competence review", "reviewedOn", "A review is dated today or earlier");
      const review: StoredReview = {
        id: nextId++,
        personId: stored.id,
        reviewedOn,
        reviewer: callerDisplayName(),
        recordedAt: now,
      };
      state.reviews.push(review);
      return jsonResponse(201, unowned(review));
    }

    if (seenPath) {
      if (!readsWishes) return notFound();
      if (method !== "POST") return undefined;
      if (!((audience.manager || audience.hr) && !audience.self)) return forbidden();
      if (stored.erasedAt) return erasedRefusal();
      const stamp = <T extends { personId: number; seenAt?: string }>(rows: T[], current: (row: T) => boolean) => {
        let count = 0;
        for (const row of rows)
          if (row.personId === stored.id && !row.seenAt && current(row)) {
            row.seenAt = now;
            count += 1;
          }
        return count;
      };
      return jsonResponse(200, {
        seenAt: now,
        interests: stamp(state.interests, () => true),
        preferences: stamp(state.preferences, () => true),
        declarations: stamp(state.availability, () => true),
        competences: stamp(state.personCompetences, (row) => !row.superseded),
      });
    }

    const kind = lists?.[2] as "interests" | "preferences" | "availability";
    const held = (competenceId: number) =>
      state.personCompetences.some(
        (row) => row.personId === stored.id && row.competence.id === competenceId && !row.superseded,
      );
    // The catalogue's order (ListPersonInterests): the category's sort, then the competence's.
    const catalogueOrder = (competence: StaffCompetence) => {
      const category = state.competenceCategories.find((one) => one.id === competence.categoryId);
      return [category?.sort ?? 0, competence.categoryId, competence.sort, competence.id];
    };
    const byCatalogue = (a: StaffCompetence, b: StaffCompetence) => {
      const [x, y] = [catalogueOrder(a), catalogueOrder(b)];
      return x.map((value, index) => value - y[index]).find((difference) => difference !== 0) ?? 0;
    };
    const answer = (): Response => {
      const own = <T extends { personId: number }>(rows: T[]) =>
        rows.filter((row) => row.personId === stored.id).map(unowned);
      if (kind === "interests")
        return jsonResponse(200, {
          interests: own(state.interests)
            .map((row) => ({
              row,
              competence: state.competences.find((one) => one.id === row.competenceId) as StaffCompetence,
            }))
            .sort((a, b) => byCatalogue(a.competence, b.competence))
            .map(
              ({ row, competence }): StaffPersonInterest => ({
                competenceId: row.competenceId,
                competence: {
                  id: competence.id,
                  key: competence.key,
                  names: competence.names,
                  kind: competence.kind,
                  hasLevel: competence.hasLevel,
                  categoryId: competence.categoryId,
                  archived: competence.archivedAt !== undefined,
                },
                held: held(row.competenceId),
                ...(row.seenAt ? { seenAt: row.seenAt } : {}),
              }),
            ),
        });
      if (kind === "preferences") return jsonResponse(200, { preferences: own(state.preferences) });
      return jsonResponse(200, { declarations: own(state.availability) });
    };
    if (method === "GET") return readsWishes ? answer() : notFound();
    if (method !== "PUT") return undefined;
    // selfOnly: the 404 is for whoever does not see the person (seen() above); a colleague,
    // an Approver and anyone else who sees them but is not them is the uniform 403.
    if (!audience.self) return forbidden();
    const others = <T extends { personId: number }>(rows: T[]) => rows.filter((row) => row.personId !== stored.id);
    const mine = <T extends { personId: number }>(rows: T[]) => rows.filter((row) => row.personId === stored.id);

    // Every refusal of a stage collected before answering, as the server's fieldErrors are.
    const errors: Record<string, string[]> = {};
    let code: string | undefined;
    const refuse = (field: string, message: string, coded?: string) => {
      errors[field] = [...(errors[field] ?? []), message];
      code = code ?? coded;
    };
    const failed = () => Object.keys(errors).length > 0;
    const refusal = (title: string) => staffProblem(400, { title, ...(code ? { code } : {}), errors });

    // strictRows: a property the request or a row does not take, then the list itself when it is not said.
    const strictRows = (title: string, list: string, rowKeys: readonly string[]): Response | undefined => {
      const top = unknownProperties(body, [list], title);
      if (top) return top;
      const rows = (body as Record<string, unknown> | undefined)?.[list];
      if (Array.isArray(rows))
        for (const [index, row] of rows.entries())
          for (const key of Object.keys((row ?? {}) as Record<string, unknown>)
            .filter((one) => !rowKeys.includes(one))
            .sort())
            refuse(
              `${list}[${index}].${key}`,
              "This property is not one this request takes; the request was not applied",
            );
      if (failed()) return refusal(title);
      if (!Array.isArray(rows)) {
        refuse(list, `${list} is required: the whole list, or [] to remove every row`);
        return refusal(title);
      }
      return undefined;
    };

    if (kind === "interests") {
      const title = "Invalid interests";
      const strict = strictRows(title, "interests", ["competenceId"]);
      if (strict) return strict;
      const sent = (body as { interests: { competenceId: number }[] }).interests;
      if (sent.length > 20) {
        refuse("interests", `A person has at most 20 interests, but ${sent.length} were given`);
        return refusal(title);
      }
      const ids: number[] = [];
      for (const [index, { competenceId }] of sent.entries()) {
        if (ids.includes(competenceId))
          refuse(`interests[${index}].competenceId`, `Competence ${competenceId} is named twice`);
        else ids.push(competenceId);
      }
      if (failed()) return refusal(title);
      if (stored.erasedAt) return erasedRefusal();
      // Under the lock: a new interest names a competence that is offered; one on file is kept as it is.
      const before = mine(state.interests);
      for (const [index, competenceId] of ids.entries()) {
        if (before.some((one) => one.competenceId === competenceId)) continue;
        const competence = state.competences.find((one) => one.id === competenceId);
        if (!competence || competence.hidden || competence.archivedAt)
          refuse(
            `interests[${index}].competenceId`,
            `Competence ${competenceId} is unknown, hidden or archived, and no new interest names it`,
            "competence_unavailable",
          );
      }
      if (failed()) return refusal(title);
      state.interests = [
        ...others(state.interests),
        ...ids.map(
          (competenceId) =>
            before.find((one) => one.competenceId === competenceId) ?? { personId: stored.id, competenceId },
        ),
      ];
      return answer();
    }

    if (kind === "preferences") {
      const title = "Invalid preferences";
      const strict = strictRows(title, "preferences", ["kind", "value", "weight"]);
      if (strict) return strict;
      const sent = (body as { preferences: Omit<StaffPersonPreference, "seenAt">[] }).preferences;
      if (sent.length > 7) {
        refuse("preferences", `A person has at most 7 preferences, one per kind, but ${sent.length} were given`);
        return refusal(title);
      }
      const kinds: string[] = [];
      for (const [index, row] of sent.entries()) {
        const field = (name: string) => `preferences[${index}].${name}`;
        let known = preferenceKinds.includes(row.kind);
        if (!known) refuse(field("kind"), `A kind is one of ${preferenceKinds.join(", ")}, but was '${row.kind}'`);
        else if (kinds.includes(row.kind)) {
          refuse(field("kind"), `A person has one preference of the kind ${row.kind}; it is given twice`);
          known = false;
        }
        if (!Number.isInteger(row.weight) || row.weight < 1 || row.weight > 3)
          refuse(field("weight"), "A weight is 1 to 3");
        if (!known) continue;
        kinds.push(row.kind);
        const message = preferenceValueRefusal(row.kind, row.value);
        if (message) refuse(field("value"), message);
      }
      if (failed()) return refusal(title);
      if (stored.erasedAt) return erasedRefusal();
      const before = mine(state.preferences);
      const same = (a: Omit<StaffPersonPreference, "seenAt">, b: StaffPersonPreference) =>
        a.kind === b.kind && a.weight === b.weight && JSON.stringify(a.value) === JSON.stringify(b.value);
      // Under the lock: a new or changed location preference names locations that exist and are not archived.
      for (const [index, row] of sent.entries()) {
        if (row.kind !== "location" || before.some((one) => same(row, one))) continue;
        const ids = (row.value as { locationIds: number[] }).locationIds;
        const missing = ids.find((id) => !state.locations.some((one) => one.id === id && !one.archivedAt));
        if (missing !== undefined)
          refuse(
            `preferences[${index}].value`,
            `Location ${missing} is unknown or archived, and no new or changed preference names it`,
          );
      }
      if (failed()) return refusal(title);
      state.preferences = [
        ...others(state.preferences),
        ...sent.map((row) => before.find((one) => same(row, one)) ?? { ...row, personId: stored.id }),
      ];
      return answer();
    }

    const title = "Invalid availability";
    const strict = strictRows(title, "declarations", [
      "kind",
      "weekday",
      "fromDate",
      "toDate",
      "startMinute",
      "endMinute",
    ]);
    if (strict) return strict;
    const sent = (body as { declarations: Omit<StaffAvailabilityDeclaration, "seenAt">[] }).declarations;
    if (sent.length > 50) {
      refuse("declarations", `A declaration has at most 50 rows, but ${sent.length} were given`);
      return refusal(title);
    }
    const accepted: Omit<StaffAvailabilityDeclaration, "seenAt">[] = [];
    for (const [index, row] of sent.entries()) {
      let fail = false;
      const add = (name: string, message: string) => {
        refuse(`declarations[${index}].${name}`, message);
        fail = true;
      };
      const { weekday, startMinute: start, endMinute: end } = row;
      if (!availabilityKinds.includes(row.kind))
        add("kind", `A kind is one of ${availabilityKinds.join(", ")}, but was '${row.kind}'`);
      if (weekday !== undefined && (weekday < 1 || weekday > 7))
        add("weekday", "A weekday is 1 (Monday) to 7 (Sunday)");
      if (weekday === undefined && !row.fromDate)
        add("fromDate", "A row without a weekday is dated, and needs its first day");
      if (row.fromDate && row.toDate && row.toDate < row.fromDate)
        add("toDate", "The last day is on or after the first");
      if (start !== undefined && end === undefined)
        add("endMinute", "A row with a start minute needs its end minute too");
      else if (start === undefined && end !== undefined)
        add("startMinute", "A row with an end minute needs its start minute too");
      else if (start !== undefined && end !== undefined) {
        if (start < 0 || start > 1440) add("startMinute", "A minute of the day is 0 to 1440");
        if (end < 0 || end > 1440) add("endMinute", "A minute of the day is 0 to 1440");
        else if (end <= start) add("endMinute", "The end minute is after the start minute");
      }
      if (fail) continue;
      // A row equal in every field to one before it is refused on its own index, no field named.
      if (accepted.some((one) => sameDeclaration(one, row)))
        refuse(`declarations[${index}]`, "This row is given twice");
      else accepted.push(row);
    }
    if (failed()) return refusal(title);
    if (stored.erasedAt) return erasedRefusal();
    const before = mine(state.availability);
    state.availability = [
      ...others(state.availability),
      ...sent.map((row) => before.find((one) => sameDeclaration(one, row)) ?? { ...row, personId: stored.id }),
    ];
    return answer();
  };

  /** A competence's summary as the server answers it today, from the catalogue as it stands. */
  const competenceSummary = (competenceId: number): StaffCompetenceSummary => {
    const row = state.competences.find((one) => one.id === competenceId) as StaffCompetence;
    return {
      id: row.id,
      key: row.key,
      names: row.names,
      kind: row.kind,
      hasLevel: row.hasLevel,
      categoryId: row.categoryId,
      archived: row.archivedAt !== undefined,
    };
  };

  /** The catalogue's order (`ListCompetences`): the category's sort, then the competence's, then the id. */
  const catalogueRank = (competenceId: number): number[] => {
    const competence = state.competences.find((one) => one.id === competenceId) as StaffCompetence;
    const category = state.competenceCategories.find((one) => one.id === competence.categoryId);
    return [category?.sort ?? 0, competence.categoryId, competence.sort, competence.id];
  };
  const byCatalogueOrder = (a: number, b: number) => {
    const [x, y] = [catalogueRank(a), catalogueRank(b)];
    return x.map((value, index) => value - y[index]).find((difference) => difference !== 0) ?? 0;
  };

  const profileSummary = (profile: StaffCompetenceProfile) => ({
    id: profile.id,
    key: profile.key,
    names: profile.names,
    archived: profile.archivedAt !== undefined,
  });

  /**
   * The profiles that reach a person (plan reading 25): those HR assigned,
   * archived ones listed with `archived`, and the default of the department
   * their terms in force name today — none when no employment is in force,
   * none while the default is archived (reading 26) — merged by profile and
   * ordered by key.
   */
  const effectiveProfiles = (stored: StoredPerson) => {
    const department =
      stored.status.state === "active" ? state.departments.find((one) => one.id === stored.department?.id) : undefined;
    const byDefault = state.competenceProfiles.find(
      (one) => one.id === department?.competenceProfileId && !one.archivedAt,
    );
    const assigned = state.personProfiles
      .filter((one) => one.personId === stored.id)
      .map((one) => state.competenceProfiles.find((profile) => profile.id === one.profileId))
      .filter((profile): profile is StaffCompetenceProfile => profile !== undefined);
    const reached = [...new Set([...assigned, ...(byDefault ? [byDefault] : [])])];
    return reached
      .sort((a, b) => a.key.localeCompare(b.key) || a.id - b.id)
      .map((profile) => ({
        profile,
        assigned: assigned.includes(profile),
        department:
          department && profile === byDefault
            ? { id: department.id, code: department.code, name: department.name }
            : undefined,
      }));
  };

  const personProfileOf = ({ profile, assigned, department }: ReturnType<typeof effectiveProfiles>[number]) => ({
    profile: profileSummary(profile),
    assigned,
    ...(department ? { department } : {}),
  });

  /**
   * One item judged on the person's current row of its competence, in plan
   * reading 23's order (`gapStatus`): no row is missing; a certificate
   * declared is declared only; a level under the minimum is below it (a row
   * without a level never is); then, when the item must be valid, a start
   * not reached is missing, an expiry passed expired, an expiry within the
   * competence's lead expiring; else met. A skill
   * declared counts as met with `declared` (reading 24: `mustBeValid: false`
   * never judges the validity).
   */
  const gapStatusOf = (
    item: StaffCompetenceProfile["items"][number],
    row: StaffPersonCompetence | undefined,
  ): StaffCompetenceGapStatus => {
    if (!row) return "missing";
    const competence = state.competences.find((one) => one.id === item.competenceId) as StaffCompetence;
    if (competence.kind === "certificate" && !row.verified) return "declared_only";
    if (item.minLevel !== undefined && row.level !== undefined && row.level < item.minLevel) return "below_level";
    if (!item.mustBeValid) return "met";
    if (row.validFrom > TODAY) return "missing";
    if (row.validTo !== undefined && row.validTo < TODAY) return "expired";
    const lead = competence.reminderLeadDays;
    if (row.validTo !== undefined && lead !== undefined && daysUntil(TODAY, row.validTo) <= lead) return "expiring";
    return "met";
  };
  const counts = (status: StaffCompetenceGapStatus) => status !== "met" && status !== "expiring";

  /** The gap list (design S13): every unarchived profile reaching the person, each item judged, the not met summed. */
  const gapsOf = (stored: StoredPerson) => {
    const rows = state.personCompetences.filter((row) => row.personId === stored.id && !row.superseded);
    const profiles = effectiveProfiles(stored)
      .filter(({ profile }) => !profile.archivedAt)
      .map((reached) => {
        const items: StaffCompetenceGapItem[] = reached.profile.items.map((item) => {
          const row = rows.find((one) => one.competence.id === item.competenceId);
          const competence = competenceSummary(item.competenceId);
          return {
            competenceId: item.competenceId,
            competence,
            ...(item.minLevel !== undefined ? { minLevel: item.minLevel } : {}),
            mustBeValid: item.mustBeValid,
            status: gapStatusOf(item, row),
            declared: row !== undefined && !row.verified,
            archived: competence.archived,
            verified: row?.verified ?? false,
            ...(row?.level !== undefined ? { level: row.level } : {}),
            ...(row?.validTo !== undefined ? { validTo: row.validTo } : {}),
          };
        });
        return {
          ...personProfileOf(reached),
          items,
          notMet: items.filter((item) => counts(item.status)).length,
        };
      });
    return { profiles, notMet: profiles.reduce((sum, profile) => sum + profile.notMet, 0) };
  };

  /**
   * Profiles, gaps and the overview (delivery C, design S12–S14), as the
   * server judges them. The catalogue is read by anyone in the app and
   * written by HR — a POST's key, its names and items (at most 50, a
   * competence once, a level 0 to 4), then each item's competence: unknown,
   * or hidden or archived on a new or changed item, is
   * `competence_unavailable`, a minimum level on one without a level
   * `level_not_allowed`, both keyed `items[i]…` — a PUT under `If-Match`,
   * its 404 and revision after the request is judged on its own. The matrix
   * refuses an unknown `competenceId` on that field. A person's profiles and gaps are read by the person,
   * their manager, an Approver, HR and staff:personal-data, anyone else the
   * unknown person's 404; HR alone assigns (the uniform 403 for anyone else
   * who reads them), at most 20, a profile once, an unarchived one known —
   * each refused on `profileIds[i]` — after the erased person's 409. The
   * matrix is the directory's (`staff:view`, `staff:manage`) and the
   * interests overview HR's alone, the uniform 403 otherwise. It does not
   * model the events.
   */
  const competenceProfileRoutes = (
    method: string,
    path: string,
    url: URL,
    init: RequestInit | undefined,
    body: Record<string, unknown> | undefined,
  ): Response | undefined => {
    const hr = manages(role);
    const invalid = (title: string, errors: Record<string, string[]>, code?: string) =>
      staffProblem(400, { title, ...(code ? { code } : {}), errors });

    const profileTitle = "Invalid competence profile";
    type ProfileInput = {
      key?: string;
      names?: { en?: string; nb?: string };
      archived?: boolean;
      items: StaffCompetenceProfileItemInput[];
    };

    /** strictRows: a property the request or an item does not take, then the items when the body does not say them. */
    const profileStrictRefusal = (allowed: readonly string[]): Response | undefined => {
      const top = unknownProperties(body, allowed, profileTitle);
      if (top) return top;
      const items = body?.items;
      const errors: Record<string, string[]> = {};
      if (!Array.isArray(items)) errors.items = ["items is required: the whole list, or [] to remove every row"];
      else
        items.forEach((item, index) => {
          for (const key of Object.keys((item ?? {}) as Record<string, unknown>)
            .filter((one) => !["competenceId", "minLevel", "mustBeValid"].includes(one))
            .sort())
            errors[`items[${index}].${key}`] = [
              "This property is not one this request takes; the request was not applied",
            ];
        });
      return Object.keys(errors).length > 0 ? invalid(profileTitle, errors) : undefined;
    };

    /** The names (parseCompetenceNames) and the items on their own (parseProfileItems), collected into errors. */
    const parseProfile = (input: ProfileInput, errors: Record<string, string[]>) => {
      for (const [field, label, value] of [
        ["names.en", "An English name", input.names?.en],
        ["names.nb", "A Norwegian name", input.names?.nb],
      ] as const) {
        const length = [...(value ?? "").trim()].length;
        if (length === 0) errors[field] = [`${label} is required`];
        else if (length > 100)
          errors[field] = [`${label} cannot be longer than 100 characters, the given value was ${length} characters`];
      }
      const items = input.items;
      if (items.length > 50) {
        errors.items = ["A profile has at most 50 items"];
        return;
      }
      items.forEach((item, index) => {
        if (items.findIndex((other) => other.competenceId === item.competenceId) < index)
          errors[`items[${index}].competenceId`] = [
            `Competence ${item.competenceId} is already an item of this profile`,
          ];
        if (
          item.minLevel !== undefined &&
          !(Number.isInteger(item.minLevel) && item.minLevel >= 0 && item.minLevel <= 4)
        )
          errors[`items[${index}].minLevel`] = ["A minimum level is 0 to 4"];
      });
    };

    /**
     * The items judged against the catalogue (judgeProfileItems): an unknown
     * competence, or a hidden or archived one on a new or changed item, is
     * `competence_unavailable`; a minimum level on one without a level
     * `level_not_allowed` — the latter beside the former on the same index
     * for a hidden or archived one. `kept` are the profile's stored items.
     */
    const judgeProfileItems = (
      items: StaffCompetenceProfileItemInput[],
      kept: StaffCompetenceProfile["items"],
    ): Response | undefined => {
      const errors: Record<string, string[]> = {};
      let code: string | undefined;
      const coded = (field: string, rule: string, message: string) => {
        errors[field] = [...(errors[field] ?? []), message];
        code = code ?? rule;
      };
      items.forEach((item, index) => {
        const competence = state.competences.find((one) => one.id === item.competenceId);
        if (!competence) {
          coded(
            `items[${index}].competenceId`,
            "competence_unavailable",
            `Competence ${item.competenceId} is unknown, hidden or archived; a new or changed item cannot name it`,
          );
          return;
        }
        const unchanged = kept.some(
          (one) =>
            one.competenceId === item.competenceId &&
            one.minLevel === item.minLevel &&
            one.mustBeValid === (item.mustBeValid ?? true),
        );
        if ((competence.hidden || competence.archivedAt) && !unchanged)
          coded(
            `items[${index}].competenceId`,
            "competence_unavailable",
            `Competence ${item.competenceId} is hidden or archived; a new or changed item cannot name it`,
          );
        if (item.minLevel !== undefined && !competence.hasLevel)
          coded(`items[${index}].minLevel`, "level_not_allowed", "This competence takes no level");
      });
      return Object.keys(errors).length > 0 ? invalid(profileTitle, errors, code) : undefined;
    };
    const itemsOf = (items: StaffCompetenceProfileItemInput[]): StaffCompetenceProfile["items"] =>
      items.map((item) => ({
        competenceId: item.competenceId,
        competence: competenceSummary(item.competenceId),
        ...(item.minLevel !== undefined ? { minLevel: item.minLevel } : {}),
        mustBeValid: item.mustBeValid ?? true,
      }));
    const ordered = (items: StaffCompetenceProfile["items"]) =>
      [...items].sort((a, b) => byCatalogueOrder(a.competenceId, b.competenceId));
    const namesOf = (input: ProfileInput) => ({
      en: (input.names?.en ?? "").trim(),
      nb: (input.names?.nb ?? "").trim(),
    });

    // The server's order: If-Match, the strict body, the request on its own
    // (key, names, items), then — under the lock — the 404, the revision
    // and the catalogue; a POST's key taken last.
    const catalogue = /^\/api\/v1\/staff\/competence-profiles(?:\/(\d+))?$/.exec(path);
    if (catalogue) {
      if (method === "GET" && !catalogue[1]) return jsonResponse(200, state.competenceProfiles);
      if (!hr) return forbidden();
      if (method === "POST" && !catalogue[1]) {
        const strict = profileStrictRefusal(["key", "names", "items"]);
        if (strict) return strict;
        const input = body as ProfileInput;
        const errors: Record<string, string[]> = {};
        if (!/^[a-z0-9-]{1,40}$/.test(input.key ?? ""))
          errors.key = ["A key is 1 to 40 characters of a–z, 0–9 and hyphens"];
        parseProfile(input, errors);
        if (Object.keys(errors).length > 0) return invalid(profileTitle, errors);
        const refused = judgeProfileItems(input.items, []);
        if (refused) return refused;
        if (state.competenceProfiles.some((one) => one.key === input.key))
          return invalid(profileTitle, { key: ["Another profile already has this key"] });
        const created: StaffCompetenceProfile = {
          id: nextId++,
          key: input.key ?? "",
          names: namesOf(input),
          items: ordered(itemsOf(input.items)),
          revision: 1,
        };
        state.competenceProfiles.push(created);
        state.competenceProfiles.sort((a, b) => a.key.localeCompare(b.key) || a.id - b.id);
        return jsonResponse(201, created);
      }
      if (method !== "PUT" || !catalogue[1]) return undefined;
      if (new Headers(init?.headers).get("If-Match") === null) return staffProblem(428, { title: "If-Match required" });
      const strict = profileStrictRefusal(["names", "archived", "items"]);
      if (strict) return strict;
      const input = body as ProfileInput;
      const errors: Record<string, string[]> = {};
      parseProfile(input, errors);
      if (Object.keys(errors).length > 0) return invalid(profileTitle, errors);
      const profile = state.competenceProfiles.find((one) => one.id === Number(catalogue[1]));
      if (!profile) return notFound();
      const stale = staleRevision(init, profile.revision, "competence profile");
      if (stale) return stale;
      const refused = judgeProfileItems(input.items, profile.items);
      if (refused) return refused;
      profile.names = namesOf(input);
      profile.items = ordered(itemsOf(input.items));
      if (input.archived) profile.archivedAt = profile.archivedAt ?? "2026-10-11T08:00:00Z";
      else delete profile.archivedAt;
      profile.revision += 1;
      return jsonResponse(200, profile);
    }

    const person = /^\/api\/v1\/staff\/people\/(\d+)\/(competence-profiles|competence-gaps)$/.exec(path);
    if (person) {
      const found = seen(Number(person[1]));
      if (!found) return notFound();
      const { stored, audience } = found;
      // The Profiles group (plan reading 32): never the colleague as such.
      const reads = audience.self || audience.manager || audience.hr || audience.personalData || role === "approve";
      if (!reads) return notFound();
      if (person[2] === "competence-gaps") return method === "GET" ? jsonResponse(200, gapsOf(stored)) : undefined;
      const answer = () => jsonResponse(200, { profiles: effectiveProfiles(stored).map(personProfileOf) });
      if (method === "GET") return answer();
      if (method !== "PUT") return undefined;
      if (!audience.hr) return forbidden();
      const ids = ((body as { profileIds?: number[] } | undefined)?.profileIds ?? []).map(Number);
      const title = "Invalid profile assignment";
      if (ids.length > 20) return invalid(title, { profileIds: ["A person is assigned at most 20 profiles"] });
      const duplicates = Object.fromEntries(
        ids.flatMap((id, index) =>
          ids.indexOf(id) < index ? [[`profileIds[${index}]`, ["This profile is assigned already"]]] : [],
        ),
      );
      if (Object.keys(duplicates).length > 0) return invalid(title, duplicates);
      if (stored.erasedAt) return erasedRefusal();
      const unavailable = Object.fromEntries(
        ids.flatMap((id, index) => {
          const profile = state.competenceProfiles.find((one) => one.id === id);
          if (!profile) return [[`profileIds[${index}]`, [`No competence profile has id ${id}`]]];
          // An archived profile kept as it was assigned is no new assignment.
          const kept = state.personProfiles.some((one) => one.personId === stored.id && one.profileId === id);
          return profile.archivedAt && !kept ? [[`profileIds[${index}]`, ["The profile is archived"]]] : [];
        }),
      );
      if (Object.keys(unavailable).length > 0) return invalid(title, unavailable);
      state.personProfiles = [
        ...state.personProfiles.filter((one) => one.personId !== stored.id),
        ...ids.map((profileId) => ({ personId: stored.id, profileId })),
      ];
      return answer();
    }

    if (path === "/api/v1/staff/competence-matrix" && method === "GET") {
      if (!readsDirectory(role)) return forbidden();
      const query = url.searchParams;
      const errors: Record<string, string[]> = {};
      const validOn = query.get("validOn") ?? TODAY;
      if (!isIsoDate(validOn)) errors.validOn = ["A day is YYYY-MM-DD"];
      const minLevelText = query.get("minLevel");
      const minLevel = minLevelText === null ? undefined : Number(minLevelText);
      if (minLevel !== undefined && !(Number.isInteger(minLevel) && minLevel >= 0 && minLevel <= 4))
        errors.minLevel = ["A level is 0 to 4"];
      const categoryText = query.get("categoryId");
      const categoryId = categoryText === null ? undefined : Number(categoryText);
      if (categoryId !== undefined && !(Number.isInteger(categoryId) && categoryId > 0))
        errors.categoryId = ["An id is a positive whole number"];
      const competenceIds = query.getAll("competenceId").map(Number);
      if (competenceIds.some((id) => !(Number.isInteger(id) && id > 0)))
        errors.competenceId = ["An id is a positive whole number"];
      if (Object.keys(errors).length > 0) return invalid("Invalid matrix filter", errors);
      // A competence the catalogue does not know — hidden and archived ones it does — on competenceId.
      const unknown = competenceIds.filter((id) => !state.competences.some((one) => one.id === id));
      if (unknown.length > 0)
        return invalid("Invalid matrix filter", {
          competenceId: unknown.map((id) => `No competence has id ${id}`),
        });
      const verifiedOnly = query.get("verifiedOnly") === "true";

      const competenceOf = (id: number) => state.competences.find((one) => one.id === id) as StaffCompetence;
      const rowsOf = (personId: number) =>
        state.personCompetences.filter((row) => {
          const competence = competenceOf(row.competence.id);
          return (
            row.personId === personId &&
            !row.superseded &&
            row.validFrom <= validOn &&
            (row.validTo === undefined || row.validTo >= validOn) &&
            (categoryId === undefined || competence.categoryId === categoryId) &&
            (competenceIds.length === 0 || competenceIds.includes(competence.id)) &&
            (!verifiedOnly || row.verified) &&
            (minLevel === undefined || !competence.hasLevel || (row.level ?? -1) >= minLevel)
          );
        });
      const people = state.people
        .filter((one) => one.status.state === "active" && !one.erasedAt)
        .sort((a, b) => a.displayName.localeCompare(b.displayName) || a.id - b.id)
        .map((one) => ({ person: one, rows: rowsOf(one.id) }))
        // Given competences, a person is listed when they hold every one.
        .filter(({ rows }) => competenceIds.every((id) => rows.some((row) => row.competence.id === id)));
      const columns = (
        competenceIds.length > 0
          ? [...new Set(competenceIds)]
          : [...new Set(people.flatMap(({ rows }) => rows.map((row) => row.competence.id)))]
      ).sort(byCatalogueOrder);
      return jsonResponse(200, {
        validOn,
        competences: columns.map(competenceSummary),
        people: people.map(({ person: one, rows }) => ({
          personId: one.id,
          name: one.displayName,
          competences: [...rows]
            .sort((a, b) => byCatalogueOrder(a.competence.id, b.competence.id))
            .map((row) => ({
              competenceId: row.competence.id,
              ...(row.level !== undefined ? { level: row.level } : {}),
              verified: row.verified,
              validFrom: row.validFrom,
              ...(row.validTo !== undefined ? { validTo: row.validTo } : {}),
            })),
        })),
      });
    }

    if (path === "/api/v1/staff/competence-interests" && method === "GET") {
      if (!hr) return forbidden();
      const active = (id: number) => {
        const one = findPerson(id);
        return one?.status.state === "active" && !one.erasedAt ? one : undefined;
      };
      const held = (personId: number, competenceId: number) =>
        state.personCompetences.some(
          (row) => row.personId === personId && row.competence.id === competenceId && !row.superseded,
        );
      const wanted = state.interests.filter((one) => active(one.personId));
      const competenceIdsWanted = [...new Set(wanted.map((one) => one.competenceId))].sort(byCatalogueOrder);
      const peopleOf = (competenceId: number, holds: boolean) =>
        wanted
          .filter((one) => one.competenceId === competenceId && held(one.personId, competenceId) === holds)
          .map((one) => active(one.personId) as StoredPerson)
          .sort((a, b) => a.displayName.localeCompare(b.displayName) || a.id - b.id)
          .map(summaryOf);
      return jsonResponse(
        200,
        competenceIdsWanted.map((competenceId) => ({
          competence: competenceSummary(competenceId),
          wantsToLearn: peopleOf(competenceId, false),
          wantsMore: peopleOf(competenceId, true),
        })),
      );
    }
    return undefined;
  };

  /**
   * The attention items My team reads (`GET /stats/attention`), the
   * competence ones alone (Staff phase 2): a declared row, a certificate
   * expiring within its competence's lead or expired, and an overdue review
   * — the interval after the later of the person's last review and the
   * start of their employment (the fixtures' runs are unbroken). HR reads
   * everyone's, an Approver the declared rows, a manager their reports';
   * never one's own, only people active today and not erased.
   */
  const attention = (): Response => {
    const hr = manages(role);
    const reportOf = (personId: number) => {
      const person = findPerson(personId);
      return person !== undefined && server.me !== undefined && managerToday(person)?.id === server.me;
    };
    const counted = (personId: number) => {
      const person = findPerson(personId);
      return person?.status.state === "active" && !person.erasedAt && personId !== server.me;
    };
    const item = (type: string, key: number, personId: number, occurredAt: string) => ({
      id: `${type}/${key}`,
      type,
      title: findPerson(personId)?.displayName ?? "",
      entityId: String(personId),
      occurredAt,
    });
    const items = [];
    for (const row of state.personCompetences) {
      if (row.superseded || !counted(row.personId)) continue;
      if (!row.verified && (hr || role === "approve" || reportOf(row.personId)))
        items.push(item("competenceDeclared", row.id, row.personId, row.declaredAt));
      if (!row.validTo || !(hr || reportOf(row.personId))) continue;
      const lead = state.competences.find((one) => one.id === row.competence.id)?.reminderLeadDays;
      if (row.validTo < TODAY) items.push(item("competenceExpired", row.id, row.personId, `${row.validTo}T00:00:00Z`));
      else if (lead !== undefined && daysUntil(TODAY, row.validTo) <= lead)
        items.push(item("competenceExpiring", row.id, row.personId, `${row.validTo}T00:00:00Z`));
    }
    const interval = state.settings.competenceReviewIntervalMonths;
    if (interval > 0)
      for (const person of state.people) {
        if (!counted(person.id) || !(hr || reportOf(person.id))) continue;
        const start = state.employments
          .filter(
            (one) => one.personId === person.id && one.startDate <= TODAY && (!one.endDate || one.endDate >= TODAY),
          )
          .map((one) => one.startDate)
          .sort()[0];
        if (!start) continue;
        const last = latestReview(person.id)?.reviewedOn;
        const anchor = last && last > start ? last : start;
        const due = proposedExpiry(anchor, interval) as string;
        if (TODAY > due) items.push(item("competenceReviewOverdue", person.id, person.id, `${due}T00:00:00Z`));
      }
    // A gap (delivery C, plan reading 31): one per person with an item not
    // met, to HR and the person's manager, never the Approver alone.
    for (const person of state.people) {
      if (!counted(person.id) || !(hr || reportOf(person.id))) continue;
      const { notMet } = gapsOf(person);
      if (notMet > 0)
        items.push({ ...item("competenceGap", person.id, person.id, `${TODAY}T00:00:00Z`), count: notMet });
    }
    return jsonResponse(200, items);
  };

  /**
   * The data operations, retention and the thresholds (design S15, S18–S20),
   * every one `staff:manage`'s: the import answering `importTally` with the
   * request's `dryRun` (a file refused whole — `import_sealed_columns` and
   * the rest — is staged with `write`), the register's export and template
   * and the payroll month as attachments named as the server names them, a
   * run of retention by hand (`retention_busy` staged with `write`), and a
   * threshold acknowledged once and for good.
   */
  const dataRoutes = (method: string, path: string, url: URL): Response | undefined => {
    const file = (text: string, fileName: string) =>
      new Response(text, {
        status: 200,
        headers: {
          "Content-Type": "text/csv; charset=utf-8",
          "Content-Disposition": `attachment; filename="${fileName}"`,
        },
      });
    const known = [
      "/api/v1/staff/people/import",
      "/api/v1/staff/people/export.csv",
      "/api/v1/staff/people/import-template.csv",
      "/api/v1/staff/payroll/export",
      "/api/v1/staff/retention/run",
      "/api/v1/staff/accounts/run",
    ];
    const threshold = /^\/api\/v1\/staff\/thresholds\/([^/]+)\/acknowledge$/.exec(path);
    if (!known.includes(path) && !threshold) return undefined;
    if (!manages(role)) return forbidden();

    if (path === "/api/v1/staff/accounts/run" && method === "POST")
      return jsonResponse(200, server.accountsRun ?? { attempted: 0, done: 0, failed: 0, remaining: 0 });

    if (path === "/api/v1/staff/people/import" && method === "POST") {
      const dryRun = url.searchParams.get("dryRun") ?? "true";
      if (dryRun !== "true" && dryRun !== "false")
        return staffProblem(400, { title: "Invalid import", errors: { dryRun: ["dryRun is true or false"] } });
      return jsonResponse(200, { ...(server.importTally ?? defaultImportTally()), dryRun: dryRun === "true" });
    }
    if (path === "/api/v1/staff/people/export.csv" && method === "GET")
      return file(
        `\ufeffemployeeNumber;givenName;familyName\r\n${state.people
          .map((one) => `${one.employeeNumber};${one.givenName};${one.familyName}\r\n`)
          .join("")}`,
        `staff-people-${TODAY}.csv`,
      );
    if (path === "/api/v1/staff/people/import-template.csv" && method === "GET")
      return file(
        `\ufeffemployeeNumber;givenName;familyName${holdsPersonalData(role) ? ";nationalIdKind;nationalId" : ""};startDate\r\n`,
        "staff-people-import-template.csv",
      );
    if (path === "/api/v1/staff/payroll/export" && method === "GET") {
      if (!manages(role) || !holdsPersonalData(role)) return forbidden();
      const month = url.searchParams.get("month") ?? "";
      if (!/^\d{4}-(0[1-9]|1[0-2])$/.test(month))
        return staffProblem(400, {
          code: "payroll_month_invalid",
          title: "Invalid payroll export",
          errors: { month: [`A month is YYYY-MM with a month from 01 to 12, but was '${month}'`] },
        });
      return file("\ufeffEmployee number;Name\r\nE-1;Ada Lovelace\r\n", `staff-payroll-${month}.csv`);
    }
    if (path === "/api/v1/staff/retention/run" && method === "POST")
      return jsonResponse(200, server.retentionRun ?? defaultRetentionRun());
    if (threshold && method === "POST") {
      const key = threshold[1] as StaffThreshold;
      if (!["5", "30", "50", "otp"].includes(key))
        return staffProblem(404, {
          code: "threshold_unknown",
          title: "Unknown threshold",
          detail: "A threshold is 5, 30, 50 or otp.",
        });
      // Once and for good: a second acknowledgement keeps the first date.
      state.settings = {
        ...state.settings,
        thresholdsAcknowledged: {
          [key]: "2026-10-11T12:00:00Z",
          ...state.settings.thresholdsAcknowledged,
        },
      };
      return jsonResponse(200, state.settings.thresholdsAcknowledged);
    }
    return undefined;
  };

  /**
   * Absence (delivery B): the caller's own rows, account, standing and care
   * days; the calendar's public holidays and people; the approvals and the
   * decisions on them; the open cases and their milestones. A write is
   * judged as far as a page shows it — the If-Match the row carries (its
   * revision, or its etag quoted), a reason on a rejection — and anything
   * the server would refuse further is staged with `write`.
   */
  const absenceRoutes = (
    method: string,
    path: string,
    url: URL,
    init: RequestInit | undefined,
    body: Record<string, unknown> | undefined,
  ): Response | undefined => {
    const year = Number(url.searchParams.get("year")) || Number(TODAY.slice(0, 4));
    const types = defaultMeta(role).absenceTypes;
    const holidays = server.absence?.publicHolidays ?? ["2026-12-25", "2026-12-26"];
    const inRange = (day: string, from: string, to: string) => day >= from && day <= to;
    const weekdays = (from: string, to: string) => {
      let count = 0;
      for (
        let day = new Date(`${from}T00:00:00Z`);
        day <= new Date(`${to}T00:00:00Z`);
        day.setUTCDate(day.getUTCDate() + 1)
      ) {
        const iso = day.toISOString().slice(0, 10);
        if (day.getUTCDay() % 6 !== 0 && !holidays.includes(iso)) count += 1;
      }
      return count;
    };
    const ifMatchOf = () => new Headers(init?.headers).get("If-Match");
    const tokenOf = (row: Schemas["StaffAbsence"]) => (row.etag ? `"${row.etag}"` : String(row.revision));
    const changed = () =>
      staffProblem(409, {
        code: "revision_conflict",
        title: "Revision conflict",
        detail: "The absence has changed since it was read.",
      });
    const accountOf = (wanted: number) => {
      const found = server.absence?.holidayAccounts?.find((one) => one.year === wanted);
      return found ?? defaultHolidayAccount({ year: wanted });
    };

    if (path === "/api/v1/staff/me/holiday-account" && method === "GET") return jsonResponse(200, accountOf(year));
    if (path === "/api/v1/staff/me/self-certification" && method === "GET")
      return jsonResponse(200, server.absence?.selfCertification ?? defaultSelfCertification());
    if (path === "/api/v1/staff/me/care-days" && method === "GET")
      return jsonResponse(200, { ...(server.absence?.careDays ?? defaultCareDays()), year });
    if (path === "/api/v1/staff/me/absences") {
      if (server.me === undefined) return meProblem();
      if (method === "GET")
        return jsonResponse(
          200,
          page(state.myAbsences, Math.max(1, Number(url.searchParams.get("page")) || 1), pageSize),
        );
      if (method === "POST") {
        const input = body as Schemas["StaffAbsenceChangeRequest"];
        const type = types.find((one) => one.id === input.typeId);
        if (!type) return staffProblem(400, { title: "Invalid absence", errors: { typeId: ["No such type"] } });
        const lastDay = input.lastDay ?? input.firstDay;
        const row: Schemas["StaffAbsence"] = {
          ...input,
          id: nextId++,
          personId: server.me,
          status: type.needsDecision ? "requested" : "registered",
          lastDay,
          workingDays: type.unit === "hours" ? 0 : weekdays(input.firstDay, lastDay),
          shownAs: "type",
          percent: input.percent ?? 100,
          revision: 1,
        };
        state.myAbsences.unshift(row);
        if (type.drawsHoliday) {
          const account = accountOf(Number(input.firstDay.slice(0, 4)));
          const pending = account.pending + (row.workingDays ?? 0);
          const after = { ...account, pending, overdrawn: account.remaining - pending < 0 };
          return jsonResponse(201, { ...row, accounts: [after], overdrawn: after.overdrawn });
        }
        return jsonResponse(201, row);
      }
    }

    if (path === "/api/v1/staff/calendar" && method === "GET") {
      const from = url.searchParams.get("from") ?? "";
      const to = url.searchParams.get("to") ?? "";
      const people = (server.absence?.calendarPeople ?? defaultCalendarPeople()).map((one) => ({
        ...one,
        absences: one.absences.filter((absence) => absence.firstDay <= to && absence.lastDay >= from),
      }));
      return jsonResponse(200, {
        from,
        to,
        publicHolidays: holidays.filter((day) => inRange(day, from, to)),
        people,
      });
    }

    if (path === "/api/v1/staff/approvals" && method === "GET")
      return jsonResponse(200, page(state.approvals, 1, pageSize));

    // The person page's Absence tab: a person's rows, account, adjustments,
    // standing and care days, as the test shaped them for the caller.
    const personRows = (personId: number) => state.personAbsence[personId]?.absences;
    const rowOf = (id: number) =>
      state.myAbsences.find((one) => one.id === id) ??
      Object.values(state.personAbsence)
        .flatMap((one) => one.absences)
        .find((one) => one.id === id);
    const holidayPerson =
      /^\/api\/v1\/staff\/people\/(\d+)\/(holiday-account|holiday-adjustments|carry-over|self-certification|self-certification\/withdrawal|care-days)$/.exec(
        path,
      );
    if (holidayPerson) {
      const personId = Number(holidayPerson[1]);
      const read = state.personAbsence[personId];
      if (!read) return notFound();
      switch (holidayPerson[2]) {
        case "holiday-account": {
          const found = read.accounts?.find((one) => one.year === year);
          return jsonResponse(200, found ?? defaultHolidayAccount({ personId, year }));
        }
        case "holiday-adjustments": {
          if (!manages(role)) return forbidden();
          read.adjustments ??= [];
          if (method === "GET") return jsonResponse(200, read.adjustments);
          const input = body as Schemas["StaffHolidayAdjustmentRequest"];
          const created = { ...input, id: nextId++, personId, createdAt: "2026-10-11T08:00:00Z" };
          read.adjustments.unshift(created);
          return jsonResponse(201, created);
        }
        case "carry-over": {
          if (!manages(role)) return forbidden();
          const input = body as Schemas["StaffCarryOverRequest"];
          read.adjustments ??= [];
          if (read.adjustments.some((one) => one.fromYear === input.fromYear && one.reason !== "correction"))
            return staffProblem(409, {
              code: "carry_over_recorded",
              title: "Carry-over recorded",
              detail: `A carry-over from ${input.fromYear} is already recorded.`,
            });
          const created: Schemas["StaffHolidayAdjustment"] = {
            id: nextId++,
            personId,
            year: input.fromYear + 1,
            days: input.days ?? 4,
            reason: input.kind === "forced" ? "forced_carry_over" : "agreed_carry_over",
            fromYear: input.fromYear,
            ...(input.note ? { note: input.note } : {}),
            createdAt: "2026-10-11T08:00:00Z",
          };
          read.adjustments.unshift(created);
          return jsonResponse(201, created);
        }
        case "self-certification":
          return read.selfCertification ? jsonResponse(200, read.selfCertification) : notFound();
        case "self-certification/withdrawal": {
          if (!manages(role)) return forbidden();
          const input = body as Schemas["StaffSelfCertWithdrawalRequest"];
          const standing = read.selfCertification ?? defaultSelfCertification({ personId });
          delete standing.withdrawnUntil;
          delete standing.withdrawnReason;
          delete standing.withdrawnFrom;
          if (input.until)
            Object.assign(standing, {
              withdrawnUntil: input.until,
              withdrawnReason: input.reason,
              withdrawnFrom: TODAY,
            });
          read.selfCertification = standing;
          return jsonResponse(200, standing);
        }
        case "care-days":
          return read.careDays ? jsonResponse(200, { ...read.careDays, year }) : notFound();
      }
    }
    const adjustment = /^\/api\/v1\/staff\/holiday-adjustments\/(\d+)$/.exec(path);
    if (adjustment && method === "DELETE") {
      if (!manages(role)) return forbidden();
      for (const read of Object.values(state.personAbsence)) {
        const index = (read.adjustments ?? []).findIndex((one) => one.id === Number(adjustment[1]));
        if (index >= 0) {
          read.adjustments?.splice(index, 1);
          return new Response(null, { status: 204 });
        }
      }
      return notFound();
    }
    if (path === "/api/v1/staff/absences") {
      const personId = Number(url.searchParams.get("personId"));
      if (method === "GET") {
        // from and to keep the rows that touch the range; paged as the server pages.
        const from = url.searchParams.get("from") ?? "0000-01-01";
        const to = url.searchParams.get("to") ?? "9999-12-31";
        const rows = (personRows(personId) ?? []).filter((one) => one.firstDay <= to && one.lastDay >= from);
        const size = Math.min(Number(url.searchParams.get("pageSize")) || pageSize, pageSize);
        return jsonResponse(200, page(rows, Math.max(1, Number(url.searchParams.get("page")) || 1), size));
      }
      if (method === "POST") {
        if (!manages(role)) return forbidden();
        const input = body as Schemas["StaffAbsenceRequest"];
        const type = types.find((one) => one.id === input.typeId);
        const rows = personRows(input.personId);
        if (!type || !rows) return notFound();
        const lastDay = input.lastDay ?? input.firstDay;
        const row: Schemas["StaffAbsence"] = {
          ...input,
          id: nextId++,
          status: type.needsDecision ? "requested" : "registered",
          lastDay,
          workingDays: type.unit === "hours" ? 0 : weekdays(input.firstDay, lastDay),
          shownAs: "type",
          percent: input.percent ?? 100,
          etag: `e${nextId}`,
        };
        rows.unshift(row);
        return jsonResponse(201, row);
      }
    }

    const absence = /^\/api\/v1\/staff\/absences\/(\d+)\/?(approve|reject|cancel|correct|postpone-holiday)?$/.exec(
      path,
    );
    if (absence) {
      const id = Number(absence[1]);
      const action = absence[2];
      if (action === "approve" || action === "reject") {
        const waiting = state.approvals.find((one) => one.absence.id === id);
        if (!waiting) return notFound();
        if (action === "reject" && !String((body as { note?: unknown })?.note ?? "").trim())
          return staffProblem(400, { title: "Invalid decision", errors: { note: ["A rejection says why"] } });
        state.approvals = state.approvals.filter((one) => one !== waiting);
        return jsonResponse(200, { ...waiting.absence, status: action === "approve" ? "approved" : "rejected" });
      }
      const row = rowOf(id);
      if (!row) return notFound();
      if (action === "cancel" && method === "POST") {
        if (row.status !== "requested" && !(row.status === "approved" && manages(role)))
          return staffProblem(409, {
            code: "absence_not_cancellable",
            title: "Absence not cancellable",
            detail: "This absence cannot be cancelled by you.",
          });
        row.status = "cancelled";
        if (row.revision !== undefined) row.revision += 1;
        if (row.etag) row.etag = `${row.etag}+`;
        return jsonResponse(200, row);
      }
      if (action === "postpone-holiday" && method === "POST") {
        if (!manages(role)) return forbidden();
        const demandedOn = (body as { demandedOn?: string } | undefined)?.demandedOn ?? TODAY;
        if (demandedOn < row.firstDay)
          return staffProblem(400, {
            title: "Invalid demand",
            errors: { demandedOn: ["The demand is never dated before the sykmelding's first day"] },
          });
        return jsonResponse(200, { demandedOn, absences: [] });
      }
      if ((!action && method === "PUT") || (action === "correct" && method === "POST")) {
        if (ifMatchOf() !== tokenOf(row)) return changed();
        if (action === "correct" && row.status !== "approved" && row.status !== "registered")
          return staffProblem(409, {
            code: "absence_not_cancellable",
            title: "Absence not cancellable",
            detail: "Only an approved or registered absence is corrected.",
          });
        const input = body as Schemas["StaffAbsenceChangeRequest"];
        for (const key of ["percent", "hours", "degreePercent", "note"] as const) delete row[key];
        Object.assign(row, input, { lastDay: input.lastDay ?? input.firstDay });
        if (row.revision !== undefined) row.revision += 1;
        if (row.etag) row.etag = `${row.etag}+`;
        return jsonResponse(200, row);
      }
    }

    if (path === "/api/v1/staff/cases" && method === "GET") {
      const personId = Number(url.searchParams.get("personId")) || undefined;
      const status = url.searchParams.get("status") ?? "open";
      const listed = state.cases.filter(
        (one) => (personId === undefined || one.personId === personId) && (status === "all" || one.open),
      );
      return jsonResponse(200, page(listed, 1, pageSize));
    }
    const sickCase = /^\/api\/v1\/staff\/cases\/(\d+)(?:\/(close)|\/milestones\/(\w+)\/(done|waive|note))?$/.exec(path);
    if (sickCase) {
      const found = state.cases.find((one) => one.id === Number(sickCase[1]));
      if (!found) return notFound();
      if (sickCase[2] && method === "POST") {
        if (!manages(role)) return forbidden();
        found.open = false;
        found.closedAt = "2026-10-11T08:00:00Z";
        for (const milestone of found.milestones) milestone.due = false;
        return jsonResponse(200, found);
      }
      if (sickCase[3]) {
        const milestone = found.milestones.find((one) => one.kind === sickCase[3]);
        if (!milestone || milestone.kind === "employer_period_end")
          return staffProblem(400, { title: "Invalid milestone", errors: { kind: ["Not a milestone to mark"] } });
        if (sickCase[4] === "note" && method === "PUT") {
          if (!holdsPersonalData(role)) return forbidden();
          const stale = staleRevision(init, milestone.revision ?? 0, "milestone");
          if (stale) return stale;
          const note = (body as { note?: string } | undefined)?.note;
          if (note) milestone.note = note;
          else delete milestone.note;
          milestone.revision = (milestone.revision ?? 0) + 1;
          return jsonResponse(200, found);
        }
        if (sickCase[4] === "done") {
          milestone.doneAt = (body as { doneAt?: string } | undefined)?.doneAt ?? TODAY;
          milestone.waived = false;
          delete milestone.waivedReason;
        } else {
          const reason = String((body as { reason?: unknown })?.reason ?? "").trim();
          if (!reason) return staffProblem(400, { title: "Invalid waiver", errors: { reason: ["Say why"] } });
          milestone.waived = true;
          delete milestone.doneAt;
        }
        milestone.due = false;
        milestone.revision = (milestone.revision ?? 0) + 1;
        return jsonResponse(200, found);
      }
      if (method === "PUT") {
        const stale = staleRevision(init, found.revision ?? 0, "case");
        if (stale) return stale;
        // A field left out is kept; one sent is written, null clearing it.
        const input = body as { accommodationNote?: string | null; maxBenefitEstimate?: string | null };
        if ("accommodationNote" in input) {
          if (input.accommodationNote) found.accommodationNote = input.accommodationNote;
          else delete found.accommodationNote;
        }
        if ("maxBenefitEstimate" in input) {
          if (!manages(role))
            return staffProblem(400, {
              title: "Invalid case",
              errors: { maxBenefitEstimate: ["The estimate is staff:manage's"] },
            });
          const derived = new Date(`${found.firstDay}T00:00:00Z`);
          derived.setUTCDate(derived.getUTCDate() + 364);
          found.maxBenefitEstimate = input.maxBenefitEstimate ?? derived.toISOString().slice(0, 10);
          found.maxBenefitEstimateEdited = input.maxBenefitEstimate != null;
        }
        found.revision = (found.revision ?? 0) + 1;
        return jsonResponse(200, found);
      }
    }

    // Settings' absence types and holiday schemes (staff:manage's to write).
    const typeRoute = /^\/api\/v1\/staff\/absence-types(?:\/(\d+))?$/.exec(path);
    if (typeRoute) {
      if (method === "GET") return jsonResponse(200, state.absenceTypes);
      if (!manages(role)) return forbidden();
      if (method === "POST") {
        const input = body as Schemas["StaffAbsenceTypeRequest"];
        const created: Schemas["StaffAbsenceType"] = {
          hidden: false,
          sort: 100,
          ...input,
          id: nextId++,
          seeded: false,
          reserved: false,
          revision: 1,
        };
        state.absenceTypes.push(created);
        return jsonResponse(201, created);
      }
      const type = state.absenceTypes.find((one) => one.id === Number(typeRoute[1]));
      if (!type) return notFound();
      const stale = staleRevision(init, type.revision, "absence type");
      if (stale) return stale;
      const input = body as Schemas["StaffAbsenceTypeChangeRequest"];
      // A seed's pay is HR's to change — but ammefri's and svangerskapskontroll's, paid by law (aml. § 12-8 (2), § 12-1), and a reserved type's.
      const fixed = ["allowsPercent", "drawsHoliday", "needsDecision", "visibility", "ameldingLeave"] as const;
      if (
        type.seeded &&
        (fixed.some((key) => input[key] !== type[key]) ||
          ((type.key === "ammefri" || type.key === "svangerskapskontroll") && !input.paid) ||
          (type.reserved && input.paid !== type.paid))
      )
        return staffProblem(409, {
          code: "seed_immutable",
          title: "Seeded type",
          detail: "A seeded type changes only its names, its order, whether it is hidden and whether it is paid.",
        });
      delete type.ameldingLeave;
      Object.assign(type, input, { revision: type.revision + 1 });
      return jsonResponse(200, type);
    }
    const schemeRoute = /^\/api\/v1\/staff\/holiday-schemes(?:\/(\d+))?$/.exec(path);
    if (schemeRoute) {
      if (method === "GET") return jsonResponse(200, state.holidaySchemes);
      if (!manages(role)) return forbidden();
      const convert = (v: number) => Math.floor(v / 6) * 5 + Math.min(v % 6, 5);
      const filled = (input: Schemas["StaffHolidaySchemeRequest"]) => {
        const values = {
          name: input.name,
          entitlementVirkedager: input.entitlementVirkedager,
          extraFrom60Virkedager: input.extraFrom60Virkedager,
          mainPeriodVirkedager: input.mainPeriodVirkedager ?? 18,
          afterSeptemberVirkedager: input.afterSeptemberVirkedager ?? 6,
          maxCarryOverVirkedager: input.maxCarryOverVirkedager ?? 12,
        };
        return {
          ...values,
          converted: {
            entitlement: convert(values.entitlementVirkedager),
            extraFrom60: convert(values.extraFrom60Virkedager),
            mainPeriod: convert(values.mainPeriodVirkedager),
            afterSeptember: convert(values.afterSeptemberVirkedager),
            maxCarryOver: convert(values.maxCarryOverVirkedager),
          },
        };
      };
      const moveDefault = (to: Schemas["StaffHolidayScheme"]) => {
        for (const other of state.holidaySchemes) if (other !== to && other.isDefault) other.isDefault = false;
        to.isDefault = true;
      };
      if (method === "POST") {
        const input = body as Schemas["StaffHolidaySchemeRequest"];
        const created: Schemas["StaffHolidayScheme"] = {
          ...filled(input),
          id: nextId++,
          isDefault: false,
          seeded: false,
          revision: 1,
        };
        state.holidaySchemes.push(created);
        if (input.isDefault) moveDefault(created);
        return jsonResponse(201, created);
      }
      const scheme = state.holidaySchemes.find((one) => one.id === Number(schemeRoute[1]));
      if (!scheme) return notFound();
      const stale = staleRevision(init, scheme.revision, "holiday scheme");
      if (stale) return stale;
      const input = body as Schemas["StaffHolidaySchemeChangeRequest"];
      if (input.archived && scheme.isDefault)
        return staffProblem(409, {
          code: "holiday_scheme_in_use",
          title: "Holiday scheme in use",
          detail: "The default scheme, or one an employment names, is not archived.",
        });
      Object.assign(scheme, filled(input), { revision: scheme.revision + 1 });
      if (input.archived) scheme.archivedAt = scheme.archivedAt ?? "2026-10-11T08:00:00Z";
      else delete scheme.archivedAt;
      if (input.isDefault) moveDefault(scheme);
      return jsonResponse(200, scheme);
    }
    return undefined;
  };

  /**
   * The groups (Entra delivery D) as the server keeps them: staff:manage's
   * alone, the key's pattern and uniqueness, If-Match on a change,
   * `group_archived` on an archived group's writes, `group_derived` on a
   * derived group's members, `group_synced` on deleting one a provider
   * made, and a provider's section checked against its declaration.
   */
  const groupRoutes = (
    method: string,
    path: string,
    init: RequestInit | undefined,
    body: unknown,
  ): Response | undefined => {
    if (path === "/api/v1/staff/me/groups" && method === "GET") {
      if (server.me === undefined) return meProblem();
      return jsonResponse(200, server.myGroups ?? defaultMyGroups());
    }
    const match = /^\/api\/v1\/staff\/groups(?:\/(\d+)(?:\/(archive|members|owners|traits)(?:\/([^/]+))?)?)?$/.exec(
      path,
    );
    if (!match) return undefined;
    if (!manages(role)) return forbidden();
    const listed = (group: StaffGroup): StaffGroup => {
      const { members, owners, ...rest } = group;
      return { ...rest, memberCount: members?.length ?? 0, ownerCount: owners?.length ?? 0 };
    };
    const whole = (group: StaffGroup): StaffGroup => ({
      ...group,
      memberCount: group.members?.length ?? 0,
      ownerCount: group.owners?.length ?? 0,
    });
    if (!match[1]) {
      if (method === "GET")
        return jsonResponse(200, [...state.groups].sort((a, b) => a.name.localeCompare(b.name)).map(listed));
      if (method === "POST") {
        const input = body as Schemas["StaffGroupRequest"];
        if (!/^[a-z0-9][a-z0-9-]{0,59}$/.test(input.key ?? ""))
          return staffProblem(400, {
            title: "Invalid group",
            errors: { key: ["The key is 1 to 60 lower-case letters, digits or hyphens"] },
          });
        if (state.groups.some((one) => one.key === input.key))
          return staffProblem(400, { title: "Invalid group", errors: { key: ["Another group has this key"] } });
        const group: StaffGroup = {
          id: nextId++,
          key: input.key ?? "",
          name: input.name,
          ...(input.description ? { description: input.description } : {}),
          kind: input.kind,
          ...(input.rule ? { rule: input.rule } : {}),
          memberCount: 0,
          ownerCount: 0,
          providers: [],
          members: [],
          owners: [],
          revision: 1,
          createdAt: `${TODAY}T08:00:00Z`,
          updatedAt: `${TODAY}T08:00:00Z`,
        };
        state.groups.push(group);
        return jsonResponse(201, whole(group));
      }
      return undefined;
    }
    const group = state.groups.find((one) => one.id === Number(match[1]));
    if (!group) return notFound();
    const sub = match[2];
    const archivedRefusal = () =>
      staffProblem(409, {
        code: "group_archived",
        title: "Group archived",
        detail: "The group is archived and can no longer be changed.",
      });
    const peopleOf = (ids: number[]) =>
      ids.flatMap((id) => state.people.filter((one) => one.id === id && !one.erasedAt));
    if (!sub) {
      if (method === "GET") return jsonResponse(200, whole(group));
      if (method === "PUT") {
        if (group.archivedAt) return archivedRefusal();
        const stale = staleRevision(init, group.revision, "group");
        if (stale) return stale;
        const input = body as Schemas["StaffGroupRequest"];
        group.name = input.name;
        group.description = input.description;
        group.kind = input.kind;
        group.rule = input.rule;
        group.revision += 1;
        return jsonResponse(200, whole(group));
      }
      if (method === "DELETE") {
        const synced = (state.groupTraits[group.id] ?? []).some((one) => one.syncedAt || one.externalId || one.enabled);
        if (synced)
          return staffProblem(409, {
            code: "group_synced",
            title: "Group synced",
            detail:
              "A provider has the group in its directory, is being sent it, or is turned on for it; archive it instead.",
          });
        state.groups = state.groups.filter((one) => one !== group);
        return new Response(null, { status: 204 });
      }
    }
    if (sub === "archive" && method === "POST") {
      group.archivedAt = group.archivedAt ?? `${TODAY}T09:00:00Z`;
      return jsonResponse(200, whole(group));
    }
    if (sub === "members" && method === "PUT") {
      if (group.archivedAt) return archivedRefusal();
      if (group.kind === "derived")
        return staffProblem(409, {
          code: "group_derived",
          title: "Group derived",
          detail: "A derived group's members are its rule's.",
        });
      const wanted = peopleOf((body as { personIds: number[] }).personIds);
      group.members = wanted.map(
        (person) =>
          group.members?.find((one) => one.personId === person.id) ?? {
            personId: person.id,
            displayName: person.displayName,
            employeeNumber: person.employeeNumber,
            source: "listed",
            since: TODAY,
          },
      );
      return jsonResponse(200, whole(group));
    }
    if (sub === "owners" && method === "PUT") {
      if (group.archivedAt) return archivedRefusal();
      group.owners = peopleOf((body as { personIds: number[] }).personIds).map((person) => ({
        personId: person.id,
        displayName: person.displayName,
        employeeNumber: person.employeeNumber,
      }));
      return jsonResponse(200, whole(group));
    }
    const sections = state.groupTraits[group.id] ?? [];
    if (sub === "traits" && !match[3] && method === "GET") return jsonResponse(200, { providers: sections });
    if (sub === "traits" && match[3] && method === "PUT") {
      const section = sections.find((one) => one.key === decodeURIComponent(match[3]));
      if (!section) return notFound();
      if (group.archivedAt) return archivedRefusal();
      const input = body as Schemas["StaffGroupTraitsRequest"];
      if (!input.enabled) {
        if (section.enabled) section.enabled = false;
        return jsonResponse(200, section);
      }
      const values = input.values ?? {};
      const errors: Record<string, string[]> = {};
      for (const [key, value] of Object.entries(values)) {
        const trait = section.traits.find((one) => one.key === key);
        if (!trait) errors[key] = ["trait_invalid: not a trait of this provider"];
        else if (trait.type === "choice" && !trait.choices?.some((choice) => choice.value === value))
          errors[key] = ["trait_invalid: not one of the choices"];
        else if (trait.type === "string" && trait.maxLength && String(value).length > trait.maxLength)
          errors[key] = [`trait_invalid: at most ${trait.maxLength} characters`];
      }
      for (const trait of section.traits)
        if (trait.required && !(trait.key in values)) errors[trait.key] = ["trait_required: a value is needed"];
      if (Object.keys(errors).length > 0) return staffProblem(400, { title: "Invalid traits", errors });
      Object.assign(section, { enabled: true, values, state: "pending" as const });
      delete section.lastError;
      delete section.lastErrorField;
      const listedState = group.providers.find((one) => one.key === section.key);
      if (listedState) Object.assign(listedState, { enabled: true, state: "pending" });
      else group.providers.push({ key: section.key, enabled: true, state: "pending" });
      return jsonResponse(200, section);
    }
    return undefined;
  };

  const stub = stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const path = url.pathname;
    const method = init?.method ?? "GET";
    // A document's upload is multipart: its fields are read by the route.
    const body = init?.body && !(init.body instanceof FormData) ? JSON.parse(String(init.body)) : undefined;
    const answered = method === "GET" ? server.read?.(path) : server.write?.(method, path, body);
    if (answered) return Promise.resolve(answered);

    if (path === "/api/v1/staff/meta") {
      if (server.meta instanceof Response) return Promise.resolve(server.meta.clone());
      return Promise.resolve(jsonResponse(200, server.meta ?? metaOf()));
    }

    if (path === "/api/v1/staff/settings") {
      if (!manages(role)) return Promise.resolve(forbidden());
      if (method === "GET") return Promise.resolve(jsonResponse(200, state.settings));
      if (method === "PUT") {
        const refused = staleRevision(init, state.settings.revision, "settings") ?? settingsRefusal(body);
        if (refused) return Promise.resolve(refused);
        // A replace — but the retention periods left out keep their values,
        // and the derived block and the acknowledgements are not the body's.
        const {
          retention,
          thresholdsAcknowledged,
          retentionGraceDays,
          healthRetentionDays,
          createBeforeDays,
          deleteExternalAfterDays,
          erasePregnancyHealthTypes,
          eraseSickCaseDates,
          defaultAccountKind,
          directoryFields,
          competenceReviewIntervalMonths,
          ...kept
        } = state.settings;
        // The self-certification policy and the carry-over left out keep theirs too.
        const {
          selfCertPolicy,
          selfCertDaysPerOccurrence,
          selfCertOccurrencesPerYear,
          selfCertDaysPerYear,
          selfCertAfterMonths,
          autoCarryOver,
          autoCarryOverSince,
        } = kept;
        // The account settings left out keep theirs; a null
        // deleteExternalAfterDays is "never", answered absent.
        const {
          deleteExternalAfterDays: sentDelete,
          directoryFields: sentFields,
          defaultAccountKind: sentKind,
          ...sent
        } = body as StaffSettingsInput;
        const keptDelete = "deleteExternalAfterDays" in (body as object) ? sentDelete : deleteExternalAfterDays;
        state.settings = {
          retentionGraceDays,
          healthRetentionDays,
          createBeforeDays,
          // The two erase settings left out keep theirs too (Task 5b).
          erasePregnancyHealthTypes,
          eraseSickCaseDates,
          // The competence review interval left out keeps its value (Staff phase 2, B).
          competenceReviewIntervalMonths,
          selfCertPolicy,
          selfCertDaysPerOccurrence,
          selfCertOccurrencesPerYear,
          selfCertDaysPerYear,
          selfCertAfterMonths,
          autoCarryOver,
          ...(autoCarryOverSince ? { autoCarryOverSince } : {}),
          employeeNumberPrefix: "",
          ...sent,
          // The kind and the switches left out keep theirs (Entra B); a switch left out keeps its value.
          defaultAccountKind: sentKind ?? defaultAccountKind,
          directoryFields: { ...directoryFields, ...sentFields },
          // The workspace's, answered but never taken by a replace.
          timeZone: state.settings.timeZone,
          ...(typeof keptDelete === "number" ? { deleteExternalAfterDays: keptDelete } : {}),
          retention,
          thresholdsAcknowledged,
          revision: state.settings.revision + 1,
        };
        return Promise.resolve(jsonResponse(200, state.settings));
      }
    }

    const structure =
      /^\/api\/v1\/staff\/(legal-entities|business-units|departments|locations)(?:\/(\d+)(\/archive)?)?$/.exec(path);
    if (structure) {
      // The server's own judgment: the lists are staff:view's and
      // staff:manage's, and anyone else is the uniform 403.
      if (method === "GET" && !readsDirectory(role)) return Promise.resolve(forbidden());
      if (method !== "GET" && !manages(role)) return Promise.resolve(forbidden());
      const table = tables[structure[1] as keyof typeof tables];
      const rows = table.rows() as Row[];
      // A department's default profile is a known one, and an unarchived one
      // unless it is the department's own already (plan reading 26,
      // checkDepartmentEdges): a plain field error otherwise.
      const profileRefusal = (current: number | undefined) => {
        const profileId = (body as { competenceProfileId?: number } | undefined)?.competenceProfileId;
        if (structure[1] !== "departments" || profileId === undefined) return undefined;
        const profile = state.competenceProfiles.find((one) => one.id === profileId);
        const message = !profile
          ? `No competence profile has id ${profileId}`
          : profile.archivedAt && profileId !== current
            ? `The profile ${profile.names.en} is archived; a department does not take an archived profile as its default`
            : undefined;
        return message
          ? staffProblem(400, { title: "Invalid department", errors: { competenceProfileId: [message] } })
          : undefined;
      };
      if (!structure[2]) {
        if (method === "GET") return Promise.resolve(jsonResponse(200, rows));
        if (method === "POST") {
          const refused = profileRefusal(undefined);
          if (refused) return Promise.resolve(refused);
          const { isDefault, ...fields } = body;
          const row: Row = { ...fields, id: nextId++, revision: 1, ...(table.defaults ? { isDefault: false } : {}) };
          rows.push(row);
          if (table.defaults && (isDefault === true || rows.length === 1)) makeDefault(rows, row);
          return Promise.resolve(jsonResponse(201, row));
        }
      }
      const row = rows.find((one) => one.id === Number(structure[2]));
      if (!row) return Promise.resolve(notFound());
      if (structure[3] && method === "POST") {
        if (row.isDefault) return Promise.resolve(structureDefault(table.what));
        row.archivedAt = row.archivedAt ?? "2026-10-11T08:00:00Z";
        row.revision += 1;
        return Promise.resolve(jsonResponse(200, row));
      }
      if (method === "PUT") {
        const stale = staleRevision(init, row.revision, table.what);
        if (stale) return Promise.resolve(stale);
        const refused = profileRefusal((row as { competenceProfileId?: number }).competenceProfileId);
        if (refused) return Promise.resolve(refused);
        const { isDefault, ...fields } = body;
        if (isDefault === false && row.isDefault) return Promise.resolve(structureDefault(table.what));
        // A full replace: every optional field left out is cleared.
        for (const key of Object.keys(row)) {
          if (!["id", "revision", "isDefault", "archivedAt"].includes(key))
            delete (row as Record<string, unknown>)[key];
        }
        Object.assign(row, fields);
        if (isDefault === true) makeDefault(rows, row);
        row.revision += 1;
        return Promise.resolve(jsonResponse(200, row));
      }
    }

    if (path === "/api/v1/staff/people/similar-erased" && method === "POST") {
      if (!manages(role)) return Promise.resolve(forbidden());
      const input = body as { givenName?: string; familyName?: string; birthDate?: string };
      const missing = (["givenName", "familyName", "birthDate"] as const).filter((field) => !input[field]?.trim());
      if (missing.length > 0)
        return Promise.resolve(
          staffProblem(400, {
            title: "Invalid lookup",
            errors: Object.fromEntries(missing.map((field) => [field, [`Give the ${field}`]])),
          }),
        );
      const same = (a: string, b?: string) => a.trim().toLowerCase() === (b ?? "").trim().toLowerCase();
      return Promise.resolve(
        jsonResponse(
          200,
          state.people
            .filter(
              (one) =>
                one.erasedAt &&
                same(one.givenName, input.givenName) &&
                same(one.familyName, input.familyName) &&
                one.record?.birthDate === input.birthDate,
            )
            .map((one) => ({
              id: one.id,
              employeeNumber: one.employeeNumber,
              displayName: one.displayName,
              erasedAt: one.erasedAt,
            })),
        ),
      );
    }

    if (path === "/api/v1/staff/people") {
      if (method === "GET") return Promise.resolve(readsDirectory(role) ? listPeople(url) : forbidden());
      if (method === "POST") {
        if (!manages(role)) return Promise.resolve(forbidden());
        return Promise.resolve(unknownProperties(body, personRequestProperties) ?? createPerson(body));
      }
    }
    const employments = /^\/api\/v1\/staff\/people\/(\d+)\/employments$/.exec(path);
    if (employments && method === "POST")
      return Promise.resolve(manages(role) ? createEmployment(Number(employments[1]), body) : forbidden());

    const groupsAnswer = groupRoutes(method, path, init, body);
    if (groupsAnswer) return Promise.resolve(groupsAnswer);

    const absenceAnswer = absenceRoutes(method, path, url, init, body);
    if (absenceAnswer) return Promise.resolve(absenceAnswer);

    const dataAnswer = dataRoutes(method, path, url);
    if (dataAnswer) return Promise.resolve(dataAnswer);

    const documentsAnswer = documentRoutes(method, path, init, body);
    if (documentsAnswer) return Promise.resolve(documentsAnswer);

    const competenceAnswer = competenceRoutes(method, path, url, init, body);
    if (competenceAnswer) return Promise.resolve(competenceAnswer);

    const wishesAnswer = wishesRoutes(method, path, body);
    if (wishesAnswer) return Promise.resolve(wishesAnswer);

    const profilesAnswer = competenceProfileRoutes(method, path, url, init, body);
    if (profilesAnswer) return Promise.resolve(profilesAnswer);

    if (path === "/api/v1/staff/stats/attention" && method === "GET") return Promise.resolve(attention());

    return Promise.resolve(personRoutes(method, path, url, init, body) ?? notFound());
  }) as StaffStub;
  stub.state = state;
  return stub;
};
