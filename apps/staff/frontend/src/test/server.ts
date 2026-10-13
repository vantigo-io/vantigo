import type { StaffImportResult } from "../api/data";
import type { StaffDocument, StaffDocumentCategory } from "../api/documents";
import type {
  StaffContractCheckItem,
  StaffEmployment,
  StaffEmploymentInput,
  StaffTerms,
  StaffTermsInput,
} from "../api/employments";
import type { StaffMeta } from "../api/meta";
import type { StaffAssignableUser, StaffEvent, StaffPerson, StaffPersonInput } from "../api/people";
import type { StaffRetentionRun } from "../api/retention";
import type { StaffSettings, StaffSettingsInput, StaffThreshold } from "../api/settings";
import type { StaffBusinessUnit, StaffDepartment, StaffLegalEntity, StaffLocation } from "../api/structure";
import { defaultFullTimeHours, fullTimeHoursMismatch } from "../lib/terms";
import { jsonResponse } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";
import {
  capabilitiesOf,
  assignableUsers as defaultAssignableUsers,
  businessUnits as defaultBusinessUnits,
  departments as defaultDepartments,
  documentCategories as defaultDocumentCategories,
  documents as defaultDocuments,
  employments as defaultEmployments,
  events as defaultEvents,
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
  pageSize?: number;
  /** Answers a write instead of the fake's own; undefined falls through to it. */
  write?: (method: string, path: string, body: unknown) => Response | undefined;
  /** Answers a read instead of the fake's own, as `write` does a write. */
  read?: (path: string) => Response | undefined;
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

type Group = "work" | "dates" | "employment" | "pay" | "private" | "notes" | "sealed" | "hold" | "careDays";

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
const unknownProperties = (body: unknown, allowed: readonly string[]): Response | undefined => {
  const unknown = Object.keys((body ?? {}) as Record<string, unknown>)
    .filter((key) => !allowed.includes(key))
    .sort();
  if (unknown.length === 0) return undefined;
  return staffProblem(400, {
    title: "Invalid person",
    errors: Object.fromEntries(
      unknown.map((key) => [key, ["This property is not one this request takes; the request was not applied"]]),
    ),
  });
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
  };
  const users = server.users ?? defaultAssignableUsers();
  let nextId = 900;

  const metaOf = (): StaffMeta => {
    const base = defaultMeta(role);
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
      },
    };
    return shape(audience, whole, ["id", "revision", "capabilities", "status"], personGroupOf);
  };

  const findPerson = (id: number) => state.people.find((one) => one.id === id);

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
      if (role === "view") return one.status.state === "active";
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
      if (
        stored.erasedAt &&
        method !== "GET" &&
        sub !== "legal-hold" &&
        sub !== "reassessed" &&
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
          JSON.stringify({ exportedAt: "2026-10-11T08:00:00Z", person: personOf(stored, audience), events: [] }),
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
        const linkOf = () =>
          record.user ? { linked: true, state: "linked", managed: false, user: record.user } : { linked: false };
        if (method === "GET") return jsonResponse(200, linkOf());
        if (method === "PUT") {
          const userId = (body as { userId: string | null }).userId;
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
      if (sub === "org" && method === "GET") return jsonResponse(200, orgOf(stored));
      if (sub === "events" && method === "GET") {
        // §5's Events row: the person, a manager of theirs and HR — a colleague gets the unknown id's 404.
        if (!audience.self && !audience.manager && !audience.hr && !audience.personalData) return notFound();
        const all = (state.events[stored.id] ?? []).map((event) => shapeEvent(audience, event));
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
        const input = body as { categoryId: number; validTo?: string; signedAt?: string };
        const category = state.documentCategories.find((row) => row.id === input.categoryId);
        if (!category || category.archivedAt)
          return staffProblem(400, {
            title: "Invalid document",
            errors: { categoryId: ["Choose a category that is in use"] },
          });
        if (category.restricted && !holdsPersonalData(role)) return restrictedRefusal();
        delete document.validTo;
        delete document.signedAt;
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
    ];
    const threshold = /^\/api\/v1\/staff\/thresholds\/([^/]+)\/acknowledge$/.exec(path);
    if (!known.includes(path) && !threshold) return undefined;
    if (!manages(role)) return forbidden();

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
        const { retention, thresholdsAcknowledged, retentionGraceDays, healthRetentionDays, ...kept } = state.settings;
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
        state.settings = {
          retentionGraceDays,
          healthRetentionDays,
          selfCertPolicy,
          selfCertDaysPerOccurrence,
          selfCertOccurrencesPerYear,
          selfCertDaysPerYear,
          selfCertAfterMonths,
          autoCarryOver,
          ...(autoCarryOverSince ? { autoCarryOverSince } : {}),
          employeeNumberPrefix: "",
          ...(body as StaffSettingsInput),
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
      if (!structure[2]) {
        if (method === "GET") return Promise.resolve(jsonResponse(200, rows));
        if (method === "POST") {
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

    const dataAnswer = dataRoutes(method, path, url);
    if (dataAnswer) return Promise.resolve(dataAnswer);

    const documentsAnswer = documentRoutes(method, path, init, body);
    if (documentsAnswer) return Promise.resolve(documentsAnswer);

    return Promise.resolve(personRoutes(method, path, url, init, body) ?? notFound());
  }) as StaffStub;
  stub.state = state;
  return stub;
};
