import {
  IconBolt,
  IconBriefcase,
  IconCalendar,
  IconClock,
  IconFileInvoice,
  IconMessage,
  IconPackage,
  IconReceipt2,
  IconUsers,
  IconUsersGroup,
} from "@tabler/icons-react";
import type { DailyPoint, InvoicesSummary, StaffStats } from "../api/dashboard";
import { hasPermissions, type ModuleKey } from "../navigation";

export const moduleCards = [
  {
    title: "dashboard.customers",
    description: "dashboard.manageCustomers",
    path: "/customers",
    icon: IconUsers,
    module: "customers" as ModuleKey,
    requiredPermissions: ["customers:view"],
  },
  {
    title: "dashboard.communications",
    description: "dashboard.reviewMessages",
    path: "/communications/inbox",
    icon: IconMessage,
    module: "communications" as ModuleKey,
    requiredPermissions: ["communications:conversations-view"],
  },
  {
    title: "dashboard.products",
    description: "dashboard.manageProducts",
    path: "/products",
    icon: IconPackage,
    module: "products" as ModuleKey,
    requiredPermissions: [
      "products:products-view",
      "products:variants-view",
      "products:pricing-view",
      "products:categories-view",
      "products:tax-categories-view",
    ],
  },
  {
    title: "dashboard.energy",
    description: "dashboard.manageEnergy",
    path: "/energy/metering-points",
    icon: IconBolt,
    module: "energy" as ModuleKey,
    requiredPermissions: ["energy:metering-points-view", "energy:meters-view"],
  },
  {
    title: "dashboard.projects",
    description: "dashboard.manageProjects",
    path: "/projects",
    icon: IconBriefcase,
    module: "projects" as ModuleKey,
    requiredPermissions: ["projects:access"],
  },
  {
    title: "dashboard.time",
    description: "dashboard.manageTime",
    path: "/time",
    icon: IconClock,
    module: "time" as ModuleKey,
    requiredPermissions: ["time:access"],
  },
  {
    title: "dashboard.calendar",
    description: "dashboard.manageCalendar",
    path: "/calendar",
    icon: IconCalendar,
    module: "calendar" as ModuleKey,
    requiredPermissions: ["calendar:access"],
  },
  {
    title: "dashboard.expenses",
    description: "dashboard.manageExpenses",
    path: "/expenses",
    icon: IconReceipt2,
    module: "expenses" as ModuleKey,
    requiredPermissions: ["expenses:access"],
  },
  {
    title: "dashboard.invoices",
    description: "dashboard.manageInvoices",
    path: "/invoices",
    icon: IconFileInvoice,
    module: "invoices" as ModuleKey,
    requiredPermissions: ["invoices:access"],
  },
  {
    title: "dashboard.staff",
    description: "dashboard.manageStaff",
    path: "/staff",
    icon: IconUsersGroup,
    module: "staff" as ModuleKey,
    requiredPermissions: ["staff:access"],
  },
] as const;

/** The module cards the caller may see: module enabled for the tenant and permission granted. */
export const visibleModuleCards = (enabledModules: readonly ModuleKey[], permissions: string[] | undefined) =>
  moduleCards.filter(
    (card) => enabledModules.includes(card.module) && hasPermissions(permissions, card.requiredPermissions),
  );

/**
 * Whether the caller may read Staff's figures: `GET /staff/stats` answers
 * `staff:view` or `staff:manage` and the uniform 403 to anyone else (the
 * pre-flight's answer 6), so a caller with `staff:access` alone keeps the
 * module card below and has no Staff card in the KPI row.
 */
export const canReadStaffStats = (permissions: string[] | undefined) =>
  hasPermissions(permissions, ["staff:view", "staff:manage"]);

/**
 * The cards of the KPI row: every visible module card, Staff's for those who
 * may read its figures. Calendar has none: it serves no stats endpoints, and
 * its place on the page is the agenda above the row (calendar design D7).
 */
