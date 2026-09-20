/** An expense's lifecycle (design §4), in the order an expense moves through it. */
export const expenseStatuses = ["draft", "submitted", "approved", "rejected"] as const;

export type ExpenseStatus = (typeof expenseStatuses)[number];

/** The two kinds of money line this delivery records; `per_diem` arrives with travel claims. */
export const expenseKinds = ["outlay", "mileage"] as const;

export type ExpenseKind = (typeof expenseKinds)[number];

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

export const isPaidBy = (value: unknown): value is PaidBy =>
  typeof value === "string" && (paidByValues as readonly string[]).includes(value);

/** The `expenses` catalog key naming a kind. */
export const expenseKindLabelKey = (kind: ExpenseKind): string => (kind === "mileage" ? "kindMileage" : "kindOutlay");
