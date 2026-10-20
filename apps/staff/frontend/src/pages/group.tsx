import { Alert, Badge, Button, Group, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconArchive, IconPencil, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  archiveGroup,
  deleteGroup,
  groupQueryOptions,
  groupTraitsQueryOptions,
  type StaffGroup,
  type StaffGroupProvider,
} from "../api/groups";
import { personQueryOptions } from "../api/people";
import { NotFoundError, STAFF_QUERY_KEY } from "../api/request";
import { structureQueryOptions } from "../api/structure";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { GROUPS_ROUTE_PATH } from "../lib/routes";
import { GroupFormModal } from "./-group-form-modal";
import { GroupMembers, GroupOwners } from "./-group-members";
import { GroupProviders } from "./-group-traits";

/** Whether a provider ever made or found the group: then it is archived, never deleted (`group_synced`). */
const everSynced = (providers: StaffGroupProvider[]) =>
  providers.some(
    (provider) =>
      provider.syncedAt !== undefined ||
      provider.externalId !== undefined ||
      provider.state === "synced" ||
      provider.state === "retired" ||
      provider.state === "deleted",
  );

/**
 * One group (Entra design D9, D10, D14): the header with its kind and its
 * rule in words, *Edit*, *Archive* with a confirmation and *Delete* while no
 * provider ever made it; the members — HR's for a listed group, the rule's,
 * read-only, for a derived one — the owners, and one card per provider in
 * the slot with the traits it declares. An archived group is read-only.
 */
export const GroupPage = () => {
  const { t } = useI18n("staff");
  const { id } = useParams({ strict: false }) as { id: string };
  const groupId = Number(id);
  const navigate = useNavigate() as (options: unknown) => void;
  const queryClient = useQueryClient();
  const group = useQuery({ ...groupQueryOptions(groupId), retry: false });
  const traits = useQuery({ ...groupTraitsQueryOptions(groupId), enabled: group.isSuccess });
  const [editing, setEditing] = useState(false);

  const archive = useMutation({
    mutationFn: () => archiveGroup(groupId),
    onSuccess: async (saved) => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("archivedNotice", { name: saved.name }), message: "" });
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("couldNotArchive", { name: group.data?.name ?? "" }),
        message: refusalMessage(error),
      }),
  });
  const remove = useMutation({
    mutationFn: () => deleteGroup(groupId),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("groupDeleted", { name: group.data?.name ?? "" }), message: "" });
      navigate({ to: GROUPS_ROUTE_PATH });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY, "groups"] });
    },
    // group_synced: a provider made it since the page was read; the server's sentence says to archive.
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotDeleteGroup"), message: refusalMessage(error) }),
  });

  if (group.error instanceof NotFoundError)
    return <EmptyState title={t("groupNotFound")} description={t("groupNotFoundHint")} />;
  if (group.isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadGroup")}>
        {group.error.message}
      </Alert>
    );
  if (!group.data) return <ContentSkeleton rows={6} rowHeight={44} />;
  const data = group.data;
  const archived = Boolean(data.archivedAt);
  const deletable = traits.data !== undefined && !everSynced(traits.data.providers);

  const confirmArchive = () =>
    modals.openConfirmModal({
      title: t("archiveTitle", { name: data.name }),
      children: <Text size="sm">{t("archiveGroupExplained")}</Text>,
      labels: { confirm: t("archive"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => archive.mutate(),
    });
  const confirmDelete = () =>
    modals.openConfirmModal({
      title: t("deleteGroupTitle", { name: data.name }),
      children: <Text size="sm">{t("deleteGroupExplained")}</Text>,
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("navGroups"), to: GROUPS_ROUTE_PATH }, { label: data.name }]}
        title={
          <Group gap="xs" component="span">
            {data.name}
            {archived && (
              <Badge variant="light" color="gray">
                {t("archived")}
              </Badge>
            )}
          </Group>
        }
        description={
          <Stack gap={2} component="span">
            <Text component="span" size="sm">
              {t(`groupKind_${data.kind}`)} · {data.key}
              {data.kind === "derived" && data.rule ? (
                <>
                  {" · "}
                  <RuleWords group={data} />
                </>
              ) : null}
            </Text>
            {data.description && (
              <Text component="span" size="sm" c="dimmed">
                {data.description}
              </Text>
            )}
          </Stack>
        }
        actions={
          <Group gap="xs">
            {!archived && (
              <Button variant="light" leftSection={<IconPencil size={16} />} onClick={() => setEditing(true)}>
                {t("editGroup")}
              </Button>
            )}
            {!archived && (
              <Button
                variant="light"
                color="red"
                leftSection={<IconArchive size={16} />}
                loading={archive.isPending}
                onClick={confirmArchive}
              >
                {t("archive")}
              </Button>
            )}
            {deletable && (
              <Button
                variant="subtle"
                color="red"
                leftSection={<IconTrash size={16} />}
                loading={remove.isPending}
                onClick={confirmDelete}
              >
                {t("delete")}
              </Button>
            )}
          </Group>
        }
      />
      {archived && <Alert color="gray">{t("groupArchivedReadOnly")}</Alert>}
      <GroupMembers group={data} />
      <GroupOwners group={data} />
      {traits.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadGroupProviders")}>
          {traits.error.message}
        </Alert>
      )}
      {traits.isPending && <ContentSkeleton rows={3} rowHeight={44} />}
      {traits.data && <GroupProviders group={data} providers={traits.data.providers} />}
      <GroupFormModal opened={editing} group={data} onClose={() => setEditing(false)} />
    </Stack>
  );
};

/** The rule in words: its kind, and the row it names by name — a department beneath it too when asked. */
export const RuleWords = ({ group }: { group: StaffGroup }) => {
  const { t } = useI18n("staff");
  const rule = group.rule;
  const table =
    rule?.kind === "department"
      ? "departments"
      : rule?.kind === "businessUnit"
        ? "business-units"
        : rule?.kind === "location"
          ? "locations"
          : undefined;
  const rows = useQuery({ ...structureQueryOptions(table ?? "departments"), enabled: table !== undefined });
  const person = useQuery({
    ...personQueryOptions(rule?.id ?? 0),
    enabled: rule?.kind === "reportsTo" && rule.id !== undefined,
  });
  if (!rule) return null;
  if (rule.kind === "employed") return <>{t("groupRuleWords_employed")}</>;
  if (rule.kind === "employeeType")
    return (
      <>
        {t("groupRuleWords_employeeType", {
          type: rule.value ? t(`employeeType_${rule.value}`) : t("notSet"),
        })}
      </>
    );
  const name =
    rule.kind === "reportsTo"
      ? (person.data?.displayName ?? (person.isError ? t("personById", { id: rule.id }) : t("loadingName")))
      : ((rows.data as { id: number; name: string }[] | undefined)?.find((row) => row.id === rule.id)?.name ??
        t("loadingName"));
  if (rule.kind === "department" && rule.includeBelow) return <>{t("groupRuleWords_departmentBelow", { name })}</>;
  return <>{t(`groupRuleWords_${rule.kind}`, { name })}</>;
};
