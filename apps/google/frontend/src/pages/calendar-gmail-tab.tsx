import { Card } from "@mantine/core";
import { IconCalendar } from "@tabler/icons-react";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import { PeopleConnectionCard } from "../components/people-connection";
import "../i18n";
import { TabFrame } from "./tab-frame";

/**
 * The **Calendar and Gmail** tab: Connection for people — the switch that
 * lets each person connect their own Google account, its status, the counts
 * and the redirect URI — and, underneath, the calendar sync and Gmail still
 * to come.
 */
export function GoogleCalendarGmailTab() {
  const { t } = useI18n("google");
  return (
    <TabFrame>
      {({ connection, owner }) => (
        <>
          <PeopleConnectionCard connection={connection} owner={owner} />
          <Card withBorder radius="md" padding="lg">
            <EmptyState
              icon={IconCalendar}
              title={t("calendarGmailComingTitle")}
              description={t("calendarGmailComing")}
            />
          </Card>
        </>
      )}
    </TabFrame>
  );
}
