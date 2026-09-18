import type { TimeEntry } from "../api/entries";
import type { MyProject, MyTaskOption, ProjectBillingLine } from "../api/projects";
import type { TimeWeek, TimeWeekRow } from "../api/weeks";
import { weekDays } from "../lib/week";

export const ME = "11111111-1111-1111-1111-111111111111";

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

export const myTasks: MyTaskOption[] = [
  { id: 5001, title: "Skriv spesifikasjonen", projectId: 1001, projectCode: "KVEM1000", projectName: "Kverneland web" },
  { id: 5003, title: "Oversett rapporten", projectId: 1004, projectCode: "EURO2026", projectName: "Euro" },
];
