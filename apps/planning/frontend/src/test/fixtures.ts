import type { PlanningAllocation } from "../api/allocations";
import type { PlanningCompetence } from "../api/competences";
import type { PlanningPerson } from "../api/people";
import type { PlanningProject } from "../api/projects";
import type { PlanningCandidate, PlanningRequest } from "../api/requests";
import type { PlanningSettings } from "../api/settings";
import { addDays, addWeeks, mondayOf, today } from "../lib/weeks";

/** The business day the suite runs on, and its week: every fixture is placed relative to them. */
export const TODAY = today();
export const MONDAY = mondayOf(TODAY);
/** The Monday `n` weeks from this one. */
export const week = (n: number) => addWeeks(MONDAY, n);
/** The Sunday ending the week `n` weeks from this one. */
export const weekEnd = (n: number) => addDays(addWeeks(MONDAY, n), 6);

export const engineering = { id: 10, name: "Engineering" };
export const operations = { id: 11, name: "Operations" };

const employed = [{ from: addDays(TODAY, -400), to: addDays(TODAY, 1000) }];

export const people = (): PlanningPerson[] => [
  { id: 1, displayName: "Ada Lovelace", employeeNumber: "E-1", department: engineering, employed },
  { id: 2, displayName: "Grace Hopper", employeeNumber: "E-2", department: engineering, employed },
  { id: 3, displayName: "Linus Torvalds", employeeNumber: "E-3", department: operations, employed },
];

export const projects = (): PlanningProject[] => [
  {
    id: 100,
    code: "P-100",
    name: "Harbour app",
    status: "active",
    customer: { id: 7, name: "Fjord AS" },
    startDate: addDays(TODAY, -60),
    endDate: addDays(TODAY, 300),
  },
  { id: 200, code: "P-200", name: "Tender bid", status: "planned", customer: { id: 8, name: "Bergen Havn" } },
  { id: 300, code: "P-300", name: "Old rollout", status: "completed", customer: { id: 7, name: "Fjord AS" } },
];

const stamp = "2026-10-01T08:00:00Z";

/** A row as the server answers it, the person and the project named from the fixtures. */
export const allocation = (
  fields: Partial<PlanningAllocation> & { id: number; personId: number; projectId: number },
): PlanningAllocation => {
  const { personId, projectId, ...rest } = fields;
  const person = people().find((one) => one.id === personId);
  const project = projects().find((one) => one.id === projectId);
  return {
    fromDate: week(0),
    toDate: weekEnd(11),
    load: 50,
    loadUnit: "percent",
    status: "confirmed",
    revision: 1,
    createdAt: stamp,
    updatedAt: stamp,
    confirmedAt: stamp,
    person: person
      ? { id: person.id, displayName: person.displayName, employeeNumber: person.employeeNumber }
      : { id: personId, displayName: null },
    project: project
      ? { id: project.id, code: project.code, name: project.name, status: project.status }
      : { id: projectId },
    ...rest,
  };
};

/**
 * Ada at half time on the harbour app for twelve weeks, confirmed; Grace
 * tentatively on the tender bid (a planned project) for four; Linus twenty
 * hours a week on the harbour app for six.
 */
export const allocations = (): PlanningAllocation[] => [
  allocation({ id: 501, personId: 1, projectId: 100, title: "Frontend developer", note: "Wants the native part" }),
  allocation({
    id: 502,
    personId: 2,
    projectId: 200,
    load: 40,
    status: "tentative",
    confirmedAt: undefined,
    toDate: weekEnd(3),
  }),
  allocation({ id: 503, personId: 3, projectId: 100, load: 20, loadUnit: "hours_per_week", toDate: weekEnd(5) }),
];

export const settings = (fields: Partial<PlanningSettings> = {}): PlanningSettings => ({
  overbooking: "warn",
  defaultLoadUnit: "percent",
  unbookedThresholdPercent: 50,
  unbookedHorizonWeeks: 6,
  requestLeadDays: 14,
  endingLeadWeeks: 4,
  revision: 1,
  updatedAt: stamp,
  ...fields,
});

