import type { components } from "../api-schema";

type Schemas = components["schemas"];

/*
 * Absence as the server answers it, already shaped for the reader (design
 * §5): the fake hands these out as they are, so a page test proves the page
 * draws what the server sent and nothing it would have to work out itself.
 */

/** Ada's holiday account for 2026, as she reads it on My page. */
export const holidayAccount = (
  overrides: Partial<Schemas["StaffHolidayAccount"]> = {},
): Schemas["StaffHolidayAccount"] => ({
  personId: 101,
  year: 2026,
  entitlement: 25,
  carriedIn: 3,
  carriedInDerived: 0,
  taken: 10,
  pending: 5,
  remaining: 18,
  overdrawn: false,
  ...overrides,
});

/**
 * Ada's own absences, newest first, as she reads them herself: a pregnancy
 * leave — an HR-only type, which she reads by name and which waits for
 * staff:personal-data — a ferie she asked for, an egenmelding registered in
 * September and the summer's approved ferie.
 */
export const myAbsences = (): Schemas["StaffAbsence"][] => [
  {
    id: 504,
    personId: 101,
    status: "requested",
    firstDay: "2027-01-04",
    lastDay: "2027-03-26",
    workingDays: 60,
    shownAs: "type",
    typeId: 1005,
    percent: 100,
    revision: 1,
  },
  {
    id: 501,
    personId: 101,
    status: "requested",
    firstDay: "2026-11-16",
    lastDay: "2026-11-20",
    workingDays: 5,
    shownAs: "type",
    typeId: 1001,
    percent: 100,
    note: "Autumn break",
    revision: 2,
  },
  {
    id: 503,
    personId: 101,
    status: "registered",
    firstDay: "2026-09-14",
    lastDay: "2026-09-15",
    workingDays: 2,
    shownAs: "type",
    typeId: 1002,
    percent: 100,
    revision: 1,
  },
  {
    id: 502,
    personId: 101,
    status: "approved",
    firstDay: "2026-07-06",
    lastDay: "2026-07-24",
    workingDays: 15,
    shownAs: "type",
    typeId: 1001,
    percent: 100,
    decisionNote: "Enjoy the summer",
    revision: 3,
  },
];

/** Ada's egenmelding standing under the statutory minimum: one occurrence of two days in the rolling year. */
export const selfCertification = (
  overrides: Partial<Schemas["StaffSelfCertification"]> = {},
): Schemas["StaffSelfCertification"] => ({
  personId: 101,
  policy: "minimum",
  daysPerOccurrence: 3,
  occurrencesPerYear: 4,
  occurrencesUsed: 1,
  daysUsed: 2,
  eligibleFrom: "2025-03-01",
  ...overrides,
});

/** Ada's care days for 2026: two children, three days used. */
export const careDays = (overrides: Partial<Schemas["StaffCareDays"]> = {}): Schemas["StaffCareDays"] => ({
  personId: 101,
  year: 2026,
  recorded: true,
  childrenCounted: 2,
  entitlement: 10,
  used: 3,
  ineligibleDays: 0,
  remaining: 7,
  exceeded: false,
  employerPaid: 3,
  beyondEmployer: 0,
  eligible: true,
  eligibleFrom: "2025-01-29",
  ...overrides,
});

const grace = { id: 102, displayName: "Grace Hopper", employeeNumber: "E-2" };

/**
 * The absence calendar's people as Ada reads them for October: her report
 * Grace with an approved ferie and an HR-only leave shown to Ada as "leave",
 * without its type, hours or note.
 */
export const calendarPeople = (): Schemas["StaffCalendarPerson"][] => [
  {
    person: grace,
    absences: [
      {
        id: 611,
        personId: 102,
        status: "approved",
        firstDay: "2026-10-19",
        lastDay: "2026-10-23",
        workingDays: 5,
        shownAs: "type",
        typeId: 1001,
        percent: 100,
      },
      {
        id: 612,
        personId: 102,
        status: "approved",
        firstDay: "2026-10-26",
        lastDay: "2026-10-27",
        workingDays: 2,
        shownAs: "leave",
      },
    ],
  },
];

/** The requests Ada decides: Grace's ferie in November, with the note she wrote. */
export const approvals = (): Schemas["StaffApproval"][] => [
  {
    person: grace,
    absence: {
      id: 601,
      personId: 102,
      status: "requested",
      firstDay: "2026-11-02",
      lastDay: "2026-11-06",
      workingDays: 5,
      shownAs: "type",
      typeId: 1001,
      percent: 100,
      note: "Family visit",
    },
  },
];

/**
 * Grace's open sick-leave case as her manager reads it: the milestones by
 * their dates, the accommodation note, and no milestone note — those are the
 * person's and staff:personal-data's alone.
 */
