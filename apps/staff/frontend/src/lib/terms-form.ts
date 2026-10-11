import type {
  StaffAppointmentForm,
  StaffEmployeeType,
  StaffEmploymentType,
  StaffNoticeUnit,
  StaffRemunerationType,
  StaffTemporaryGround,
  StaffTermsInput,
  StaffWorkingHoursScheme,
} from "../api/employments";
import {
  defaultFullTimeHours,
  fullTimeHoursMismatch,
  hoursRuleKey,
  isFtePercent,
  isHiredOut,
  isOccupationCode,
} from "./terms";

/**
 * A terms row as the form holds it (design §3.3): a choice not made is null,
 * a number not typed is "", a text not written is "". The two the law cares
 * most about — the type and the percentage — start empty on purpose: an
 * unstated one is presumed to be what the employee claims (aml. § 14-6), so
 * nothing here ever fills them in.
 */
export interface TermsFormValues {
  employmentType: StaffEmploymentType | null;
  temporaryGround: StaffTemporaryGround | null;
  expectedEndDate: string | null;
  appointmentForm: StaffAppointmentForm | null;
  hiredOutTo: string;
  ftePercent: number | string;
  workingHoursScheme: StaffWorkingHoursScheme;
  fullTimeHoursPerWeek: number | string;
  departmentId: string | null;
  locationId: string | null;
  managerPersonId: number | null;
  title: string;
  occupationCode: string;
  // Under "More terms".
  employeeType: StaffEmployeeType | null;
  costCentre: string;
  division: string;
  probationEndDate: string | null;
  noticeEmployee: number | string;
  noticeEmployer: number | string;
  noticeUnit: StaffNoticeUnit | null;
  workingTimeText: string;
  breakMinutes: number | string;
  specialHoursText: string;
  overtimeRuleText: string;
  tariffReference: string;
  holidayTermsText: string;
  competenceDevelopmentText: string;
  socialSecurityText: string;
  remunerationType: StaffRemunerationType | null;
  payMethod: string;
  paydayText: string;
  note: string;
}

export const emptyTerms = (): TermsFormValues => ({
  employmentType: null,
  temporaryGround: null,
  expectedEndDate: null,
  appointmentForm: null,
  hiredOutTo: "",
  ftePercent: "",
  workingHoursScheme: "not_shift",
  fullTimeHoursPerWeek: defaultFullTimeHours.not_shift,
  departmentId: null,
  locationId: null,
  managerPersonId: null,
  title: "",
  occupationCode: "",
  employeeType: null,
  costCentre: "",
  division: "",
  probationEndDate: null,
  noticeEmployee: "",
  noticeEmployer: "",
  noticeUnit: null,
  workingTimeText: "",
  breakMinutes: "",
  specialHoursText: "",
  overtimeRuleText: "",
  tariffReference: "",
  holidayTermsText: "",
  competenceDevelopmentText: "",
  socialSecurityText: "",
  remunerationType: null,
  payMethod: "",
  paydayText: "",
  note: "",
});

/** The fields folded under "More terms", so a field error on one of them can unfold it. */
export const moreTermsFields = [
  "employeeType",
  "costCentre",
  "division",
  "probationEndDate",
  "noticeEmployee",
  "noticeEmployer",
  "noticeUnit",
  "workingTimeText",
  "breakMinutes",
  "specialHoursText",
  "overtimeRuleText",
  "tariffReference",
  "holidayTermsText",
  "competenceDevelopmentText",
  "socialSecurityText",
  "remunerationType",
  "payMethod",
  "paydayText",
  "note",
] as const;

export const termsFields = [
  "employmentType",
  "temporaryGround",
  "expectedEndDate",
  "appointmentForm",
  "hiredOutTo",
  "ftePercent",
  "workingHoursScheme",
  "fullTimeHoursPerWeek",
  "departmentId",
  "locationId",
  "managerPersonId",
  "title",
  "occupationCode",
  ...moreTermsFields,
] as const;

const asNumber = (value: number | string): number | undefined => {
  if (typeof value === "number") return Number.isFinite(value) ? value : undefined;
  const trimmed = value.trim().replace(",", ".");
  if (trimmed === "") return undefined;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : undefined;
};

/**
 * The first terms row of an employment as the contract takes it: on a first
 * row a field left out is empty, so nothing unset is sent — not even a null.
 * The temporary employment's ground and expected end go only with a
 * temporary one, and the hirer only with a hired-out form.
 */