/** Staff's catalogue as Planning answers it: a skill with levels, a certificate, and a hidden skill. */
export const competences = (): PlanningCompetence[] => [
  {
    id: 40,
    key: "react",
    nameEn: "React",
    nameNb: "React",
    categoryKey: "frontend",
    kind: "skill",
    hasLevel: true,
    hidden: false,
  },
  {
    id: 41,
    key: "safety-card",
    nameEn: "Safety card",
    nameNb: "Sikkerhetskort",
    categoryKey: "site",
    kind: "certificate",
    hasLevel: false,
    hidden: false,
  },
  {
    id: 42,
    key: "cobol",
    nameEn: "COBOL",
    nameNb: "COBOL",
    categoryKey: "backend",
    kind: "skill",
    hasLevel: true,
    hidden: true,
  },
];

/** A request as the server answers it, the project named from the fixtures. */
export const request = (
  fields: Partial<PlanningRequest> & { id: number; projectId: number; title: string },
): PlanningRequest => {
  const { projectId, ...rest } = fields;
  const project = projects().find((one) => one.id === projectId);
  return {
    fromDate: week(0),
    toDate: weekEnd(7),
    hoursPerWeek: 30,
    status: "open",
    requirements: [],
    revision: 1,
    createdAt: stamp,
    updatedAt: stamp,
    project: project
      ? { id: project.id, code: project.code, name: project.name, status: project.status }
      : { id: projectId },
    ...rest,
  };
};

/**
 * An open request on the tender bid (a planned project) for a backend
 * developer knowing React at least independently, from Wednesday this week
 * for eight weeks; a filled tester on the harbour app; a withdrawn designer.
 */
export const requests = (): PlanningRequest[] => [
  request({
    id: 801,
    projectId: 200,
    title: "Backend developer",
    fromDate: addDays(MONDAY, 2),
    note: "Bergen office preferred",
    requirements: [
      { competenceId: 40, key: "react", nameEn: "React", nameNb: "React", kind: "skill", hasLevel: true, minLevel: 2 },
      {
        competenceId: 41,
        key: "safety-card",
        nameEn: "Safety card",
        nameNb: "Sikkerhetskort",
        kind: "certificate",
        hasLevel: false,
      },
    ],
  }),
  request({
    id: 802,
    projectId: 100,
    title: "Tester",
    status: "filled",
    fromDate: week(2),
    toDate: weekEnd(5),
    hoursPerWeek: 20,
    filledBy: { allocationId: 504, person: { id: 2, displayName: "Grace Hopper", employeeNumber: "E-2" } },
  }),
  request({ id: 803, projectId: 100, title: "Designer", status: "withdrawn", hoursPerWeek: 10 }),
];

/**
 * The candidates for request 801, as the server sorts them: Ada has the time
 * and covers it; Linus is partly free with a certificate expiring and his
 * wishes lowering the fit; Grace misses React (listed with includePartial).
 */
export const candidates = (): PlanningCandidate[] => {
  const [ada, grace, linus] = people();
  return [
    {
      person: ada,
      availability: "fits",
      freeHours: 260,
      requiredHours: 222,
      fit: { score: 100, lowered: [] },
      flags: [],
      missing: [],
      covers: true,
    },
    {
      person: linus,
      availability: "partial",
      freeHours: 120,
      requiredHours: 222,
      fit: { score: 70, lowered: ["weekdays", "location"] },
      flags: [{ code: "certificate_expires", competenceKey: "safety-card", date: addDays(MONDAY, 30) }],
      missing: [],
      covers: false,
      currentProject: {
        project: { id: 100, code: "P-100", name: "Harbour app", status: "active" },
        since: addDays(TODAY, -90),
        days: 90,
      },
    },
    {
      person: grace,
      availability: "fits",
      freeHours: 300,
      requiredHours: 222,
      fit: { score: 90, lowered: ["remote"] },
      flags: [],
      missing: ["react"],
      covers: false,
    },
  ];
};
