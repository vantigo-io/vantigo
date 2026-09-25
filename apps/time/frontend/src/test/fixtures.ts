import type { TimeApprovalGroup } from "../api/approvals";
import type { TimeEntry } from "../api/entries";
import type { TimePersonOverview } from "../api/people";
import type { MyProject, MyTaskOption, ProjectBillingLine } from "../api/projects";
import type { AssignableRateUser, PersonRate } from "../api/rates";
import type { TimeProjectSummary } from "../api/stats";
import type { TimeWeek, TimeWeekRow } from "../api/weeks";
import { weekDays } from "../lib/week";

export const ME = "11111111-1111-1111-1111-111111111111";

/** Somebody else, whose time the caller approves and whose rates they manage. */
export const OTHER = "22222222-2222-2222-2222-222222222222";

/** The week every page test looks at: Monday 14 September 2026. */
export const WEEK = "2026-09-14";

export const entry = (overrides: Partial<TimeEntry> = {}): TimeEntry => ({
  id: 501,
  userId: ME,
  userDisplayName: "Ada Lovelace",
  projectId: 1001,
  projectCode: "KVEM1000",
  projectName: "Kverneland web",
  billingLineId: 3001,
  billingLineCode: "PM",
  trackableCode: "KVEM1000-PM",
  taskId: null,
  taskTitle: null,
  entryDate: WEEK,
  hours: 7.5,
  startTime: null,
  endTime: null,
  note: null,
  billable: true,
  rateSource: "line",
  status: "draft",
  rejectionReason: null,
  revision: 2,
  createdAt: "2026-09-14T08:00:00Z",
  updatedAt: "2026-09-14T08:00:00Z",
  capabilities: { canEdit: true, canSubmit: true, canApprove: false, canUnapprove: false },
  ...overrides,
});

type RowRef = Omit<TimeWeekRow, "days">;

/** A week row whose seven days hold the given entries, placed on their own dates. */
export const weekRow = (ref: RowRef, entries: TimeEntry[] = []): TimeWeekRow => ({
  ...ref,
  days: weekDays(WEEK).map((date) => ({ date, entries: entries.filter((e) => e.entryDate === date) })),
});

export const pmRow: RowRef = {
  projectId: 1001,
  projectCode: "KVEM1000",
  projectName: "Kverneland web",
  billingLineId: 3001,
  billingLineCode: "PM",
  trackableCode: "KVEM1000-PM",
  taskId: null,
  taskTitle: null,
};

export const devTaskRow: RowRef = {
  projectId: 1001,
  projectCode: "KVEM1000",
  projectName: "Kverneland web",
  billingLineId: 3002,
  billingLineCode: "DEV",
  trackableCode: "KVEM1000-DEV",
  taskId: 5001,
  taskTitle: "Skriv spesifikasjonen",
};

/** The week response for these rows, with totals summed the way the server sums them. */
export const week = (rows: TimeWeekRow[], overrides: Partial<TimeWeek> = {}): TimeWeek => {
  const perDay = weekDays(WEEK).map((_, i) =>
    rows.reduce((sum, row) => sum + row.days[i].entries.reduce((s, e) => s + e.hours, 0), 0),
  );
  return {
    weekStart: WEEK,
    submittedAt: null,
    hasUnsubmittedChanges: false,
    rows,
    totals: { perDay, week: perDay.reduce((a, b) => a + b, 0) },
    ...overrides,
  };
};

export const myProjects: MyProject[] = [
  { id: 1001, code: "KVEM1000", name: "Kverneland web", status: "active", billingType: "time-and-materials" },
  { id: 1002, code: "INTERN", name: "Internal", status: "active", billingType: "non-billable" },
];

export const kvemLines: ProjectBillingLine[] = [
  { id: 3001, code: "PM", trackableCode: "KVEM1000-PM", productName: "Project management", active: true },
  { id: 3002, code: "DEV", trackableCode: "KVEM1000-DEV", productName: "Development", active: true },
];

/**
 * A work type in full, as the projects API's WorkTypeResponse sends it. Time's
 * generated schema holds only its own module's contract, so the wire shape is
 * spelled out here; the fixtures below are checked against it, the way
 * `kvemLines` is checked against `ProjectBillingLine`.
 */
export interface WorkTypeWire {
  id: number;
  projectId: number;
  name: string;
  billMultiplierPercent: number;
  costMultiplierPercent: number;
  active: boolean;
  createdAt: string;
  updatedAt: string;
}

/**
 * Kverneland's work types, literally as the projects API answers them — a
 * retired one included, which the entry form must not offer.
 */
