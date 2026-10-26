import type { PlanningAllocation } from "../api/allocations";
import type { PlanningPerson } from "../api/people";
import type { PlanningProject } from "../api/projects";
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
