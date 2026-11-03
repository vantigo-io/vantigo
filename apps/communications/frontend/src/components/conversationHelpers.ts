import { getTimeZones, type useI18n } from "@vantigo/frontend-shell";
import type { Participant, PersonalSender } from "../api/conversations";

/** A message's instant in the viewer's time zone (time zones design D8), as the shell holds it. */
export const relative = (date: string) =>
  new Intl.DateTimeFormat(undefined, {
    dateStyle: "short",
    timeStyle: "short",
    timeZone: getTimeZones().timeZone,
  }).format(new Date(date));

export const participantName = (item: Pick<Participant, "displayName" | "address">) => item.displayName || item.address;

/**
 * Where a connect started from the inbox returns to: the page as it is, its selected conversation and
 * filters in the search, so the person lands back where they chose to connect — never a bare inbox.
 */
export const returnHere = () =>
  typeof window === "undefined" ? "/communications/inbox" : `${window.location.pathname}${window.location.search}`;

/**
 * The composer's "Send from my address" label: the address, and the personal mailbox provider's name
 * ("Outlook") when the server answers one (Google Workspace design G4).
 */
export const personalSenderLabel = (
  t: ReturnType<typeof useI18n>["t"],
  sender: Pick<PersonalSender, "address" | "providerDisplayName">,
) =>
  sender.providerDisplayName
    ? t("sendFromPersonalVia", { provider: sender.providerDisplayName, address: sender.address ?? "" })
    : t("sendFromPersonal", { address: sender.address ?? "" });