export const kpiCards = <Card extends { module: ModuleKey }>(
  cards: readonly Card[],
  permissions: string[] | undefined,
) => cards.filter((card) => card.module !== "calendar" && (card.module !== "staff" || canReadStaffStats(permissions)));

/** The activity chart's series. Invoices has none: it serves no timeseries (payments and delivery design D7). */
export const metrics = [
  { module: "customers" as ModuleKey, metric: "newCustomers", color: "blue.6", label: "dashboard.newCustomers" },
  {
    module: "communications" as ModuleKey,
    metric: "newConversations",
    color: "violet.6",
    label: "dashboard.newConversations",
  },
  { module: "products" as ModuleKey, metric: "newProducts", color: "orange.6", label: "dashboard.newProducts" },
  { module: "energy" as ModuleKey, metric: "consumptionKwh", color: "teal.6", label: "dashboard.consumption" },
  { module: "projects" as ModuleKey, metric: "newProjects", color: "grape.6", label: "dashboard.newProjects" },
  { module: "time" as ModuleKey, metric: "hours", color: "cyan.6", label: "dashboard.hoursLogged" },
  { module: "expenses" as ModuleKey, metric: "netAmount", color: "pink.6", label: "dashboard.netExpenses" },
] as const;

/** Staff's attention types that are about one person, whose id is the entity id (design S20). */
const staffPersonAttentionTypes = [
  "contractOverdue",
  "probationEnding",
  "temporaryNoticeDue",
  "temporaryConverts",
  "reportsWithoutManager",
  "reassessmentDue",
  // Delivery B's absence items about a person.
  "sickMilestoneDue",
  "holidayUntaken",
  "mainPeriodShort",
  "selfApproved",
  "holidayAffectedBySickness",
];

/**
 * Staff's account items (delivery C): one per person whose account flag is on,
 * entityId the person's id, dealt with on the person's Account tab.
 */
const staffAccountAttentionTypes = [
  "accountNoEmployment",
  "accountEmailMissing",
  "accountNotFound",
  "accountNotManaged",
  "accountBlockedByOwner",
  "accountDisabledByOwner",
  "accountUserGone",
  "accountPrivileged",
  "accountMismatch",
  "accountModeChanged",
  "accountEmailDiffers",
  "accountEmailTaken",
  "accountNumberTaken",
  "accountFailing",
  "invitationUnanswered",
  "invitationRevoked",
  "erasureIncomplete",
  // The provider link's (Entra delivery B), read from the person's provider link.
  "accountNotArrived",
  "accountAmbiguousInEntra",
  "accountAddressTakenInEntra",
  "accountSyncedOnPremises",
  "accountPrivilegedInEntra",
  "hireDateNotWritten",
  "licenceUnavailable",
  "passUnrevealed",
  "managerNotInEntra",
];

/** Staff's installation items: entityId the literal 'settings', dealt with in Settings. */
const staffSettingsAttentionTypes = [
  "retentionFailing",
  "otpThreshold",
  "headcountThreshold",
  "employeeRoleMissing",
  "employeeRoleUnsafe",
  "accountModeMismatch",
  // The identity provider's (Entra delivery B), counted over the people of its kind.
  "entraNotConnected",
  "entraFailing",
  "secretExpiring",
  "secretExpired",
];

/**
 * Staff's group items (Entra delivery D): one per group a provider could not
 * sync, entityId the group's id, dealt with on the group's page.
 */
const staffGroupAttentionTypes = ["groupSyncFailed"];

/** Staff's attention types about the requests waiting a decision: the approvals inbox's (delivery B). */
const staffApprovalAttentionTypes = ["absenceAwaitingDecision", "hrOnlyAwaitingDecision"];

/** The four project-economy attention types, which all link to the Economy tab. */
const projectEconomyAttentionTypes = ["budgetWarning", "budgetExceeded", "milestoneReady", "milestoneOverdue"];

