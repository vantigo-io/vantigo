import type { Participant } from "../api/conversations";

export const relative = (date: string) =>
  new Intl.DateTimeFormat(undefined, { dateStyle: "short", timeStyle: "short" }).format(new Date(date));

export const participantName = (item: Pick<Participant, "displayName" | "address">) => item.displayName || item.address;
