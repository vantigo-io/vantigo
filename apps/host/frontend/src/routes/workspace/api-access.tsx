import { Badge, Button, Group, Modal, Select, Stack, Table, Text, Textarea, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, redirect, useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, PageSection, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { listAuthorizationRoles, listAuthorizationUsers } from "../../api/authorization";
import { createIntegration, type Integration, integrationsQueryKey, listIntegrations } from "../../api/integrations";
import { CustomRolePicker } from "../../components/custom-role-picker";
import "../../i18n";
import { ClientHosts, RegisteredClients } from "./-api-access-oauth-clients";
import { contactOptions, fieldErrorsOf, integrationStatusColor } from "./-api-access-shared";

type Draft = { name: string; description: string; contactUserId: string | null; roleIds: string[] };
const emptyDraft: Draft = { name: "", description: "", contactUserId: null, roleIds: [] };

/**
 * Workspace admin › API access (API access design K22): the Owner's
 * integrations, the accounts external applications act as with an API key.
 * The list says each one's status, roles, keys in force, the nearest expiry
 * among them and when a key was last used; *New integration* makes one, and a
 * name opens its page. Below it, while OAuth is on, the clients registered by
 * hand and the client hosts that may connect (OAuth connections design C8,
 * C11, C25).
 */
const ApiAccessPage = () => {
  const { t, formatters } = useI18n("host");
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const integrations = useQuery({ queryKey: integrationsQueryKey, queryFn: listIntegrations });
  const roles = useQuery({ queryKey: ["authorization", "roles"], queryFn: listAuthorizationRoles });
  const users = useQuery({ queryKey: ["authorization", "users"], queryFn: listAuthorizationUsers });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const date = (value: string | null) =>
    value ? formatters.formatDate(value, { dateStyle: "medium" }) : t("apiAccess.none");

  const create = useMutation({
    mutationFn: (d: Draft) =>
      createIntegration({
        name: d.name.trim(),
        ...(d.description.trim() ? { description: d.description.trim() } : {}),
        ...(d.contactUserId ? { contactUserId: d.contactUserId } : {}),
        roleIds: d.roleIds,
      }),
    onSuccess: async (detail) => {
      setDraft(null);
      notifications.show({ color: "teal", title: t("apiAccess.created"), message: detail.integration.name });
      await queryClient.invalidateQueries({ queryKey: integrationsQueryKey });
      await navigate({ to: "/workspace/api-access/$integrationId", params: { integrationId: detail.integration.id } });
    },
    onError: (error) => {
      setErrors(fieldErrorsOf(error));
      notifications.show({ color: "red", title: t("apiAccess.couldNotSave"), message: (error as Error).message });
    },
  });

  const open = () => {
    setErrors({});
    setDraft(emptyDraft);
  };

  return (
    <Stack gap="xl">
      <PageHeader
        title={t("apiAccess.title")}
        description={t("apiAccess.description")}
        actions={
          <Button leftSection={<IconPlus size={16} />} onClick={open}>
            {t("apiAccess.newIntegration")}
          </Button>
        }
      />
      <PageSection>
        {integrations.isPending ? (
          <ContentSkeleton rows={3} />
        ) : (integrations.data ?? []).length === 0 ? (
          <EmptyState title={t("apiAccess.empty")} description={t("apiAccess.emptyHint")} />
        ) : (
          <Table.ScrollContainer minWidth={760}>
            <Table verticalSpacing="sm">
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("apiAccess.name")}</Table.Th>
                  <Table.Th>{t("apiAccess.status")}</Table.Th>
                  <Table.Th>{t("apiAccess.roles")}</Table.Th>
                  <Table.Th>{t("apiAccess.keyCount")}</Table.Th>
                  <Table.Th>{t("apiAccess.nearestExpiry")}</Table.Th>
                  <Table.Th>{t("apiAccess.lastUsed")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(integrations.data ?? []).map((integration: Integration) => (
                  <Table.Tr key={integration.id}>
                    <Table.Td>
                      <Link to="/workspace/api-access/$integrationId" params={{ integrationId: integration.id }}>
                        <Text fw={600} component="span">
                          {integration.name}
                        </Text>
                      </Link>
                    </Table.Td>
                    <Table.Td>
                      <Badge color={integrationStatusColor[integration.status]} variant="light">
                        {t(`apiAccess.status_${integration.status}`)}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Group gap={4}>
                        {integration.roles.length === 0 && <Text size="sm">{t("apiAccess.none")}</Text>}
                        {integration.roles.map((role) => (
                          <Badge key={role.id} variant="outline">
                            {role.displayName}
                          </Badge>
                        ))}
                      </Group>
                    </Table.Td>
                    <Table.Td>{integration.keyCount}</Table.Td>
                    <Table.Td>{date(integration.nearestExpiry)}</Table.Td>
                    <Table.Td>
                      {integration.lastUsedAt
                        ? formatters.formatDate(integration.lastUsedAt, { dateStyle: "medium", timeStyle: "short" })
                        : t("apiAccess.neverUsed")}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </PageSection>
      <RegisteredClients />
      <ClientHosts />
      <Modal opened={draft !== null} onClose={() => setDraft(null)} title={t("apiAccess.newIntegration")} centered>
        {draft && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              create.mutate(draft);
            }}
          >
            <Stack>
              <TextInput
                label={t("apiAccess.name")}
                description={t("apiAccess.nameHint")}
                required
                maxLength={100}
                value={draft.name}
                error={errors.name}
                onChange={(event) => setDraft({ ...draft, name: event.currentTarget.value })}
              />
              <Textarea
                label={t("apiAccess.descriptionLabel")}
                autosize
                minRows={2}
                maxLength={1000}
                value={draft.description}
                error={errors.description}
                onChange={(event) => setDraft({ ...draft, description: event.currentTarget.value })}
              />
              <Select
                label={t("apiAccess.contact")}
                description={t("apiAccess.contactHint")}
                searchable
                clearable
                data={contactOptions(users.data ?? [], draft.contactUserId)}
                value={draft.contactUserId}
                error={errors.contactUserId}
                onChange={(contactUserId) => setDraft({ ...draft, contactUserId })}
              />
              <CustomRolePicker
                label={t("apiAccess.roles")}
                description={t("apiAccess.rolesHint")}
                roles={roles.data ?? []}
                value={draft.roleIds}
                onChange={(roleIds) => setDraft({ ...draft, roleIds })}
              />
              {errors.roleIds && (
                <Text size="sm" c="red">
                  {errors.roleIds}
                </Text>
              )}
              <Button type="submit" loading={create.isPending} disabled={!draft.name.trim()}>
                {t("apiAccess.create")}
              </Button>
            </Stack>
          </form>
        )}
      </Modal>
    </Stack>
  );
};

export const Route = createFileRoute("/workspace/api-access")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
  },
  component: ApiAccessPage,
});
