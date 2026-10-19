import { getTimeZones } from "@vantigo/frontend-shell";
import type { Participant } from "../api/conversations";

/** A message's instant in the viewer's time zone (time zones design D8), as the shell holds it. */
export const relative = (date: string) =>
  new Intl.DateTimeFormat(undefined, {
    dateStyle: "short",
    timeStyle: "short",
    timeZone: getTimeZones().timeZone,
  }).format(new Date(date));

export const participantName = (item: Pick<Participant, "displayName" | "address">) => item.displayName || item.address;