export const termsInputOf = (values: TermsFormValues): StaffTermsInput => {
  const input: Record<string, unknown> = {};
  const set = (key: string, value: unknown) => {
    if (value === null || value === undefined || value === "") return;
    input[key] = typeof value === "string" ? value.trim() || undefined : value;
    if (input[key] === undefined) delete input[key];
  };
  const temporary = values.employmentType === "midlertidig";
  set("employmentType", values.employmentType);
  if (temporary) {
    set("temporaryGround", values.temporaryGround);
    set("expectedEndDate", values.expectedEndDate);
  }
  set("appointmentForm", values.appointmentForm);
  if (isHiredOut(values.appointmentForm)) set("hiredOutTo", values.hiredOutTo);
  set("ftePercent", asNumber(values.ftePercent));
  set("workingHoursScheme", values.workingHoursScheme);
  set("fullTimeHoursPerWeek", asNumber(values.fullTimeHoursPerWeek));
  set("departmentId", values.departmentId ? Number(values.departmentId) : undefined);
  set("locationId", values.locationId ? Number(values.locationId) : undefined);
  set("managerPersonId", values.managerPersonId ?? undefined);
  set("title", values.title);
  set("occupationCode", values.occupationCode);
  set("employeeType", values.employeeType);
  set("costCentre", values.costCentre);
  set("division", values.division);
  set("probationEndDate", values.probationEndDate);
  set("noticeEmployee", asNumber(values.noticeEmployee));
  set("noticeEmployer", asNumber(values.noticeEmployer));
  set("noticeUnit", values.noticeUnit);
  set("workingTimeText", values.workingTimeText);
  set("breakMinutes", asNumber(values.breakMinutes));
  set("specialHoursText", values.specialHoursText);
  set("overtimeRuleText", values.overtimeRuleText);
  set("tariffReference", values.tariffReference);
  set("holidayTermsText", values.holidayTermsText);
  set("competenceDevelopmentText", values.competenceDevelopmentText);
  set("socialSecurityText", values.socialSecurityText);
  set("remunerationType", values.remunerationType);
  set("payMethod", values.payMethod);
  set("paydayText", values.paydayText);
  set("note", values.note);
  return input as StaffTermsInput;
};

type Translate = (key: string, options?: Record<string, unknown>) => string;

/**
 * The sentence that says why the hours do not fit the scheme, or null when
 * they do (or are left to the scheme). The scheme's hours are written as the
 * reader writes a number — 36,5 in Norwegian.
 */
export const hoursMismatchMessage = (
  t: Translate,
  values: TermsFormValues,
  formatNumber: (value: number) => string,
): string | null => {
  const hours = asNumber(values.fullTimeHoursPerWeek);
  if (hours === undefined || !fullTimeHoursMismatch(values.workingHoursScheme, hours)) return null;
  return t(hoursRuleKey(values.workingHoursScheme), {
    hours: formatNumber(defaultFullTimeHours[values.workingHoursScheme]),
  });
};

/**
 * What the form checks before asking, as validators for a form whose terms
 * sit under `terms` (an employment's first row nests them so). The server
 * judges all of it again, and its field errors are keyed the same way.
 */
export const termsValidation = (t: Translate, formatNumber: (value: number) => string) => ({
  employmentType: (value: StaffEmploymentType | null) => (value ? null : t("employmentTypeRequired")),
  temporaryGround: (value: StaffTemporaryGround | null, values: { terms: TermsFormValues }) =>
    values.terms.employmentType === "midlertidig" && !value ? t("temporaryGroundRequired") : null,
  ftePercent: (value: number | string) => {
    const percent = asNumber(value);
    if (percent === undefined) return t("ftePercentRequired");
    return isFtePercent(percent) ? null : t("ftePercentRange");
  },
  fullTimeHoursPerWeek: (_value: number | string, values: { terms: TermsFormValues }) =>
    hoursMismatchMessage(t, values.terms, formatNumber),
  occupationCode: (value: string) =>
    value.trim() === "" || isOccupationCode(value.trim()) ? null : t("occupationCodeInvalid"),
  noticeUnit: (value: StaffNoticeUnit | null, values: { terms: TermsFormValues }) =>
    !value &&
    (asNumber(values.terms.noticeEmployee) !== undefined || asNumber(values.terms.noticeEmployer) !== undefined)
      ? t("noticeUnitRequired")
      : null,
});
