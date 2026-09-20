/** An expense's lifecycle (design §4), in the order an expense moves through it. */
export const expenseStatuses = ["draft", "submitted", "approved", "rejected"] as const;

export type ExpenseStatus = (typeof expenseStatuses)[number];

/** Every kind of money line the module records. */
export const expenseKinds = ["outlay", "mileage", "per_diem"] as const;

export type ExpenseKind = (typeof expenseKinds)[number];

/**
 * The kinds an expense of its own can be. A per diem day exists only inside a
 * travel claim, so it is never among "My expenses"' standalone list and is not
 * offered as a filter there — the trip it belongs to is the unit instead.
 */
export const standaloneExpenseKinds = ["outlay", "mileage"] as const;

/** Who is out of pocket for an outlay. Mileage carries none — it is always the employee's. */
export const paidByValues = ["employee", "company"] as const;

export type PaidBy = (typeof paidByValues)[number];

type StatusPresentation = { labelKey: string; color: string };

const presentation: Record<ExpenseStatus, StatusPresentation> = {
  draft: { labelKey: "statusDraft", color: "gray" },
  submitted: { labelKey: "statusSubmitted", color: "blue" },
  approved: { labelKey: "statusApproved", color: "green" },
  rejected: { labelKey: "statusRejected", color: "red" },
};

/** The `expenses` catalog key naming this status. Never colour alone: a badge carries both. */
export const expenseStatusLabelKey = (status: ExpenseStatus): string => presentation[status].labelKey;

export const expenseStatusColor = (status: ExpenseStatus): string => presentation[status].color;

export const isExpenseStatus = (value: unknown): value is ExpenseStatus =>
  typeof value === "string" && (expenseStatuses as readonly string[]).includes(value);

export const isExpenseKind = (value: unknown): value is ExpenseKind =>
  typeof value === "string" && (expenseKinds as readonly string[]).includes(value);

/**
 * Whether the value names a kind an expense of its own can be. `per_diem` is
 * not one: a per diem day exists only inside a travel claim, so it is never a
 * filter on a list of standalone expenses.
 */
export const isStandaloneExpenseKind = (value: unknown): value is (typeof standaloneExpenseKinds)[number] =>
  typeof value === "string" && (standaloneExpenseKinds as readonly string[]).includes(value);

export const isPaidBy = (value: unknown): value is PaidBy =>
  typeof value === "string" && (paidByValues as readonly string[]).includes(value);

/** The `expenses` catalog key naming a kind. */
export const expenseKindLabelKey = (kind: ExpenseKind): string =>
  kind === "mileage" ? "kindMileage" : kind === "per_diem" ? "kindPerDiem" : "kindOutlay";