export const kvemWorkTypes = [
  {
    id: 6001,
    projectId: 1001,
    name: "Overtid 50 %",
    billMultiplierPercent: 150,
    costMultiplierPercent: 140,
    active: true,
    createdAt: "2026-09-01T08:00:00Z",
    updatedAt: "2026-09-01T08:00:00Z",
  },
  {
    id: 6003,
    projectId: 1001,
    name: "Gammel overtid",
    billMultiplierPercent: 150,
    costMultiplierPercent: 150,
    active: false,
    createdAt: "2026-01-01T08:00:00Z",
    updatedAt: "2026-06-01T08:00:00Z",
  },
] satisfies WorkTypeWire[];

export const myTasks: MyTaskOption[] = [
  { id: 5001, title: "Skriv spesifikasjonen", projectId: 1001, projectCode: "KVEM1000", projectName: "Kverneland web" },
  { id: 5003, title: "Oversett rapporten", projectId: 1004, projectCode: "EURO2026", projectName: "Euro" },
];

/** An entry as the approval queue answers one: submitted, and the caller may approve it. */
export const submittedEntry = (overrides: Partial<TimeEntry> = {}): TimeEntry =>
  entry({
    status: "submitted",
    capabilities: { canEdit: false, canSubmit: false, canApprove: true, canUnapprove: false },
    ...overrides,
  });

/** The week before the one every other fixture uses. */
export const LAST_WEEK = "2026-09-07";

/** The queue as two groups, oldest week first, the way the server orders them. */
export const approvalGroups: TimeApprovalGroup[] = [
  {
    userId: OTHER,
    displayName: "Grace Hopper",
    weekStart: LAST_WEEK,
    hours: 4,
    entries: [
      submittedEntry({
        id: 711,
        userId: OTHER,
        userDisplayName: "Grace Hopper",
        entryDate: LAST_WEEK,
        hours: 4,
        note: "Hardware bring-up",
      }),
    ],
  },
  {
    userId: ME,
    displayName: "Ada Lovelace",
    weekStart: WEEK,
    hours: 9.5,
    entries: [
      submittedEntry({
        id: 701,
        entryDate: WEEK,
        hours: 7.5,
        note: "Kickoff",
        billing: { billRate: 1200, currency: "NOK" },
      }),
      submittedEntry({ id: 702, ...devTaskRow, entryDate: "2026-09-15", hours: 2, billable: false }),
    ],
  },
];

export const peopleOverview: TimePersonOverview[] = [
  {
    userId: ME,
    displayName: "Ada Lovelace",
    weeks: [
      { weekStart: LAST_WEEK, hours: 32, approvedHours: 32, rejectedCount: 0, submittedAt: "2026-09-12T14:00:00Z" },
      { weekStart: WEEK, hours: 9.5, approvedHours: 0, rejectedCount: 1, submittedAt: null },
    ],
  },
  {
    userId: OTHER,
    displayName: "Grace Hopper",
    weeks: [
      { weekStart: LAST_WEEK, hours: 4, approvedHours: 0, rejectedCount: 0, submittedAt: "2026-09-11T09:00:00Z" },
      { weekStart: WEEK, hours: 0, approvedHours: 0, rejectedCount: 0, submittedAt: null },
    ],
  },
];

/** What the directory search answers: active users, whether or not they ever logged an hour. */
export const assignableUsers: AssignableRateUser[] = [
  { userId: ME, displayName: "Ada Lovelace" },
  { userId: OTHER, displayName: "Grace Hopper" },
];

export const personRates: PersonRate[] = [
  {
    id: 41,
    userId: ME,
    displayName: "Ada Lovelace",
    validFrom: "2026-01-01",
    billRate: 1100,
    costRate: 700,
    currency: "NOK",
  },
  {
    id: 42,
    userId: ME,
    displayName: "Ada Lovelace",
    validFrom: "2026-07-01",
    billRate: 1200,
    costRate: null,
    currency: "NOK",
  },
  {
    id: 43,
    userId: OTHER,
    displayName: "Grace Hopper",
    validFrom: "2026-03-01",
    billRate: null,
    costRate: 800,
    currency: "NOK",
  },
];

export const projectSummary: TimeProjectSummary = {
  projectId: 1001,
  hours: {
    total: 30.5,
    byStatus: { draft: 2.5, submitted: 8, approved: 20, rejected: 1.5, invoiced: 0 },
    byLine: [
      { billingLineId: 3001, billingLineCode: "PM", trackableCode: "KVEM1000-PM", hours: 10.5 },
      { billingLineId: 3002, billingLineCode: "DEV", trackableCode: "KVEM1000-DEV", hours: 18 },
      { hours: 2 },
    ],
    byPerson: [
      { userId: ME, displayName: "Ada Lovelace", hours: 22.5 },
      { userId: OTHER, displayName: "Grace Hopper", hours: 8 },
    ],
  },
  billing: { amount: 33600, currency: "NOK", unpricedHours: 2 },
};