/**
 * Where an attention item leads. Every module agrees on `type` and
 * `entityId`, so this is the one place that turns the pair into a URL.
 * Time's two ids were decided with its stats contract: an unsubmitted week
 * carries its Monday, and a waiting approval carries the queue's group key
 * `<userId>/<Monday>`, which is not addressable — the queue is.
 */
export const attentionHref = (item: { module: ModuleKey; type: string; entityId: string }) => {
  if (item.module === "communications" && item.type === "conversationNoReply") {
    return `/communications/inbox?conversationId=${encodeURIComponent(item.entityId)}`;
  }
  // Customers' six types all carry a customer id, follow-ups included: a
  // follow-up lives on a timeline entry, and the entry lives on the customer's
  // page, which is the only page that can show it.
  if (item.module === "customers") return `/customers/${encodeURIComponent(item.entityId)}`;
  if (item.module === "products") return `/products/${encodeURIComponent(item.entityId)}`;
  if (item.module === "energy") return `/energy/metering-points/${encodeURIComponent(item.entityId)}`;
  if (item.module === "projects") {
    // The two budget types carry a bare project id, same as projectOverdue;
    // the two milestone types carry `<projectId>/<milestoneId>`, which is the
    // milestone's own id, not a URL — encoding the pair whole (as the bare
    // project case below does) would turn the slash into %2F and break the
    // link, so the project id is taken as the part before it. A malformed id
    // (empty, or with no project part) must not reach the router as
    // `/projects//economy`; the app's own list is the safest fallback for
    // anything this build cannot address precisely.
    if (projectEconomyAttentionTypes.includes(item.type)) {
      const projectId = item.entityId.split("/")[0];
      return projectId ? `/projects/${encodeURIComponent(projectId)}/economy` : "/projects";
    }
    return `/projects/${encodeURIComponent(item.entityId)}`;
  }
  if (item.module === "time") {
    if (item.type === "weekUnsubmitted") return `/time?week=${encodeURIComponent(item.entityId)}`;
    if (item.type === "approvalWaiting") return "/time/approvals";
    // A type this build does not know: the app's home is the one page that is
    // right for any of them, and is certainly not the approval queue.
    return "/time";
  }
  if (item.module === "expenses") {
    if (item.type === "approvalWaiting") return "/expenses/approvals";
    if (item.type === "expenseRejected") {
      // A rejected unit is either a standalone expense, whose entity id is a
      // bare number, or a whole travel claim, whose id is `claim/<id>` —
      // the two units number independently, so a bare id would collide. The
      // link keys on the prefix rather than on parsing the number, and a
      // prefixed id with nothing after it falls back to the list.
      const claimId = item.entityId.startsWith("claim/") ? item.entityId.slice("claim/".length) : undefined;
      if (claimId !== undefined) {
        return claimId ? `/expenses/claims/${encodeURIComponent(claimId)}` : "/expenses?status=rejected";
      }
      return "/expenses?status=rejected";
    }
    if (item.type === "reimbursementWaiting") return "/expenses/reimbursements";
    // A type this build does not know: My expenses is the one page that is
    // right for any of them.
    return "/expenses";
  }
  if (item.module === "invoices") {
    // Three types carry an invoice id, and so do the Vipps payments' four —
    // the attempt's invoice, whose Vipps payments card shows it; a file's
    // open lines its bank file; an unposted batch is printed and posted on
    // the paper letters' page. Held letters carry their cause, and the
    // overdue list says what each needs.
    if (
      [
        "invoiceOverdue",
        "invoiceRefundDue",
        "reminderFailed",
        "paymentCaptureUnknown",
        "paymentCaptureFailed",
        "paymentReservationStranded",
        "vippsRefundSeen",
      ].includes(item.type)
    ) {
      return `/invoices/${encodeURIComponent(item.entityId)}`;
    }
    if (item.type === "bankTransactionsOpen") return `/invoices/payments/files/${encodeURIComponent(item.entityId)}`;
    if (item.type === "remindersHeld") return "/invoices/overdue";
    if (item.type === "reminderBatchUnposted") return "/invoices/reminders/print";
    return "/invoices";
  }
  if (item.module === "staff") {
    // The types about a person carry their id and open their page — an
    // account item its Account tab, the sole personal-data holder's own
    // HR-only approval its Absence tab; the requests waiting a decision open
    // the Absence page's approvals; the deletion ledger, the thresholds and
    // the accounts' installation items are dealt with in Settings. An
    // adoption nobody confirmed is confirmed on the Microsoft Entra ID page.
    if (item.type === "accountUnconfirmedInEntra") return "/entra";
    const person = `/staff/people/${encodeURIComponent(item.entityId)}`;
    if (staffPersonAttentionTypes.includes(item.type)) return person;
    if (staffAccountAttentionTypes.includes(item.type)) return `${person}?tab=account`;
    if (item.type === "hrOnlySelfApproved") return `${person}?tab=absence`;
    if (staffApprovalAttentionTypes.includes(item.type)) return "/staff/absence";
    if (staffSettingsAttentionTypes.includes(item.type)) return "/staff/settings";
    if (staffGroupAttentionTypes.includes(item.type)) return `/staff/groups/${encodeURIComponent(item.entityId)}`;
    return "/staff";
  }
  return "/communications/inbox";
};

