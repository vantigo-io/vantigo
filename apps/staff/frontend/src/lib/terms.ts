import type {
  StaffAppointmentForm,
  StaffEmployeeType,
  StaffEmploymentType,
  StaffEndReason,
  StaffGender,
  StaffNoticeUnit,
  StaffPayPeriod,
  StaffRelationshipType,
  StaffRemunerationType,
  StaffTemporaryGround,
  StaffTerms,
  StaffWorkingHoursScheme,
} from "../api/employments";
import type {
  StaffBankAccountKind,
  StaffNationalIdKind,
  StaffNextOfKinRelationship,
  StaffPersonRoleKind,
} from "../api/people";

// The enumerations the contract names, in the order a picker offers them.
// Their labels are catalog keys built from the value, so a value added to the
// contract without a label fails the catalog's own test rather than showing
// a raw code.

// Each list is the keys of a record that `satisfies` the contract's union, so
// a value the contract gains fails the typecheck here instead of going
// unoffered; the order of the keys is the order a picker shows.
const valuesOf = <T extends string>(record: Record<T, true>): readonly T[] => Object.keys(record) as T[];

export const employmentTypes = valuesOf({ fast: true, midlertidig: true } satisfies Record<StaffEmploymentType, true>);
export const temporaryGrounds = valuesOf({
  a: true,
  b: true,
  c: true,
  d: true,
  e: true,
  tariff: true,
} satisfies Record<StaffTemporaryGround, true>);
export const workingHoursSchemes = valuesOf({
  not_shift: true,
  shift_365: true,
  offshore_336: true,
  continuous_336: true,
  round_the_clock_355: true,
  other_shift: true,
} satisfies Record<StaffWorkingHoursScheme, true>);
export const relationshipTypes = valuesOf({
  ordinary: true,
  maritime: true,
  freelancer: true,
  pension_or_other: true,
} satisfies Record<StaffRelationshipType, true>);
export const employeeTypes = valuesOf({
  employee: true,
  contractor: true,
  consultant: true,
  intern: true,
} satisfies Record<StaffEmployeeType, true>);
export const noticeUnits = valuesOf({ days: true, weeks: true, months: true } satisfies Record<StaffNoticeUnit, true>);
export const remunerationTypes = valuesOf({
  monthly: true,
  hourly: true,
  commission: true,
  fee: true,
  piecework: true,
} satisfies Record<StaffRemunerationType, true>);
export const genders = valuesOf({
  female: true,
  male: true,
  other: true,
  unspecified: true,
} satisfies Record<StaffGender, true>);
export const appointmentForms = valuesOf({
  fast: true,
  fast_utleid: true,
  midlertidig: true,
  midlertidig_utleid: true,
  midlertidig_tilkallingsvikar: true,
} satisfies Record<StaffAppointmentForm, true>);

/**
 * The appointment forms that go with a type (the a-melding's ansettelsesform):
 * `fast` and `fast_utleid` with a permanent employment, the other three with a
 * temporary one. The server refuses anything else `appointment_form_mismatch`;
 * the form offers only these, and the type's own first.
 */
export const appointmentFormsFor = (type: StaffEmploymentType | null | undefined): readonly StaffAppointmentForm[] =>
  type === "midlertidig"
    ? ["midlertidig", "midlertidig_utleid", "midlertidig_tilkallingsvikar"]
    : type === "fast"
      ? ["fast", "fast_utleid"]
      : [];

/** Whether an appointment form hires the person out, so the hirer (§ 14-6 o) is asked for. */
export const isHiredOut = (form: StaffAppointmentForm | null | undefined): boolean =>
  form === "fast_utleid" || form === "midlertidig_utleid";

/** The hours of a full position a scheme starts with — what the server fills in when they are left out. */
export const defaultFullTimeHours: Record<StaffWorkingHoursScheme, number> = {
  not_shift: 37.5,
  shift_365: 36.5,
  offshore_336: 33.6,
  continuous_336: 33.6,
  round_the_clock_355: 35.5,
  other_shift: 37.5,
};

/**
 * The a-melding's rule for the hours of a full position per scheme, as the
 * server checks it (`full_time_hours_mismatch`): exactly 36.5 for shift_365,
 * exactly 35.5 for round_the_clock_355, 28 to 33.6 for the two 336 schemes,
 * free otherwise (more than 0, at most 168). The form shows a mismatch before
 * it is sent; the server's answer is still the one that counts.
 */
export const fullTimeHoursMismatch = (scheme: StaffWorkingHoursScheme, hours: number): boolean => {
  switch (scheme) {
    case "shift_365":
      return hours !== 36.5;
    case "round_the_clock_355":
      return hours !== 35.5;
    case "offshore_336":
    case "continuous_336":
      return hours < 28 || hours > 33.6;
    default:
      return hours <= 0 || hours > 168;
  }
};

/** The catalog key of the sentence that states a scheme's rule. */
export const hoursRuleKey = (scheme: StaffWorkingHoursScheme): string =>
  scheme === "shift_365" || scheme === "round_the_clock_355"
    ? "hoursRuleExactly"
    : scheme === "offshore_336" || scheme === "continuous_336"
      ? "hoursRuleRange"
      : "hoursRuleFree";

/** A STYRK-08 yrkeskode is exactly seven digits. */
export const isOccupationCode = (value: string): boolean => /^\d{7}$/.test(value);

/** A percentage of a full position: more than 0, at most 100, at most two decimals. */
export const isFtePercent = (value: number): boolean =>
  value > 0 && value <= 100 && Math.abs(Math.round(value * 100) - value * 100) < 1e-9;

export const endReasons = valuesOf({
  employee_resigned: true,
  employer_terminated: true,
  contract_ended: true,
  wrongly_reported: true,
  internal_change: true,
  payroll_system_change: true,
} satisfies Record<StaffEndReason, true>);

export const payPeriods = valuesOf({
  monthly: true,
  yearly: true,
  hourly: true,
  once: true,
} satisfies Record<StaffPayPeriod, true>);

export const nextOfKinRelationships = valuesOf({
  spouse: true,
  partner: true,
  parent: true,
  child: true,
  sibling: true,
  other: true,
} satisfies Record<StaffNextOfKinRelationship, true>);

export const personRoleKinds = valuesOf({
  verneombud: true,
  amu_member: true,
  hms_responsible: true,
} satisfies Record<StaffPersonRoleKind, true>);

export const nationalIdKinds = valuesOf({
  fodselsnummer: true,
  d_nummer: true,
  foreign: true,
} satisfies Record<StaffNationalIdKind, true>);

export const bankAccountKinds = valuesOf({ bban: true, iban: true } satisfies Record<StaffBankAccountKind, true>);

/** The seventeen items aml. § 14-6 (1) asks a written contract to hold, a to q, in the law's order. */
export const contractCheckItems = [
  "a",
  "b",
  "c",
  "d",
  "e",
  "f",
  "g",
  "h",
  "i",
  "j",
  "k",
  "l",
  "m",
  "n",
  "o",
  "p",
  "q",
] as const;

/** A pay component as the form adds it: Norwegian kroner by the month until HR says otherwise. */
export const DEFAULT_CURRENCY = "NOK";
export const MAX_PAY_COMPONENTS = 20;
export const MAX_NEXT_OF_KIN = 3;
export const MAX_PERSON_ROLES = 20;

/** Whether a terms row is in force on `today`: it has taken effect, and the next row has not. */
export const inForceOn = (row: Pick<StaffTerms, "validFrom" | "validTo">, today: string): boolean =>
  row.validFrom <= today && (!row.validTo || row.validTo >= today);
