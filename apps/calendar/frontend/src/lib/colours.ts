/** The kind of an appointment's item on `/items`. */
/** The kind of an Outlook busy block on `/items` (Outlook sync design O9.5). */
export const OUTLOOK_KIND = "calendar.outlook";
export const APPOINTMENT_KIND = "calendar.appointment";
/** The kind of a subscribed calendar's item on `/items` (feed design D8). */
export const SUBSCRIPTION_KIND = "calendar.subscription";
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
  planning: "indigo",
  holidays: "gray",
};

/** The module a kind comes from: kinds are `<module>.<noun>`. */
export const moduleOfKind = (kind: string): string => kind.split(".")[0];

export const colourForModule = (module: string): string => `var(--vantigo-calendar-${moduleColours[module] ?? "gray"})`;

/** A kind with a colour of its own, apart from its module's: Outlook's busy time is not an appointment. */
const kindColours: Record<string, string> = {
  [OUTLOOK_KIND]: "cyan",
};

export const colourForKind = (kind: string): string =>
  kindColours[kind] ? `var(--vantigo-calendar-${kindColours[kind]})` : colourForModule(moduleOfKind(kind));

/** The catalog key of a kind's name; a kind no catalog names reads as itself. */
export const kindLabelKey = (kind: string): string => `kind_${kind.replace(/[.-]/g, "_")}`;

/**
 * The palette a subscription is drawn in (feed design D9): twelve Mantine
 * hues, in the order the picker offers them, each a scheme-aware
 * `--vantigo-calendar-<name>` variable.
 */
export const SUBSCRIPTION_COLOURS = [
  "blue",
  "cyan",
  "teal",
  "green",
  "lime",
  "yellow",
  "orange",
  "red",
  "pink",
  "grape",
  "violet",
  "indigo",
] as const;

export type SubscriptionColour = (typeof SUBSCRIPTION_COLOURS)[number];

/** A palette name as the colour it draws; a name off the palette (a newer server) is grey. */
export const colourForName = (name: string): string =>
  `var(--vantigo-calendar-${(SUBSCRIPTION_COLOURS as readonly string[]).includes(name) ? name : "gray"})`;