/**
 * Invoices' sentences (invoices payments and reminders design D12): the
 * server's title is a buyer's name, a file's booking days, a cause or a
 * posting day, never a sentence, so each type is worded here.
 */
const invoicesAttentionTitleKeys: Record<string, string> = {
  invoiceOverdue: "dashboard.invoiceOverdue",
  invoiceRefundDue: "dashboard.invoiceRefundDue",
  reminderFailed: "dashboard.invoiceReminderFailed",
  reminderBatchUnposted: "dashboard.invoiceBatchUnposted",
  paymentCaptureUnknown: "dashboard.invoicePaymentCaptureUnknown",
  paymentCaptureFailed: "dashboard.invoicePaymentCaptureFailed",
  paymentReservationStranded: "dashboard.invoicePaymentReservationStranded",
  vippsRefundSeen: "dashboard.invoiceVippsRefundSeen",
};

const invoicesHeldTitleKeys: Record<string, string> = {
  collectionRatesOutdated: "dashboard.invoiceRemindersHeldRates",
  collectionRegimeUnreviewed: "dashboard.invoiceRemindersHeldRegime",
};

const projectAttentionTitleKeys: Record<string, string> = {
  budgetWarning: "dashboard.projectBudgetWarning",
  budgetExceeded: "dashboard.projectBudgetExceeded",
  milestoneReady: "dashboard.projectMilestoneReady",
  milestoneOverdue: "dashboard.projectMilestoneOverdue",
};

/**
 * Staff's sentences (design S20): the server's title is a person's name, or
 * an English sentence for the ledger and the thresholds, so every type is
 * worded here — a headcount threshold by the threshold its entity id names.
 */
