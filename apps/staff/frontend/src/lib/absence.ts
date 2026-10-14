import type { StaffAbsence } from "../api/absence";
import type { StaffAbsenceType } from "../api/meta";
import { ApiValidationError } from "../api/request";
import { staffCatalog } from "../i18n";
import { useStaffFormat } from "./format";

/** A translation function, as `useI18n` hands one out. */
type Translate = (key: string, values?: Record<string, unknown>) => string;

/** The kinds a note is refused on (`absence_note_on_sick`): what they are about is health data. */
export const isSickKind = (kind: StaffAbsenceType["kind"]): boolean =>
  kind === "sick_self" || kind === "sick_certified" || kind === "child_sick";

/**
 * What a row is called to its reader, from what the server shaped: the type
 * the row names (`shownAs` type), "Leave" for an HR-only type the reader may
 * not name, "Absent" for a colleague, who reads no type. A type the row names
 * that `/meta` does not list (a hidden one) is "Absence" — never guessed.
 */
export const absenceName = (
  absence: StaffAbsence,
  types: readonly StaffAbsenceType[],
  locale: string,
  t: Translate,
): string => {
  if (absence.shownAs === "leave") return t("shownAs_leave");
  if (absence.shownAs === "absent" || absence.typeId === undefined) return t("shownAs_absent");
  const type = types.find((one) => one.id === absence.typeId);
  if (!type) return t("absenceUnnamed");
  return locale === "nb" ? type.names.nb : type.names.en;
};

/** The days of a row: one day, or the first and the last. */
export const absenceDays = (absence: Pick<StaffAbsence, "firstDay" | "lastDay">, date: (day: string) => string) =>
  absence.firstDay === absence.lastDay
    ? date(absence.firstDay)
    : `${date(absence.firstDay)} – ${date(absence.lastDay)}`;

const catalog = staffCatalog.en as Record<string, string>;

const codeOf = (error: unknown): string | undefined => {
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

/**
 * A refusal in the reader's words: a coded refusal by its code
 * (`refusal.<code>`), never the server's English; a validation error by its
 * first field's sentence; anything else by its own message.
 */
export const refusalText = (error: unknown, t: Translate): string => {
  const code = codeOf(error);
  if (code && `refusal.${code}` in catalog) return t(`refusal.${code}`);
  if (error instanceof ApiValidationError) return Object.values(error.fieldErrors)[0] ?? error.message;
  return error instanceof Error ? error.message : String(error);
};

/** The field of the request sheet each coded refusal is about. */
const refusalFields: Record<string, readonly string[]> = {
  percent_not_allowed: ["percent"],
  hours_on_day_type: ["hours", "lastDay"],
  absence_note_on_sick: ["note"],
  absence_overlap: ["firstDay"],
  absence_status_change: ["typeId"],
  self_certification_exhausted: ["typeId"],
  self_certification_too_early: ["typeId"],
  self_certification_withdrawn: ["typeId"],
};

/**
 * A refused absence write placed on the sheet: each field error on the field
 * the form shows (`shown`), a coded one in the catalog's words; a conflict
 * on the field its rule is about; what has no field on the form is said out
 * loud (`rest`).
 */
export const absenceRefusals = (
  error: unknown,
  shown: readonly string[],
  t: Translate,
): { fields: Record<string, string>; rest: string[] } => {
  const code = codeOf(error);
  const coded = code ? (refusalFields[code] ?? []) : [];
  const fields: Record<string, string> = {};
  const rest: string[] = [];
  if (error instanceof ApiValidationError) {
    for (const [field, message] of Object.entries(error.fieldErrors)) {
      const words = coded.includes(field) ? refusalText(error, t) : message;
      if (shown.includes(field)) fields[field] = words;
      else rest.push(words);
    }
    if (Object.keys(fields).length === 0 && rest.length === 0) rest.push(error.message);
    return { fields, rest };
  }
  const field = coded.find((one) => shown.includes(one));
  if (field) fields[field] = refusalText(error, t);
  else rest.push(refusalText(error, t));
  return { fields, rest };
};

/** What a row carries beyond its days: the working days or hours, a share of the day, the degree. */
export const useAbsenceDetails = () => {
  const { t, number } = useStaffFormat();
  return (absence: StaffAbsence): string[] =>
    [
      absence.hours !== undefined
        ? t("absenceHours", { hours: number(absence.hours) })
        : absence.workingDays === undefined
          ? undefined
          : absence.workingDays === 1
            ? t("oneWorkingDay")
            : t("workingDayCount", { count: absence.workingDays }),
      absence.percent !== undefined && absence.percent < 100
        ? t("absencePercent", { percent: number(absence.percent) })
        : undefined,
      absence.degreePercent === undefined ? undefined : t("absenceDegree", { percent: number(absence.degreePercent) }),
    ].filter((one): one is string => one !== undefined);
};
