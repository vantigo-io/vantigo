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
