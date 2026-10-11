import type { StaffEmployment, StaffEmploymentInput } from "../api/employments";
import type { StaffMeta } from "../api/meta";
import type { StaffPerson, StaffPersonInput } from "../api/people";
import type { StaffSettings } from "../api/settings";
import type { StaffBusinessUnit, StaffDepartment, StaffLegalEntity, StaffLocation } from "../api/structure";
import { defaultFullTimeHours, fullTimeHoursMismatch } from "../lib/terms";
import { jsonResponse } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";
import {
  businessUnits as defaultBusinessUnits,
  departments as defaultDepartments,
  legalEntities as defaultLegalEntities,
  locations as defaultLocations,
  meta as defaultMeta,
  people as defaultPeople,
  settings as defaultSettings,
  type Role,
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
  pageSize?: number;
  /** Answers a write instead of the fake's own; undefined falls through to it. */
  write?: (method: string, path: string, body: unknown) => Response | undefined;
}

/** What the fake holds after the writes, for a test to read back. */
export interface StaffState {
  settings: StaffSettings;
  legalEntities: StaffLegalEntity[];
  businessUnits: StaffBusinessUnit[];
  departments: StaffDepartment[];
  locations: StaffLocation[];
  people: StoredPerson[];
  employments: (StaffEmployment & { terms: StaffEmploymentInput["terms"] })[];
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

/** What the wire carries of a stored person: the unit and the location are the fake's own. */
const listItem = (stored: StoredPerson) => {
  const item: Partial<StoredPerson> = { ...stored };
  delete item.unitId;
  delete item.locationId;
  return item as Omit<StoredPerson, "unitId" | "locationId">;
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
 * rule. **What it does not model**: the archived-reference rule
 * (`structure_archived`), the cycles, the field shaping of a person, and
 * the structure filters across several employments — a test that needs one
 * of those stages it with `write`.
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
    employments: [],
  };
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

  const settingsRefusal = (body: StaffSettings): Response | undefined => {
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

  const personOf = (stored: StoredPerson): StaffPerson => ({
    ...listItem(stored),
    revision: 1,
    capabilities: {
      canEdit: role !== "view",
      canManageAccount: role !== "view",
      canRevealIdentifiers: false,
      canSeePay: role !== "view",
    },
  });

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

    const employmentNumber = body.employmentNumber ?? `${stored.employeeNumber}-1`;
    const employment = {
      id: nextId++,
      personId,
      employmentNumber,
      startDate: body.startDate,
      businessUnitId: body.businessUnitId ?? state.businessUnits.find((one) => one.isDefault)?.id,
      relationshipType: body.relationshipType ?? "ordinary",
      revision: 1,
      terms,
    };
    state.employments.push(employment);
    stored.status = body.startDate > TODAY ? { state: "upcoming", date: body.startDate } : { state: "active" };
    stored.unitId = employment.businessUnitId;
    stored.locationId = terms.locationId ?? undefined;
    const department = state.departments.find((one) => one.id === terms.departmentId);
    stored.department = department ? { id: department.id, code: department.code, name: department.name } : undefined;
    const answered: Partial<typeof employment> = { ...employment };
    delete answered.terms;
    return jsonResponse(201, answered);
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
      .map(listItem);
    return jsonResponse(200, page(rows, Math.max(1, Number(params.get("page")) || 1), pageSize));
  };

  const stub = stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const path = url.pathname;
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    const answered = method === "GET" ? undefined : server.write?.(method, path, body);
    if (answered) return Promise.resolve(answered);

    if (path === "/api/v1/staff/meta") {
      if (server.meta instanceof Response) return Promise.resolve(server.meta.clone());
      return Promise.resolve(jsonResponse(200, server.meta ?? metaOf()));
    }

    if (path === "/api/v1/staff/settings") {
      if (method === "GET") return Promise.resolve(jsonResponse(200, state.settings));
      if (method === "PUT") {
        const refused = staleRevision(init, state.settings.revision, "settings") ?? settingsRefusal(body);
        if (refused) return Promise.resolve(refused);
        state.settings = { ...body, revision: state.settings.revision + 1 };
        return Promise.resolve(jsonResponse(200, state.settings));
      }
    }

    const structure =
      /^\/api\/v1\/staff\/(legal-entities|business-units|departments|locations)(?:\/(\d+)(\/archive)?)?$/.exec(path);
    if (structure) {
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

    if (path === "/api/v1/staff/people") {
      if (method === "GET") return Promise.resolve(listPeople(url));
      if (method === "POST") return Promise.resolve(createPerson(body));
    }
    const personRow = /^\/api\/v1\/staff\/people\/(\d+)$/.exec(path);
    if (personRow && method === "GET") {
      const stored = state.people.find((one) => one.id === Number(personRow[1]));
      return Promise.resolve(stored ? jsonResponse(200, personOf(stored)) : notFound());
    }
    const employments = /^\/api\/v1\/staff\/people\/(\d+)\/employments$/.exec(path);
    if (employments && method === "POST") return Promise.resolve(createEmployment(Number(employments[1]), body));

    return Promise.resolve(notFound());
  }) as StaffStub;
  stub.state = state;
  return stub;
};