const staffAttentionTitleKeys: Record<string, string> = {
  contractOverdue: "dashboard.staffContractOverdue",
  probationEnding: "dashboard.staffProbationEnding",
  temporaryNoticeDue: "dashboard.staffTemporaryNoticeDue",
  temporaryConverts: "dashboard.staffTemporaryConverts",
  reportsWithoutManager: "dashboard.staffReportsWithoutManager",
  reassessmentDue: "dashboard.staffReassessmentDue",
  retentionFailing: "dashboard.staffRetentionFailing",
  otpThreshold: "dashboard.staffOtpThreshold",
  sickMilestoneDue: "dashboard.staffSickMilestoneDue",
  holidayUntaken: "dashboard.staffHolidayUntaken",
  mainPeriodShort: "dashboard.staffMainPeriodShort",
  selfApproved: "dashboard.staffSelfApproved",
  holidayAffectedBySickness: "dashboard.staffHolidayAffectedBySickness",
  absenceAwaitingDecision: "dashboard.staffAbsenceAwaitingDecision",
  hrOnlyAwaitingDecision: "dashboard.staffHrOnlyAwaitingDecision",
  hrOnlySelfApproved: "dashboard.staffHrOnlySelfApproved",
  accountNoEmployment: "dashboard.staffAccountNoEmployment",
  accountEmailMissing: "dashboard.staffAccountEmailMissing",
  accountNotFound: "dashboard.staffAccountNotFound",
  accountNotManaged: "dashboard.staffAccountNotManaged",
  accountBlockedByOwner: "dashboard.staffAccountBlockedByOwner",
  accountDisabledByOwner: "dashboard.staffAccountDisabledByOwner",
  accountUserGone: "dashboard.staffAccountUserGone",
  accountPrivileged: "dashboard.staffAccountPrivileged",
  accountMismatch: "dashboard.staffAccountMismatch",
  accountModeChanged: "dashboard.staffAccountModeChanged",
  accountEmailDiffers: "dashboard.staffAccountEmailDiffers",
  accountEmailTaken: "dashboard.staffAccountEmailTaken",
  accountNumberTaken: "dashboard.staffAccountNumberTaken",
  accountFailing: "dashboard.staffAccountFailing",
  invitationUnanswered: "dashboard.staffInvitationUnanswered",
  invitationRevoked: "dashboard.staffInvitationRevoked",
  erasureIncomplete: "dashboard.staffErasureIncomplete",
  employeeRoleMissing: "dashboard.staffEmployeeRoleMissing",
  employeeRoleUnsafe: "dashboard.staffEmployeeRoleUnsafe",
  accountModeMismatch: "dashboard.staffAccountModeMismatch",
  accountNotArrived: "dashboard.staffAccountNotArrived",
  accountAmbiguousInEntra: "dashboard.staffAccountAmbiguousInEntra",
  accountAddressTakenInEntra: "dashboard.staffAccountAddressTakenInEntra",
  accountUnconfirmedInEntra: "dashboard.staffAccountUnconfirmedInEntra",
  accountSyncedOnPremises: "dashboard.staffAccountSyncedOnPremises",
  accountPrivilegedInEntra: "dashboard.staffAccountPrivilegedInEntra",
  hireDateNotWritten: "dashboard.staffHireDateNotWritten",
  licenceUnavailable: "dashboard.staffLicenceUnavailable",
  passUnrevealed: "dashboard.staffPassUnrevealed",
  managerNotInEntra: "dashboard.staffManagerNotInEntra",
  entraNotConnected: "dashboard.staffEntraNotConnected",
  entraFailing: "dashboard.staffEntraFailing",
  secretExpiring: "dashboard.staffSecretExpiring",
  secretExpired: "dashboard.staffSecretExpired",
  groupSyncFailed: "dashboard.staffGroupSyncFailed",
};

/**
 * The title Staff's server gives an account item about an erased record, in
 * place of a name: `erasedPersonTitle` in
 * apps/server/internal/staff/accounts_attention.go. An interim match on the
 * English words, until the item carries a marker of its own.
 */
export const staffErasedPersonTitle = "An erased person";

const staffHeadcountThresholdKeys: Record<string, string> = {
  "5": "dashboard.staffHeadcountThreshold5",
  "30": "dashboard.staffHeadcountThreshold30",
  "50": "dashboard.staffHeadcountThreshold50",
};

// Six now: the four registry signals (Brreg in full design D4) and the two
// follow-up ones (follow-ups design D2). All six are server-built titles
// carrying the customer's own name, so all six take the same `{{name}}`
// treatment in attentionTitle below.
const customerAttentionTitleKeys: Record<string, string> = {
  registryBankrupt: "dashboard.customerRegistryBankrupt",
  registryLiquidation: "dashboard.customerRegistryLiquidation",
  registryDeleted: "dashboard.customerRegistryDeleted",
  registryRenamed: "dashboard.customerRegistryRenamed",
  followUpOverdue: "dashboard.customerFollowUpOverdue",
  followUpDue: "dashboard.customerFollowUpDue",
};

