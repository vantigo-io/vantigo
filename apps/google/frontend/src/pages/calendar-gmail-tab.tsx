import { IconCalendar } from "@tabler/icons-react";
import { EmptyState, PageSection, useI18n } from "@vantigo/frontend-shell";
import { MailChannelsCard } from "../components/mail-channels";
import { PeopleConnectionCard } from "../components/people-connection";
import "../i18n";
import { TabFrame } from "./tab-frame";

/**
 * The **Calendar and Gmail** tab: Connection for people — the switch that
 * lets each person connect their own Google account, its status, the counts
 * and the redirect URI —, Mail channels — the switch that lets
 * Communications' channels use the organisation's Gmail mailboxes, and its
 * scopes — and, underneath, sending from a person's Gmail still to come.
 */
export function GoogleCalendarGmailTab() {
  const { t } = useI18n("google");
  return (
    <TabFrame>
      {({ connection, owner }) => (
        <>
          <PeopleConnectionCard connection={connection} owner={owner} />
          <MailChannelsCard connection={connection} owner={owner} />
          <PageSection>
            <EmptyState
              icon={IconCalendar}
              title={t("calendarGmailComingTitle")}
              description={t("calendarGmailComing")}
            />
          </PageSection>
        </>
      )}
    </TabFrame>
  );
}
