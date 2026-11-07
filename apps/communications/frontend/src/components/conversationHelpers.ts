import { getTimeZones, type useI18n } from "@vantigo/frontend-shell";
import type { ConversationListItem, Participant, PersonalSender } from "../api/conversations";

/** The width an e-mail's HTML is laid out at when the space is narrower, and the height of the frame then. */
export const EMAIL_DESIGN_WIDTH = 600;
export const FIT_FRAME_HEIGHT = 320;

/** The scale that fits an e-mail laid out at 600 px into `containerWidth`: 1 where it fits or the width is not known yet. */
export const fitScale = (containerWidth: number) =>
  containerWidth > 0 && containerWidth < EMAIL_DESIGN_WIDTH ? containerWidth / EMAIL_DESIGN_WIDTH : 1;

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

/** The search box matches the subject, the preview and the participants of the conversations already loaded. */
export const matchesSearch = (item: ConversationListItem, query: string) => {
  const needle = query.trim().toLowerCase();
  if (!needle) return true;
  return [item.subject ?? "", item.previewText ?? "", ...item.participants.map(participantName)].some((text) =>
    text.toLowerCase().includes(needle),
  );
};