/**
 * The catalog key that names an item, or undefined for an item whose own
 * `title` the server already wrote. Time's titles are built from data rather
 * than from a catalog, so they arrive in English; naming them again here is
 * what puts them in the reader's language. The four project-economy types and
 * the six customers types (four registry signals plus the two follow-up ones)
 * are server-built too (a project's, a milestone's or a customer's own name),
 * so they take the same treatment,
 * this time with the name filled into the sentence rather than a date.
 */
export const attentionTitleKey = (item: {
  module: ModuleKey;
  type: string;
  count?: number;
  entityId?: string;
  title?: string;
}) => {
  if (item.module === "time") {
    if (item.type === "weekUnsubmitted") return "dashboard.timeWeekUnsubmitted";
    if (item.type === "approvalWaiting") return "dashboard.timeApprovalWaiting";
    return undefined;
  }
  if (item.module === "projects") return projectAttentionTitleKeys[item.type];
  if (item.module === "customers") return customerAttentionTitleKeys[item.type];
  if (item.module === "invoices") {
    // A file with no booked line has no booking days to name.
    if (item.type === "bankTransactionsOpen") {
      return item.entityId !== undefined && item.title === ""
        ? "dashboard.invoiceBankLinesOpenUndated"
        : "dashboard.invoiceBankLinesOpen";
    }
    if (item.type === "remindersHeld") return item.entityId ? invoicesHeldTitleKeys[item.entityId] : undefined;
    return invoicesAttentionTitleKeys[item.type];
  }
  if (item.module === "expenses") {
    // A rejected unit is an expense or a whole trip, and the two are called
    // different things. The link already keys on the `claim/` prefix; so does
    // the sentence, rather than calling a trip "your expense".
    if (item.type === "expenseRejected") {
      return item.entityId?.startsWith("claim/") ? "dashboard.claimRejected" : "dashboard.expenseRejected";
    }
    // The count is the server's, sent because a title it builds cannot be
    // localised with a number baked in; when it is present the sentence
    // names it, and when it is not (a caller with exactly one waiting) the
    // shorter form still reads naturally.
    if (item.type === "approvalWaiting") {
      return item.count ? "dashboard.expenseApprovalWaitingCount" : "dashboard.expenseApprovalWaiting";
    }
    if (item.type === "reimbursementWaiting") return "dashboard.expenseReimbursementWaiting";
    return undefined;
  }
  if (item.module === "staff") {
    if (item.type === "headcountThreshold")
      return item.entityId ? staffHeadcountThresholdKeys[item.entityId] : undefined;
    if (item.type === "accountFailing" && item.title === staffErasedPersonTitle)
      return "dashboard.staffAccountFailingErased";
    return staffAttentionTitleKeys[item.type];
  }
  return undefined;
};

/** The Monday a time item is about: alone in the id, or after the user id. */
export const attentionWeek = (entityId: string) => entityId.slice(entityId.indexOf("/") + 1);

/**
 * What an attention item is called: the module's own title, or — for time,
 * whose titles the server builds from data and therefore in English — the
 * catalog's, naming the week the item is about.
 *
 * The week is a plain calendar date, so it is formatted **in UTC**: west of
 * Greenwich a local rendering of `2026-08-31T00:00Z` is the 30th, and the
 * title would then name a different week from the one the link opens.
 */
