import { Card } from "@mantine/core";
import { IconCalendar } from "@tabler/icons-react";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import "../i18n";

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
