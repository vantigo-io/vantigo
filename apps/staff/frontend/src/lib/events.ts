/**
 * The kinds of change a person's history holds in this delivery (design
 * S21), each named by a catalog key `event_<kind>`. A kind the server adds
 * later still renders — as its code — rather than vanishing.
 */
export const eventKinds = [
  "person_created",
  "person_updated",
  "next_of_kin_updated",
  "roles_updated",
  "national_id_set",
  "national_id_revealed",
  "bank_account_set",
  "bank_account_revealed",
  "user_linked",
  "user_unlinked",
  "employment_created",
  "employment_updated",
  "employment_ended",
  "employment_reopened",
  "terms_added",
  "terms_corrected",
  "terms_deleted",
] as const;

/**
 * The catalog key that names a field of a payload — the contract's own
 * field names, which are mostly the form's labels already. Where the form's
 * key differs (a terms row's `title` is the job title), it is said here.
 */
const fieldLabelOverrides: Record<string, string> = {
  title: "jobTitle",
  note: "termsNote",
  notes: "hrNotes",
  departmentId: "department",
  locationId: "location",
  managerPersonId: "manager",
  businessUnitId: "businessUnit",
  user: "linkedUser",
  nationalId: "nationalId",
  bankAccount: "bankAccount",
  nextOfKin: "nextOfKin",
  roles: "roles",
};

export const eventFieldLabelKey = (field: string): string => fieldLabelOverrides[field] ?? field;

/** Every field a payload of this delivery can name — the person's, the employment's and a terms row's. */
export const eventFields = [
  "employeeNumber",
  "givenName",
  "familyName",
  "displayName",
  "workEmail",
  "phoneWork",
  "workCountry",
  "preferredLanguage",
  "birthDate",
  "gender",
  "phoneMobile",
  "phonePrivate",
  "privateEmail",
  "street",
  "postalCode",
  "city",
  "country",
  "notes",
  "nextOfKin",
  "roles",
  "nationalId",
  "bankAccount",
  "user",
  "employmentNumber",
  "startDate",
  "endDate",
  "endReason",
  "businessUnitId",
  "relationshipType",
  "contractSignedAt",
  "validFrom",
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
  "employeeType",
  "costCentre",
  "division",
  "probationEndDate",
  "probationExtensionAgreed",
  "probationExtendedTo",
  "probationExtensionReason",
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
  "payComponents",
  "payMethod",
  "paydayText",
  "seniorityDate",
  "payGrade",
  "note",
] as const;

/** What one field of a payload says: its values before and after, or only that it changed. */
export type FieldChange = "changed" | { from?: unknown; to?: unknown };

export interface EventFormatting {
  /** A catalog sentence. */
  t: (key: string, options?: Record<string, unknown>) => string;
  /** Whether the catalog has a key — an enumeration's value is named when it does. */
  has: (key: string) => boolean;
  date: (value: string) => string;
  number: (value: number) => string;
}

const ISO_DAY = /^\d{4}-\d{2}-\d{2}$/;

/**
 * One value of a payload as the reader reads it: a day as a date, a code by
 * its label when the catalog names it (`employmentType` `fast` is
 * "Permanent (fast)"), a list item by item, and the few shapes the module
 * writes — a user, a sealed field's kind and last four, a pay component, a
 * role, a next of kin — by what a person recognises them by.
 */
export const formatEventValue = (field: string, value: unknown, f: EventFormatting): string => {
  if (value === undefined || value === null || value === "") return "—";
  if (typeof value === "boolean") return f.t(value ? "yes" : "no");
  if (typeof value === "number") return f.number(value);
  if (typeof value === "string") {
    if (ISO_DAY.test(value)) return f.date(value);
    const enumKey = `${eventFieldLabelKey(field)}_${value}`;
    return f.has(enumKey) ? f.t(enumKey) : value;
  }
  if (Array.isArray(value))
    return value.length === 0 ? "—" : value.map((one) => formatEventValue(field, one, f)).join(", ");
  if (typeof value === "object") {
    const shape = value as Record<string, unknown>;
    if (typeof shape.displayName === "string") return shape.displayName;
    if (typeof shape.role === "string") return f.t(`role_${shape.role}`);
    if (typeof shape.name === "string" && typeof shape.amount === "number")
      return f.t("payComponentLine", {
        name: shape.name,
        amount: f.number(shape.amount),
        currency: shape.currency,
        period: f.t(`payPeriod_${shape.period}`),
      });
    if (typeof shape.name === "string") return shape.name;
    if (typeof shape.last4 === "string")
      return `${f.t(`bankAccountKind_${shape.kind}`)}, ${f.t("endingIn", { last4: shape.last4 })}`;
    if (typeof shape.kind === "string")
      return [f.t(`nationalIdKind_${shape.kind}`), shape.country].filter(Boolean).join(", ");
  }
  return String(value);
};

/** The lines an event's payload says, one per field, in the order the payload names them. */
export const eventLines = (payload: Record<string, unknown>, f: EventFormatting): string[] => {
  const fields = payload.fields;
  if (!fields || typeof fields !== "object") return [];
  return Object.entries(fields as Record<string, FieldChange>).map(([field, change]) => {
    const label = f.has(eventFieldLabelKey(field)) ? f.t(eventFieldLabelKey(field)) : field;
    if (change === "changed") return f.t("fieldChanged", { field: label });
    return f.t("fieldFromTo", {
      field: label,
      from: formatEventValue(field, change?.from, f),
      to: formatEventValue(field, change?.to, f),
    });
  });
};
