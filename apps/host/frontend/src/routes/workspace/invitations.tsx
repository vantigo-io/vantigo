import {
  Alert,
  Avatar,
  Badge,
  Button,
  Card,
  Group,
  Menu,
  SegmentedControl,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconDots, IconMailForward, IconPlus, IconUserPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, redirect } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useMemo, useState } from "react";
import { type Invitation, invitationAction, listInvitations } from "../../api/account-lifecycle";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { translateHostRole } from "../../i18n";
import "../../i18n";

const status = (item: Invitation) =>
  item.revokedAt
    ? "revoked"
    : item.acceptedAt
      ? "accepted"
      : new Date(item.expiresAt) <= new Date()
        ? "expired"
        : "pending";
const colors = { pending: "blue", expired: "orange", revoked: "gray", accepted: "teal" } as const;
const statusLabel = (value: ReturnType<typeof status>, t: (key: string) => string) =>
  value === "pending"
    ? t("common.pending")
    : value === "expired"
      ? t("common.expired")
      : value === "revoked"
        ? t("common.revoked")
        : t("common.accepted");
const InvitationsPage = () => {
  const { t, formatters } = useI18n("host");
  const qc = useQueryClient();
  const query = useQuery({ queryKey: ["owner-invitations"], queryFn: listInvitations, refetchInterval: 30000 });
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState("all");
  const action = useMutation({
    mutationFn: ({ id, kind }: { id: string; kind: "revoke" | "resend" }) => invitationAction(id, kind),
    onSuccess: (_, v) => {
      void qc.invalidateQueries({ queryKey: ["owner-invitations"] }).then(() =>
        notifications.show({
          title: v.kind === "revoke" ? t("admin.invitationRevoked") : t("admin.invitationResent"),
          message: t("admin.invitationListUpdated"),
          color: "teal",
        }),
      );
    },
    onError: (error, v) =>
      notifications.show({
        title: v.kind === "revoke" ? t("admin.invitationRevokeFailed") : t("admin.invitationResendFailed"),
        message: error instanceof Error ? error.message : t("common.tryAgain"),
        color: "red",
      }),
  });
  const items = useMemo(
    () =>
      (query.data ?? []).filter(
        (i) =>
          `${i.email} ${i.displayName ?? ""}`.toLowerCase().includes(search.toLowerCase()) &&
          (filter === "all" || status(i) === filter),
      ),
    [query.data, search, filter],
  );
  const act = (i: Invitation, kind: "revoke" | "resend") =>
    kind === "revoke"
      ? modals.openConfirmModal({
          title: t("admin.revokeInvitationQuestion"),
          children: <Text size="sm">{t("admin.invitationWillNotWork", { email: i.email })}</Text>,
          labels: { confirm: t("admin.revoke"), cancel: t("common.cancel") },
          confirmProps: { color: "red" },
          onConfirm: () => action.mutate({ id: i.id, kind }),
        })
      : action.mutate({ id: i.id, kind });
  return (
    <Stack gap="xl">
      <PageHeader
        title={t("admin.invitations")}
        description={t("admin.invitationsDescriptionShort")}
        actions={
          <Button component={Link} to="/workspace/users" leftSection={<IconPlus size={16} />}>
            {t("admin.inviteSomeone")}
          </Button>
        }
      />
      <Card withBorder radius="md" p={0} style={{ overflow: "hidden" }}>
        <Group p="md">
          <TextInput
            label={t("admin.searchInvitations")}
            placeholder={t("admin.searchNameEmail")}
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
            style={{ flex: "1 1 280px" }}
          />
          <SegmentedControl
            value={filter}
            onChange={setFilter}
            style={{ flex: "1 1 360px" }}
            data={[
              { value: "all", label: t("common.all") },
              { value: "pending", label: t("common.pending") },
              { value: "expired", label: t("common.expired") },
              { value: "revoked", label: t("common.revoked") },
              { value: "accepted", label: t("common.accepted") },
            ]}
          />
        </Group>
        {query.isError ? (
          <Alert m="md" color="red" title={t("admin.invitationLoadFailed")}>
            <Button size="compact-sm" variant="light" onClick={() => void query.refetch()}>
              {t("common.tryAgain")}
            </Button>
          </Alert>
        ) : query.isPending ? (
          <ContentSkeleton rows={3} rowHeight={52} p="md" />
        ) : items.length === 0 ? (
          <EmptyState icon={IconUserPlus} title={t("admin.noInvitationFilters")} />
        ) : (
          <>
            <div className="users-desktop-table">
              <Table verticalSpacing="md" highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("admin.recipient")}</Table.Th>
                    <Table.Th>{t("common.role")}</Table.Th>
                    <Table.Th>{t("admin.status")}</Table.Th>
                    <Table.Th>{t("admin.createdExpires")}</Table.Th>
                    <Table.Th />
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {items.map((i) => (
                    <Table.Tr key={i.id}>
                      <Table.Td>
                        <Group gap="sm">
                          <Avatar name={i.displayName || i.email} color="initials" />
                          <div>
                            <Text fw={600}>{i.displayName || t("admin.unnamedUser")}</Text>
                            <Text size="sm" c="dimmed">
                              {i.email}
                            </Text>
                          </div>
                        </Group>
                      </Table.Td>
                      <Table.Td>
                        <Badge variant="light">{translateHostRole(i.role, t)}</Badge>
                      </Table.Td>
                      <Table.Td>
                        <Badge color={colors[status(i)]} variant="dot">
                          {statusLabel(status(i), t)}
                        </Badge>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm">
                          {formatters.formatDate(new Date(i.createdAt), { dateStyle: "medium" })} ·{" "}
                          {formatters.formatDate(new Date(i.expiresAt), { dateStyle: "medium" })}
                        </Text>
                      </Table.Td>
                      <Table.Td ta="right">
                        <Menu>
                          <Menu.Target>
                            <Button
                              variant="subtle"
                              size="compact-sm"
                              aria-label={t("admin.actionsFor", { user: i.email })}
                            >
                              <IconDots size={18} />
                            </Button>
                          </Menu.Target>
                          <Menu.Dropdown>
                            <Menu.Item
                              leftSection={<IconMailForward size={15} />}
                              onClick={() => act(i, "resend")}
                              disabled={action.isPending || status(i) === "accepted" || status(i) === "revoked"}
                            >
                              {t("admin.resend")}
                            </Menu.Item>
                            <Menu.Item
                              color="red"
                              onClick={() => act(i, "revoke")}
                              disabled={action.isPending || status(i) === "accepted" || status(i) === "revoked"}
                            >
                              {t("admin.revoke")}
                            </Menu.Item>
                          </Menu.Dropdown>
                        </Menu>
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </div>
            <SimpleGrid className="users-mobile-list" p="md">
              {items.map((i) => (
                <Card key={i.id} withBorder>
                  <Group justify="space-between">
                    <Group gap="sm">
                      <Avatar name={i.displayName || i.email} color="initials" />
                      <div>
                        <Text fw={600}>{i.displayName || t("admin.unnamedUser")}</Text>
                        <Text size="sm" c="dimmed">
                          {i.email}
                        </Text>
                      </div>
                    </Group>
                    <Menu>
                      <Menu.Target>
                        <Button
                          variant="subtle"
                          size="compact-sm"
                          aria-label={t("admin.actionsForInvitation", { email: i.email })}
                        >
                          <IconDots size={18} />
                        </Button>
                      </Menu.Target>
                      <Menu.Dropdown>
                        <Menu.Item
                          onClick={() => act(i, "resend")}
                          disabled={action.isPending || status(i) === "accepted" || status(i) === "revoked"}
                        >
                          {t("admin.resend")}
                        </Menu.Item>
                        <Menu.Item
                          color="red"
                          onClick={() => act(i, "revoke")}
                          disabled={action.isPending || status(i) === "accepted" || status(i) === "revoked"}
                        >
                          {t("admin.revoke")}
                        </Menu.Item>
                      </Menu.Dropdown>
                    </Menu>
                  </Group>
                  <Group mt="md">
                    <Badge variant="light">{translateHostRole(i.role, t)}</Badge>
                    <Badge color={colors[status(i)]} variant="dot">
                      {statusLabel(status(i), t)}
                    </Badge>
                  </Group>
                </Card>
              ))}
            </SimpleGrid>
          </>
        )}
      </Card>
    </Stack>
  );
};
export const Route = createFileRoute("/workspace/invitations")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
  },
  component: InvitationsPage,
});
