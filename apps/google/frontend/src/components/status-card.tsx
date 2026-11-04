import { Alert, Badge, List, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { eventsQueryOptions, type GoogleConnection, type GoogleStatus } from "../api/connection";
import { actionText, codeText, reasonText } from "../lib/words";
import { Fact, SectionCard } from "./step-card";

const statusColor: Record<GoogleStatus, string> = {
  unconfigured: "gray",
  pending_delegation: "yellow",
  connected: "teal",
  failing: "red",
};

/** A status as a badge in the reader's words. */
export const StatusBadge = ({ status }: { status: GoogleStatus }) => {
  const { t } = useI18n("google");
  return (
    <Badge color={statusColor[status]} size="lg" variant={status === "unconfigured" ? "outline" : "filled"}>
      {t(`statusName.${status}`)}
    </Badge>
  );
};

/** Why the status is what it is, as a sentence. */
export const ReasonAlert = ({ status, reason }: { status: GoogleStatus; reason?: string }) => {
  const { t } = useI18n("google");
  if (!reason) return null;
  return (
    <Alert color={status === "failing" ? "red" : "yellow"} variant="light" role="status">
      {reasonText(reason, t)}
    </Alert>
  );
};

const Subheading = ({ children }: { children: string }) => (
  <Title order={4} fz="h6" mt="xs">
    {children}
  </Title>
);

/**
 * The scopes as the last test found them: those granted, those Vantigo needs
 * and was refused, and those it never uses but was granted anyway — a
 * warning, never a status: delegation reaches every account, so a scope
 * nobody needs is better removed.
 */
export const ScopeLists = ({
  granted,
  missing,
  unexpected,
}: {
  granted: string[];
  missing: string[];
  unexpected: string[];
}) => {
  const { t } = useI18n("google");
  return (
    <Stack gap="xs">
      {missing.length === 0 ? (
        granted.length > 0 && <Text size="sm">{t("scopesAllGranted")}</Text>
      ) : (
        <Stack gap={4}>
          <Text size="sm" c="red">
            {t("scopesMissingList", { count: missing.length })}
          </Text>
          <List size="sm" aria-label={t("scopesMissing")}>
            {missing.map((name) => (
              <List.Item key={name}>{name}</List.Item>
            ))}
          </List>
        </Stack>
      )}
      {granted.length > 0 && (
        <Stack gap={4}>
          <Text size="sm" c="dimmed">
            {t("scopesGranted")}
          </Text>
          <List size="sm" aria-label={t("scopesGranted")}>
            {granted.map((name) => (
              <List.Item key={name}>{name}</List.Item>
            ))}
          </List>
        </Stack>
      )}
      {unexpected.length > 0 && (
        <Alert color="orange" variant="light" title={t("scopesUnexpected")}>
          <Stack gap={4}>
            <List size="sm" aria-label={t("scopesUnexpected")}>
              {unexpected.map((name) => (
                <List.Item key={name}>{name}</List.Item>
              ))}
            </List>
            <Text size="sm">{t("scopesUnexpectedHint")}</Text>
          </Stack>
        </Alert>
      )}
    </Stack>
  );
};

/**
 * The **Status** card (Google Workspace design G16): the status and its
 * reason, the organisation as the last test read it, when it was tested,
 * and the scopes.
 */
export const StatusCard = ({ connection }: { connection: GoogleConnection }) => {
  const { t, formatters } = useI18n("google");
  const dateTime = (value: string) =>
    formatters.formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" });
  const tested = connection.lastCheckedAt !== undefined;

  return (
    <SectionCard title={t("status")} badge={<StatusBadge status={connection.status} />}>
      <ReasonAlert status={connection.status} reason={connection.statusReason} />
      <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }}>
        <Fact label={t("organisation")} value={connection.organisationName ?? t("notReadYet")} />
        <Fact label={t("customerId")} value={connection.customerId ?? t("notReadYet")} />
        <Fact label={t("primaryDomain")} value={connection.primaryDomain ?? t("notReadYet")} />
        <Fact
          label={t("lastChecked")}
          value={connection.lastCheckedAt ? dateTime(connection.lastCheckedAt) : t("notTestedYet")}
        />
      </SimpleGrid>
      <Fact
        label={t("verifiedDomains")}
        value={connection.domains.length > 0 ? connection.domains.join(", ") : t("notReadYet")}
      />

      <Subheading>{t("scopes")}</Subheading>
      {tested ? (
        <ScopeLists
          granted={connection.scopesGranted}
          missing={connection.scopesMissing}
          unexpected={connection.scopesUnexpected}
        />
      ) : (
        <Text size="sm" c="dimmed">
          {t("scopesNotTested")}
        </Text>
      )}
    </SectionCard>
  );
};

/** The module's recent writes, newest first: what, when, by whom, and the codes it reported. */
export const EventsCard = () => {
  const { t, formatters } = useI18n("google");
  const events = useQuery(eventsQueryOptions());
  const dateTime = (value: string) =>
    formatters.formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" });

  return (
    <SectionCard title={t("recentEvents")}>
      {events.isPending && <ContentSkeleton rows={2} rowHeight={24} />}
      {events.isError && (
        <Text size="sm" c="red">
          {t("eventsFailed")}
        </Text>
      )}
      {events.data && events.data.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("noEvents")}
        </Text>
      )}
      {events.data && events.data.length > 0 && (
        <Stack
          gap={6}
          component="ul"
          aria-label={t("recentEvents")}
          style={{ listStyle: "none", padding: 0, margin: 0 }}
        >
          {events.data.map((event) => (
            <Stack key={event.id} component="li" gap={0}>
              <Text size="sm">
                {actionText(event.action, t)}
                {event.actor.startsWith("system:") ? ` · ${t("bySystem")}` : ""}
              </Text>
              <Text size="xs" c="dimmed">
                {dateTime(event.at)}
              </Text>
              {event.codes.length > 0 && (
                <Text size="xs" c="orange">
                  {event.codes.map((code) => codeText(code, t)).join("; ")}
                </Text>
              )}
            </Stack>
          ))}
        </Stack>
      )}
    </SectionCard>
  );
};
