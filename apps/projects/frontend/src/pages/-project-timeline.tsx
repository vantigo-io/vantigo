import { Alert, Card, Group, Pagination, Stack, Text, Timeline } from "@mantine/core";
import { IconAlertCircle, IconHistory } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { projectTimelineQueryOptions, type TimelineEntry } from "../api/projects";
import "../i18n";
import { isProjectRole, projectRoleLabelKey } from "../lib/roles";
import { isProjectStatus, projectStatusLabelKey } from "../lib/status";

type Translate = (key: string, values?: Record<string, unknown>) => string;

/** The generated payload type is an empty record; every event reads its own fields out of it. */
const payloadOf = (entry: TimelineEntry): Record<string, unknown> =>
  (entry.payload ?? {}) as unknown as Record<string, unknown>;

const text = (value: unknown): string => (value === null || value === undefined ? "" : String(value));

const statusLabel = (t: Translate, value: unknown): string => {
  const status = text(value);
  return isProjectStatus(status) ? t(projectStatusLabelKey(status)) : status;
};

const roleLabel = (t: Translate, value: unknown): string => {
  const role = text(value);
  return isProjectRole(role) ? t(projectRoleLabelKey(role)) : role;
};

/**
 * The catalog key each changed field is named by. A field the backend adds
 * later falls through to its own name rather than an empty string, so a new
 * event stays legible until it gets a translation.
 */
const fieldKeys: Record<string, string> = {
  name: "name",
  code: "code",
  customerId: "customer",
  description: "description",
  startDate: "startDate",
  endDate: "endDate",
  budgetHours: "budgetHours",
  billingType: "billingType",
  fixedPriceAmount: "fixedPriceAmount",
  budgetAmount: "budgetAmount",
  currency: "currency",
  variantId: "productVariant",
  pricingMode: "pricingMode",
  fixedAmount: "fixedAmount",
  discountPercent: "discountPercent",
};

const fieldList = (t: Translate, value: unknown): string =>
  (Array.isArray(value) ? value : [])
    .map((field) => {
      const name = text(field);
      const key = fieldKeys[name];
      return key ? t(key) : name;
    })
    .join(", ");

/**
 * One line per entry (design §8.2). Every event type the backend writes has
 * its own sentence; an event this build does not know is shown by its raw
 * type, which is still more use than nothing.
 */
const describeEntry = (t: Translate, entry: TimelineEntry): string => {
  const payload = payloadOf(entry);
  switch (entry.eventType) {
    case "project-created":
      return t("timelineProjectCreated", { code: text(payload.code) });
    case "code-changed":
      return t("timelineCodeChanged", { old: text(payload.old), new: text(payload.new) });
    case "status-changed":
      return t("timelineStatusChanged", { old: statusLabel(t, payload.old), new: statusLabel(t, payload.new) });
    case "details-changed":
      return t("timelineDetailsChanged", { fields: fieldList(t, payload.fields) });
    case "customer-changed":
      return t("timelineCustomerChanged");
    case "billing-changed":
      return t("timelineBillingChanged", { fields: fieldList(t, payload.fields) });
    case "role-added":
      return t("timelineRoleAdded", { name: text(payload.displayName), role: roleLabel(t, payload.role) });
    case "role-changed":
      return t("timelineRoleChanged", {
        name: text(payload.displayName),
        oldRole: roleLabel(t, payload.oldRole),
        newRole: roleLabel(t, payload.newRole),
      });
    case "role-removed":
      return t("timelineRoleRemoved", { name: text(payload.displayName), role: roleLabel(t, payload.role) });
    case "line-added":
      return t("timelineLineAdded", { code: text(payload.code) });
    case "line-changed":
      return t("timelineLineChanged", { code: text(payload.code), fields: fieldList(t, payload.fields) });
    case "line-deactivated":
      return t("timelineLineDeactivated", { code: text(payload.code) });
    case "line-reactivated":
      return t("timelineLineReactivated", { code: text(payload.code) });
    default:
      return entry.eventType;
  }
};

/**
 * The project's timeline: read-only, newest first, one page at a time. The
 * entries carry no amounts (D12), so everyone who can see the project sees
 * the same timeline and nothing here is shaped per reader.
 */
export const ProjectTimeline = ({ projectId }: { projectId: number }) => {
  const { t, formatters } = useI18n("projects");
  const [page, setPage] = useState(1);
  const { data, isPending, isError } = useQuery(projectTimelineQueryOptions(projectId, page));
  const entries = data?.data ?? [];

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="md">
        <Group gap="xs">
          <IconHistory size={18} aria-hidden="true" />
          <Text fw={600} component="h3">
            {t("timeline")}
          </Text>
        </Group>

        {isError && <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadTimeline")} />}
        {isPending && <ContentSkeleton rows={4} rowHeight={40} />}
        {data && entries.length === 0 && <EmptyState title={t("noTimelineEntries")} size="sm" />}

        {entries.length > 0 && (
          <Timeline active={-1} bulletSize={14} lineWidth={2}>
            {entries.map((entry) => (
              <Timeline.Item key={entry.id} title={<Text size="sm">{describeEntry(t, entry)}</Text>}>
                <Text size="xs" c="dimmed">
                  {entry.actorDisplay}
                  {" · "}
                  {formatters.formatDate(entry.occurredAt, { dateStyle: "medium", timeStyle: "short" })}
                </Text>
              </Timeline.Item>
            ))}
          </Timeline>
        )}

        {data && data.pagination.totalPages > 1 && (
          <Group justify="center">
            <Pagination total={data.pagination.totalPages} value={page} onChange={setPage} />
          </Group>
        )}
      </Stack>
    </Card>
  );
};
