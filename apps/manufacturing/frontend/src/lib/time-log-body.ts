import dayjs from "dayjs";
import type { ManufacturingTimeLog } from "../api/shop-floor";
import { isDecimal, normalizeDecimal } from "./decimal";

/** The longest note a correction or an added log takes (M29). */
export const TIME_LOG_NOTE_MAX = 2000;

/** What the correction and the add dialogs hold, as typed. */
export interface TimeLogValues {
  /** `YYYY-MM-DDTHH:mm` in the viewer's time, as a datetime-local input holds it. */
  startedAt: string;
  endedAt: string;
  quantity: string;
  note: string;
}

/** Which rule a field breaks; the dialog says it in the catalog's words (`problem_<rule>`). */
export type TimeLogProblem = "required" | "endBeforeStart" | "endInFuture" | "quantity" | "noteTooLong";

const localTime = (instant: string | null) => (instant ? dayjs(instant).format("YYYY-MM-DDTHH:mm") : "");

/** A log's values to correct: its times, its quantity, and an empty note, since a correction says why. */
export const timeLogValuesOf = (log?: ManufacturingTimeLog): TimeLogValues => ({
  startedAt: localTime(log?.startedAt ?? null),
  endedAt: localTime(log?.endedAt ?? null),
  quantity: log?.quantity ?? "",
  note: "",
});

/**
 * The rules the dialogs check before sending (M29): both times, the end after
 * the start and not in the future, a quantity that is a decimal when given,
 * and a note of 1–2000 characters. The server checks them again, and the
 * overlap with the person's other logs (`log_overlaps`) only it can.
 */
export const timeLogProblems = (
  values: TimeLogValues,
  now: number = Date.now(),
): Partial<Record<keyof TimeLogValues, TimeLogProblem>> => {
  const problems: Partial<Record<keyof TimeLogValues, TimeLogProblem>> = {};
  if (!values.startedAt) problems.startedAt = "required";
  if (!values.endedAt) problems.endedAt = "required";
  else if (new Date(values.endedAt).getTime() > now) problems.endedAt = "endInFuture";
  else if (values.startedAt && values.endedAt <= values.startedAt) problems.endedAt = "endBeforeStart";
  if (values.quantity.trim() !== "" && !isDecimal(values.quantity)) problems.quantity = "quantity";
  if (values.note.trim() === "") problems.note = "required";
  else if (values.note.length > TIME_LOG_NOTE_MAX) problems.note = "noteTooLong";
  return problems;
};

/** The times and the note as the contract takes them; an empty quantity is none. */
export const timeLogBody = (values: TimeLogValues) => ({
  startedAt: new Date(values.startedAt).toISOString(),
  endedAt: new Date(values.endedAt).toISOString(),
  note: values.note.trim(),
  quantity: values.quantity.trim() === "" ? null : normalizeDecimal(values.quantity),
});