export const attentionTitle = (
  item: { module: ModuleKey; type: string; entityId: string; title: string; count?: number },
  t: (key: string, values?: Record<string, unknown>) => string,
  formatDate: (value: string, options: Intl.DateTimeFormatOptions) => string,
) => {
  const key = attentionTitleKey(item);
  if (!key) return item.title;
  // Projects', expenses' and customers' titles are all server-built from
  // data (a project's, a milestone's, an expense's or a customer's own
  // name, or nothing at all for the payroll item), not from a catalog, so
  // each names the item and — for expenses — the count the server sent, if
  // any. Time's is the odd one out: its entityId carries the week the title
  // is about, not a name.
  if (
    item.module === "projects" ||
    item.module === "expenses" ||
    item.module === "customers" ||
    item.module === "staff"
  ) {
    return t(key, { name: item.title, count: item.count });
  }
  if (item.module === "invoices") {
    // The title is a buyer's name, or a day or two — a file's first and last
    // booking day, a batch's posting day — written as dates (in UTC: they
    // are calendar days); the count is the server's. When it sends none, an
    // unposted batch has no printed letter left in it, and any other item
    // stands for one.
    const asDate = (value: string) => formatDate(value, { dateStyle: "medium", timeZone: "UTC" });
    const days = /^\d{4}-\d{2}-\d{2}( – \d{4}-\d{2}-\d{2})?$/.test(item.title)
      ? item.title.split(" – ").map(asDate).join(" – ")
      : "";
    return t(key, {
      name: item.title,
      days,
      date: days,
      count: item.count ?? (item.type === "reminderBatchUnposted" ? 0 : 1),
    });
  }
  return t(key, { date: formatDate(attentionWeek(item.entityId), { dateStyle: "medium", timeZone: "UTC" }) });
};

/**
 * The Projects card's "N milestones ready to invoice" hint, or nothing when
 * there is nothing ready. It is a state, not a delta (milestones may be in
 * several currencies, so there is no one comparable total), and zero is the
 * ordinary case for most projects, so a standing zero would be a permanent
 * fixture rather than something worth reading — the same reasoning as Time's
 * approval hint.
 */
export const readyMilestonesHint = (
  count: number | undefined,
  t: (key: string, values?: Record<string, unknown>) => string,
): string | undefined => (count ? t("dashboard.readyMilestonesHint", { count }) : undefined);

/**
 * The Projects card's hint, in full: the ready-milestones hint when there is
 * something ready — the more actionable of the two figures — and otherwise
 * exactly the "N new projects" hint the card showed before this feature, so
 * the card never goes from always saying something to saying nothing.
 */
export const projectsCardHint = (
  readyMilestones: number | undefined,
  newProjects: number | undefined,
  t: (key: string, values?: Record<string, unknown>) => string,
): string => readyMilestonesHint(readyMilestones, t) ?? t("dashboard.newProjectsHint", { count: newProjects ?? 0 });

/**
 * The Time card's "N waiting for your approval" hint, or nothing when there
 * is nothing to approve. Zero is the ordinary case for most `time:access`
 * holders — they approve nobody's hours — so a standing zero would be a
 * permanent fixture rather than something worth reading.
 */
export const awaitingApprovalHint = (
  count: number | undefined,
  t: (key: string, values?: Record<string, unknown>) => string,
): string | undefined => (count ? t("dashboard.awaitingApprovalHint", { count }) : undefined);

/**
 * The Expenses card's primary figure: the first currency the caller is owed
 * in, with how many more there are when they are owed in several — nothing is
 * ever summed across currencies (design §4) — and a plain "nothing owed" for
 * an empty list, or for no list at all. The caller shows "—" while the summary
 * is still loading, the way every other card does, and only asks this once
 * there is an answer.
 */
export const expensesUnreimbursedValue = (
  totals: { currency: string; amount: number }[] | undefined,
  formatCurrency: (value: number, currency: string) => string,
  t: (key: string, values?: Record<string, unknown>) => string,
): string => {
  if (!totals || totals.length === 0) return t("dashboard.nothingOwed");
  const [first, ...rest] = totals;
  const base = formatCurrency(first.amount, first.currency);
  return rest.length ? `${base} ${t("dashboard.moreCurrencies", { count: rest.length })}` : base;
};

