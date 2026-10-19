/** The kind of an appointment's item on `/items`. */
export const APPOINTMENT_KIND = "calendar.appointment";
/** The legend's chip for the public holidays, which are not items but drawn behind them. */
export const HOLIDAYS_KIND = "holidays";

/**
 * One colour per module (design D8), each a scheme-aware
 * `--vantigo-calendar-*` variable from `styles/fullcalendar.css`.
 */
const moduleColours: Record<string, string> = {
  calendar: "primary",
  customers: "blue",
  staff: "teal",
  projects: "grape",
  time: "green",
  expenses: "orange",
  holidays: "gray",
};

/** The module a kind comes from: kinds are `<module>.<noun>`. */
export const moduleOfKind = (kind: string): string => kind.split(".")[0];

export const colourForModule = (module: string): string => `var(--vantigo-calendar-${moduleColours[module] ?? "gray"})`;

export const colourForKind = (kind: string): string => colourForModule(moduleOfKind(kind));

/** The catalog key of a kind's name; a kind no catalog names reads as itself. */
export const kindLabelKey = (kind: string): string => `kind_${kind.replace(/[.-]/g, "_")}`;