export const cases = (): Schemas["StaffCase"][] => [
  {
    id: 701,
    personId: 102,
    firstDay: "2026-09-28",
    lastDay: "2026-10-30",
    open: true,
    maxBenefitEstimate: "2027-09-27",
    maxBenefitEstimateEdited: false,
    accommodationNote: "Shorter days for now",
    revision: 2,
    milestones: [
      { kind: "employer_period_end", dueDate: "2026-10-13", due: false, waived: false, revision: 1 },
      { kind: "follow_up_plan", dueDate: "2026-10-26", due: true, waived: false, revision: 1 },
      { kind: "dialogue_1", dueDate: "2026-11-16", due: false, waived: false, revision: 1 },
      { kind: "nav_activity", dueDate: "2026-11-23", due: false, waived: false, revision: 1 },
      { kind: "dialogue_2", dueDate: "2027-03-29", due: false, waived: false, revision: 1 },
    ],
  },
];

const alan = { id: 103, displayName: "Alan Turing", employeeNumber: "E-3" };

/**
 * The absence calendar's people as a colleague (`staff:view` alone) reads
 * them for October: the dates of each approved or registered row and
 * nothing else — no status, no type, no working days, no share of the day
 * (`shownAs` absent). Grace's HR-only leave is a bar like any other.
 */
export const colleagueCalendarPeople = (): Schemas["StaffCalendarPerson"][] => [
  {
    person: grace,
    absences: [
      { personId: 102, firstDay: "2026-10-19", lastDay: "2026-10-23", shownAs: "absent" },
      { personId: 102, firstDay: "2026-10-26", lastDay: "2026-10-27", shownAs: "absent" },
    ],
  },
  {
    person: alan,
    absences: [{ personId: 103, firstDay: "2026-10-05", lastDay: "2026-10-05", shownAs: "absent" }],
  },
];

/**
 * The calendar as HR reads it for October: Grace's ferie and her HR-only
 * leave shown as approved "leave" (HR without `staff:personal-data`), Alan's
 * request at half days waiting, and a doctor's visit of two hours.
 */
export const hrCalendarPeople = (): Schemas["StaffCalendarPerson"][] => [
  ...calendarPeople(),
  {
    person: alan,
    absences: [
      {
        id: 622,
        personId: 103,
        status: "requested",
        firstDay: "2026-10-12",
        lastDay: "2026-10-16",
        workingDays: 5,
        shownAs: "type",
        typeId: 1007,
        percent: 50,
      },
      {
        id: 623,
        personId: 103,
        status: "registered",
        firstDay: "2026-10-21",
        lastDay: "2026-10-21",
        workingDays: 0,
        shownAs: "type",
        typeId: 1020,
        hours: 2,
      },
    ],
  },
];

/**
 * The inbox of a holder of `staff:personal-data` alone, who decides the
 * HR-only types (plan reading 33): Grace's pregnancy leave, by its name.
 */
export const hrOnlyApprovals = (): Schemas["StaffApproval"][] => [
  {
    person: grace,
    absence: {
      id: 605,
      personId: 102,
      status: "requested",
      firstDay: "2027-02-01",
      lastDay: "2027-04-30",
      workingDays: 64,
      shownAs: "type",
      typeId: 1005,
      percent: 100,
      revision: 1,
    },
  },
];

/**
 * What the person page's Absence tab reads of one person, as the caller
 * reads it: the rows of `GET /absences?personId=`, the holiday accounts per
 * year, HR's adjustments, the egenmelding standing and the care days — each
 * left out for a reader the server does not answer it (`careDays` null is
 * the 404 a reader outside the care-day fields gets).
 */
export interface PersonAbsence {
  absences: Schemas["StaffAbsence"][];
  accounts?: Schemas["StaffHolidayAccount"][];
  adjustments?: Schemas["StaffHolidayAdjustment"][];
  selfCertification?: Schemas["StaffSelfCertification"] | null;
  careDays?: Schemas["StaffCareDays"] | null;
}

/**
 * Grace's absence as HR without `staff:personal-data` reads it: an opaque
 * etag on every row in place of the revision, her HR-only leave as an
 * approved "leave" with no type, note or decision, the summer's approved
 * ferie, the autumn's sykmelding registered, and a ferie she asked for in
 * December.
 */
