import { MailChannelsCard } from "../components/mail-channels";
import { PeopleConnectionCard } from "../components/people-connection";
import "../i18n";
import { TabFrame } from "./tab-frame";

/**
 * The **Calendar and Gmail** tab: Connection for people — the switch that
 * lets each person connect their own Google account, its status, the counts
 * and the redirect URI —, Mail channels — the switch that lets
 * Communications' channels use the organisation's Gmail mailboxes, and its
 * scopes. Sending from a person's own Gmail address needs nothing more here:
 * it rides on each person's connection, with the Gmail feature.
 */
export function GoogleCalendarGmailTab() {
  return (
    <TabFrame>
      {({ connection, owner }) => (
        <>
          <PeopleConnectionCard connection={connection} owner={owner} />
          <MailChannelsCard connection={connection} owner={owner} />
        </>
      )}
    </TabFrame>
  );
}