/**
 * The Invoices card's "N overdue (amount)" hint, or nothing while nothing is
 * overdue — zero is the ordinary case, so a standing zero would be a fixture
 * rather than something worth reading, the same rule as `awaitingApprovalHint`.
 * Only NOK in this phase.
 */
export const invoicesOverdueHint = (
  summary: Pick<InvoicesSummary, "overdueCount" | "overdueAmount"> | undefined,
  formatCurrency: (value: number, currency: string) => string,
  t: (key: string, values?: Record<string, unknown>) => string,
): string | undefined =>
  summary && summary.overdueCount > 0
    ? t("dashboard.invoicesOverdueHint", {
        count: summary.overdueCount,
        amount: formatCurrency(summary.overdueAmount, "NOK"),
      })
    : undefined;

export const deltaPercent = (current: number, absoluteDelta: number) => {
  const previous = current - absoluteDelta;
  if (previous === 0) return absoluteDelta === 0 ? 0 : absoluteDelta > 0 ? 100 : -100;
  return Math.round((absoluteDelta / Math.abs(previous)) * 100);
};

/**
 * The Invoices card's figures: the outstanding amount in NOK ("—" before the
 * summary is known), the overdue hint, and the period's issued gross against
 * the previous period's — not the outstanding figure's, so its label says so.
 */
export const invoicesKpi = (
  summary: InvoicesSummary | undefined,
  formatCurrency: (value: number, currency: string) => string,
  t: (key: string, values?: Record<string, unknown>) => string,
) => ({
  value: summary ? formatCurrency(summary.outstandingAmount, "NOK") : "—",
  hint: invoicesOverdueHint(summary, formatCurrency, t),
  delta: summary
    ? {
        value: deltaPercent(summary.issuedGrossTotal, summary.issuedGrossTotalDelta),
        label: t("dashboard.invoicesIssuedDelta"),
      }
    : undefined,
});

/**
 * The Staff card's figures (design S20): the headcount today, and as the
 * hint the full-time equivalents and who starts and ends within thirty days,
 * the people away today (delivery B) — with the records that never got an
 * employment named only while there are any, since zero is the ordinary case.
 */
export const staffKpi = (
  stats: StaffStats | undefined,
  formatNumber: (value: number, options?: Intl.NumberFormatOptions) => string,
  t: (key: string, values?: Record<string, unknown>) => string,
) => {
  if (!stats) return { value: "—", hint: undefined };
  const figures = t("dashboard.staffKpiHint", {
    fte: formatNumber(stats.fteSum, { maximumFractionDigits: 2 }),
    starting: stats.startingSoon,
    ending: stats.endingSoon,
  });
  const hint =
    stats.onLeaveToday === undefined
      ? figures
      : `${figures} · ${t("dashboard.staffOnLeaveTodayHint", { count: stats.onLeaveToday })}`;
  return {
    value: stats.headcount,
    hint: stats.peopleWithoutEmployment
      ? `${hint} · ${t("dashboard.staffWithoutEmploymentHint", { count: stats.peopleWithoutEmployment })}`
      : hint,
  };
};

export const sparkline = (points: DailyPoint[] | undefined) => points?.map((point) => point.value);

export const relativeTime = (value: string, locale: string) => {
  const elapsedSeconds = (Date.now() - new Date(value).getTime()) / 1000;
  const absoluteSeconds = Math.abs(elapsedSeconds);
  const [amount, unit] =
    absoluteSeconds < 60
      ? [Math.round(elapsedSeconds), "second"]
      : absoluteSeconds < 3600
        ? [Math.round(elapsedSeconds / 60), "minute"]
        : absoluteSeconds < 86400
          ? [Math.round(elapsedSeconds / 3600), "hour"]
          : [Math.round(elapsedSeconds / 86400), "day"];
  return new Intl.RelativeTimeFormat(locale === "nb" ? "nb-NO" : "en-US", { numeric: "auto" }).format(
    amount,
    unit as Intl.RelativeTimeFormatUnit,
  );
};