export const graceAbsencesAsHr = (): Schemas["StaffAbsence"][] => [
  {
    id: 714,
    personId: 102,
    status: "requested",
    firstDay: "2026-12-21",
    lastDay: "2026-12-23",
    workingDays: 3,
    shownAs: "type",
    typeId: 1001,
    percent: 100,
    note: "Christmas",
    etag: "e714",
  },
  {
    id: 712,
    personId: 102,
    status: "registered",
    firstDay: "2026-09-28",
    lastDay: "2026-10-30",
    workingDays: 25,
    shownAs: "type",
    typeId: 1003,
    percent: 100,
    degreePercent: 100,
    etag: "e712",
  },
  {
    id: 713,
    personId: 102,
    status: "approved",
    firstDay: "2026-08-17",
    lastDay: "2026-08-21",
    workingDays: 5,
    shownAs: "leave",
    etag: "e713",
  },
  {
    id: 711,
    personId: 102,
    status: "approved",
    firstDay: "2026-07-06",
    lastDay: "2026-07-24",
    workingDays: 15,
    shownAs: "type",
    typeId: 1001,
    percent: 100,
    decisionNote: "Enjoy",
    etag: "e711",
  },
];

/**
 * The same rows as Grace's manager reads them: the types the matrix lets a
 * manager name, the HR-only leave as "leave", and neither a revision nor an
 * etag — a manager writes none of them.
 */
export const graceAbsencesAsManager = (): Schemas["StaffAbsence"][] =>
  graceAbsencesAsHr().map((row) => {
    const shown = { ...row };
    delete shown.etag;
    return shown;
  });

export const graceAccount = (overrides: Partial<Schemas["StaffHolidayAccount"]> = {}) =>
  holidayAccount({ personId: 102, carriedIn: 3, taken: 15, pending: 3, remaining: 9, ...overrides });

export const graceAdjustments = (): Schemas["StaffHolidayAdjustment"][] => [
  {
    id: 801,
    personId: 102,
    year: 2026,
    days: 3,
    reason: "agreed_carry_over",
    fromYear: 2025,
    createdAt: "2026-01-05T09:00:00Z",
  },
];

/** Grace's absence as HR reads it, every part answered. */
export const graceAsHr = (): PersonAbsence => ({
  absences: graceAbsencesAsHr(),
  accounts: [graceAccount(), graceAccount({ year: 2025, carriedIn: 0, taken: 18, pending: 0, remaining: 3 })],
  adjustments: graceAdjustments(),
  selfCertification: selfCertification({ personId: 102 }),
  careDays: careDays({ personId: 102 }),
});

/** Grace's absence as her manager reads it: no adjustments, no care days, the standing without a withdrawal. */
export const graceAsManager = (): PersonAbsence => ({
  absences: graceAbsencesAsManager(),
  accounts: [graceAccount()],
  selfCertification: selfCertification({ personId: 102 }),
  careDays: null,
});

/**
 * Grace's case as staff:personal-data reads it: the milestones' notes and a
 * waiver's reason, which nobody else is sent.
 */
export const casesAsPersonalData = (): Schemas["StaffCase"][] =>
  cases().map((one) => ({
    ...one,
    milestones: one.milestones.map((milestone) =>
      milestone.kind === "follow_up_plan"
        ? { ...milestone, note: "Plan agreed with Grace", revision: 4 }
        : milestone.kind === "nav_activity"
          ? { ...milestone, waived: true, due: false, waivedReason: "Back at work part-time" }
          : milestone,
    ),
  }));

/** Grace's case as an Approver reads it: the milestones, and no accommodation note — it is not theirs. */
export const casesAsApprover = (): Schemas["StaffCase"][] =>
  cases().map((one) => {
    const shown = { ...one };
    delete shown.accommodationNote;
    return shown;
  });

/**
 * Every absence type as `GET /absence-types` answers staff:manage: the
 * seeds `/meta` offers, a hidden seed, the reserved `absent` and a company
 * type of its own, HR-only.
 */
export const allAbsenceTypes = (types: Schemas["StaffAbsenceType"][]): Schemas["StaffAbsenceType"][] => [
  ...types,
  {
    id: 1011,
    key: "velferdspermisjon",
    names: { en: "Welfare leave", nb: "Velferdspermisjon" },
    kind: "leave",
    unit: "days",
    paid: true,
    drawsHoliday: false,
    allowsPercent: false,
    needsDecision: true,
    visibility: "absent",
    ameldingLeave: "andreIkkeLovfestedePermisjoner",
    seeded: true,
    hidden: true,
    reserved: false,
    sort: 11,
    revision: 1,
  },
  {
    id: 1099,
    key: "absent",
    names: { en: "Absent", nb: "Fravær" },
    kind: "other",
    unit: "days",
    paid: false,
    drawsHoliday: false,
    allowsPercent: false,
    needsDecision: false,
    visibility: "absent",
    seeded: true,
    hidden: true,
    reserved: true,
    sort: 999,
    revision: 1,
  },
  {
    id: 2001,
    key: "studiedag",
    names: { en: "Study day", nb: "Studiedag" },
    kind: "time_off",
    unit: "days",
    paid: true,
    drawsHoliday: false,
    allowsPercent: true,
    needsDecision: true,
    visibility: "hr_only",
    seeded: false,
    hidden: false,
    reserved: false,
    sort: 30,
    revision: 2,
  },
];
