import type {
  StaffAppointmentForm,
  StaffEmployeeType,
  StaffEmploymentType,
  StaffNoticeUnit,
  StaffPayComponent,
  StaffPayPeriod,
  StaffRemunerationType,
  StaffTemporaryGround,
  StaffTerms,
  StaffTermsInput,
  StaffWorkingHoursScheme,
} from "../api/employments";
import {
  DEFAULT_CURRENCY,
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
  /** The pay group's list (§ 14-6 i), at most twenty. */
  payComponents: PayComponentFormValues[];
  seniorityDate: string | null;
  payGrade: string;
  /** The probation's agreed extension (aml. § 15-6 (4)): agreed in writing, with its last day and why. */
  probationExtensionAgreed: boolean;
  probationExtendedTo: string | null;
  probationExtensionReason: string;
  note: string;
}

/** One pay component as the form holds it: the amount a number or the text being typed. */
export interface PayComponentFormValues {
  name: string;
  amount: number | string;
  currency: string;
  period: StaffPayPeriod;
}

export const emptyPayComponent = (): PayComponentFormValues => ({
  name: "",
  amount: "",
  currency: DEFAULT_CURRENCY,
  period: "monthly",
});

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
  payComponents: [],
  seniorityDate: null,
  payGrade: "",
  probationExtensionAgreed: false,
  probationExtendedTo: null,
  probationExtensionReason: "",
  note: "",
});

/**
 * A stored terms row as the form opens it — New terms from the row in force,
 * a correction from the row itself. A field the response does not carry is
 * empty, as on a new row.
 */
export const termsFormValuesOf = (row: StaffTerms): TermsFormValues => {
  const scheme = row.workingHoursScheme ?? "not_shift";
  return {
    employmentType: row.employmentType,
    temporaryGround: row.temporaryGround ?? null,
    expectedEndDate: row.expectedEndDate ?? null,
    appointmentForm: row.appointmentForm ?? null,
    hiredOutTo: row.hiredOutTo ?? "",
    ftePercent: row.ftePercent,
    workingHoursScheme: scheme,
    fullTimeHoursPerWeek: row.fullTimeHoursPerWeek ?? defaultFullTimeHours[scheme],
    departmentId: row.departmentId ? String(row.departmentId) : null,
    locationId: row.locationId ? String(row.locationId) : null,
    managerPersonId: row.managerPersonId ?? null,
    title: row.title ?? "",
    occupationCode: row.occupationCode ?? "",
    employeeType: row.employeeType ?? null,
    costCentre: row.costCentre ?? "",
    division: row.division ?? "",
    probationEndDate: row.probationEndDate ?? null,
    noticeEmployee: row.noticeEmployee ?? "",
    noticeEmployer: row.noticeEmployer ?? "",
    noticeUnit: row.noticeUnit ?? null,
    workingTimeText: row.workingTimeText ?? "",
    breakMinutes: row.breakMinutes ?? "",
    specialHoursText: row.specialHoursText ?? "",
    overtimeRuleText: row.overtimeRuleText ?? "",
    tariffReference: row.tariffReference ?? "",
    holidayTermsText: row.holidayTermsText ?? "",
    competenceDevelopmentText: row.competenceDevelopmentText ?? "",
    socialSecurityText: row.socialSecurityText ?? "",
    remunerationType: row.remunerationType ?? null,
    payMethod: row.payMethod ?? "",
    paydayText: row.paydayText ?? "",
    payComponents: (row.payComponents ?? []).map((one) => ({ ...one })),
    seniorityDate: row.seniorityDate ?? null,
    payGrade: row.payGrade ?? "",
    probationExtensionAgreed: row.probationExtensionAgreed ?? false,
    probationExtendedTo: row.probationExtendedTo ?? null,
    probationExtensionReason: row.probationExtensionReason ?? "",
    note: row.note ?? "",
  };
};

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
  "seniorityDate",
  "payGrade",
  "probationExtensionAgreed",
  "probationExtendedTo",
  "probationExtensionReason",
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

/** The pay components as the contract takes them; an amount typed with a comma is a number all the same. */
const payComponentsOf = (rows: PayComponentFormValues[]): StaffPayComponent[] =>
  rows.map((row) => ({
    name: row.name.trim(),
    amount: asNumber(row.amount) ?? 0,
    currency: row.currency.trim().toUpperCase(),
    period: row.period,
  }));

/**
 * The first terms row of an employment as the contract takes it: on a first
 * row a field left out is empty, so nothing unset is sent — not even a null.
 * The temporary employment's ground and expected end go only with a
 * temporary one, and the hirer only with a hired-out form.
 */
export const termsInputOf = (values: TermsFormValues): StaffTermsInput => buildTerms(values, false);

/**
 * A later row (`POST /employments/{id}/terms`) or a replaced one
 * (`PUT /terms/{id}`), with its day. Every field the form holds is sent, an
 * emptied one as null: on a new row a field left out would take the row
 * before's value, so a field HR cleared has to say so.
 */
export const termsRowInputOf = (values: TermsFormValues, validFrom: string): StaffTermsInput => ({
  validFrom,
  ...buildTerms(values, true),
});

const buildTerms = (values: TermsFormValues, nulls: boolean): StaffTermsInput => {
  const input: Record<string, unknown> = {};
  const set = (key: string, value: unknown) => {
    const text = typeof value === "string" ? value.trim() : value;
    if (text === null || text === undefined || text === "") {
      if (nulls) input[key] = null;
      return;
    }
    input[key] = text;
  };
  const temporary = values.employmentType === "midlertidig";
  set("employmentType", values.employmentType);
  set("temporaryGround", temporary ? values.temporaryGround : null);
  set("expectedEndDate", temporary ? values.expectedEndDate : null);
  set("appointmentForm", values.appointmentForm);
  set("hiredOutTo", isHiredOut(values.appointmentForm) ? values.hiredOutTo : null);
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
  set("payComponents", values.payComponents.length > 0 ? payComponentsOf(values.payComponents) : null);
  set("seniorityDate", values.seniorityDate);
  set("payGrade", values.payGrade);
  // The extension exists only as agreed: its day and reason go with the agreement.
  const extended = values.probationExtensionAgreed;
  set("probationExtensionAgreed", extended ? true : null);
  set("probationExtendedTo", extended ? values.probationExtendedTo : null);
  set("probationExtensionReason", extended ? values.probationExtensionReason : null);
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
  // Each component of the list, as Mantine's form validates a list's items.
  payComponents: {
    name: (value: string) =>
      value.trim() === ""
        ? t("payComponentNameRequired")
        : value.trim().length > 100
          ? t("textTooLong", { max: 100 })
          : null,
    amount: (value: number | string) => {
      const amount = asNumber(value);
      return amount === undefined || amount < 0 || Math.abs(Math.round(amount * 100) - amount * 100) > 1e-9
        ? t("amountInvalid")
        : null;
    },
    currency: (value: string) => (/^[A-Za-z]{3}$/.test(value.trim()) ? null : t("currencyInvalid")),
  },
  payGrade: (value: string) => (value.trim().length > 20 ? t("textTooLong", { max: 20 }) : null),
  probationExtensionReason: (value: string) => (value.trim().length > 200 ? t("textTooLong", { max: 200 }) : null),
});

/**
 * Where a field error of a terms request lands on a form that nests the row
 * under `terms`: the server names `payComponents[1].amount`, the form
 * `terms.payComponents.1.amount`; the row's day sits beside it.
 */
export const termsFormPath = (field: string): string =>
  field === "validFrom" ? field : `terms.${field.replace(/\[(\d+)\]/g, ".$1")}`;
