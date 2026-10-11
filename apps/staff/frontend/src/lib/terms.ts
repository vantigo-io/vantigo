import type {
  StaffAppointmentForm,
  StaffEmployeeType,
  StaffEmploymentType,
  StaffNoticeUnit,
  StaffRelationshipType,
  StaffRemunerationType,
  StaffTemporaryGround,
  StaffWorkingHoursScheme,
} from "../api/employments";

// The enumerations the contract names, in the order a picker offers them.
// Their labels are catalog keys built from the value, so a value added to the
// contract without a label fails the catalog's own test rather than showing
// a raw code.

export const employmentTypes: readonly StaffEmploymentType[] = ["fast", "midlertidig"];
export const temporaryGrounds: readonly StaffTemporaryGround[] = ["a", "b", "c", "d", "e", "tariff"];
export const workingHoursSchemes: readonly StaffWorkingHoursScheme[] = [
  "not_shift",
  "shift_365",
  "offshore_336",
  "continuous_336",
  "round_the_clock_355",
  "other_shift",
];
export const relationshipTypes: readonly StaffRelationshipType[] = [
  "ordinary",
  "maritime",
  "freelancer",
  "pension_or_other",
];
export const employeeTypes: readonly StaffEmployeeType[] = ["employee", "contractor", "consultant", "intern"];
export const noticeUnits: readonly StaffNoticeUnit[] = ["days", "weeks", "months"];
export const remunerationTypes: readonly StaffRemunerationType[] = [
  "monthly",
  "hourly",
  "commission",
  "fee",
  "piecework",
];

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
