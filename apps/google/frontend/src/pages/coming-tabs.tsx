import { Card } from "@mantine/core";
import { IconCalendar, IconUsers } from "@tabler/icons-react";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import "../i18n";

/**
 * The **Accounts** tab: a route from the start so links never move; the
 * accounts, the groups and the sign-in come in later deliveries.
 */
export function GoogleAccountsTab() {
  const { t } = useI18n("google");
  return (
    <Card withBorder radius="md" padding="lg">
      <EmptyState icon={IconUsers} title={t("accountsComingTitle")} description={t("accountsComing")} />
    </Card>
  );
}

/**
 * The **Calendar and Gmail** tab: a route from the start so links never
 * move; each person's Google connection, the calendar sync and Gmail come in
 * later deliveries.
 */
export function GoogleCalendarGmailTab() {
  const { t } = useI18n("google");
  return (
    <Card withBorder radius="md" padding="lg">
      <EmptyState icon={IconCalendar} title={t("calendarGmailComingTitle")} description={t("calendarGmailComing")} />
    </Card>
  );
}
