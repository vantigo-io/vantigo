import { Alert, Anchor, Badge, Button, Group, Modal, NativeSelect, Stack, Table, Text, TextInput } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { agentQueryKey, agentsQueryKey, createRole, templatesQueryOptions } from "../api/agents";
import type { Agent, TemplateKey } from "../api/types";
import { NAME_MAX } from "../lib/modes";
import { roleLinkOptions } from "../lib/routes";
import { useErrorText } from "./use-error-text";

const AddRoleModal = ({ agent, opened, onClose }: { agent: Agent; opened: boolean; onClose: () => void }) => {
  const { t } = useI18n("agents");
  const errorText = useErrorText();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const templates = useQuery(templatesQueryOptions());
  const [template, setTemplate] = useState<TemplateKey | "">("");
  const [name, setName] = useState<string | undefined>();
  const chosen = templates.data?.find((candidate) => candidate.key === (template || templates.data?.[0]?.key));
  const roleName = (name ?? chosen?.defaultName ?? "").trim();

  const create = useMutation({
    mutationFn: () =>
      createRole(agent.id, {
        template: chosen?.key ?? "front_desk",
        name: roleName,
        instructions: chosen?.defaultInstructions ?? "",
      }),
    onSuccess: (role) => {
      void queryClient.invalidateQueries({ queryKey: agentQueryKey(agent.id) });
      void queryClient.invalidateQueries({ queryKey: agentsQueryKey });
      notifications.show({ color: "teal", title: t("roleCreated"), message: "" });
      onClose();
      navigate(roleLinkOptions(agent.id, role.id));
    },
    onError: (error) => notifications.show({ color: "red", title: t("roleCreateFailed"), message: errorText(error) }),
  });

  return (
    <Modal opened={opened} onClose={onClose} title={t("addRoleTitle")}>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (roleName !== "" && chosen) create.mutate();
        }}
      >
        <Stack>
          <NativeSelect
            label={t("roleTemplate")}
            value={chosen?.key ?? ""}
            onChange={(event) => {
              setTemplate(event.currentTarget.value as TemplateKey);
              setName(undefined);
            }}
            data={(templates.data ?? []).map((candidate) => ({
              value: candidate.key,
              label: t(`template.${candidate.key}`),
            }))}
          />
          <TextInput
            label={t("roleName")}
            value={name ?? chosen?.defaultName ?? ""}
            onChange={(event) => setName(event.currentTarget.value)}
            maxLength={NAME_MAX}
            required
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" disabled={roleName === "" || !chosen} loading={create.isPending}>
              {t("addRole")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};

/** The agent's roles (V10): name, template, mode, channels and a hand-off warning; each opens its own page. */
export const RolesTab = ({ agent, canManage }: { agent: Agent; canManage: boolean }) => {
  const { t } = useI18n("agents");
  const [opened, { open, close }] = useDisclosure(false);
  return (
    <Stack gap="md">
      <Group justify="space-between">
        <Text size="sm" c="dimmed" maw={560}>
          {t("rolesDescription")}
        </Text>
        {canManage && (
          <Button leftSection={<IconPlus size={16} />} onClick={open}>
            {t("addRole")}
          </Button>
        )}
      </Group>
      {agent.roles.length === 0 ? (
        <EmptyState title={t("noRolesYet")} description={t("noRolesYetDescription")} />
      ) : (
        <Table.ScrollContainer minWidth={560}>
          <Table verticalSpacing="sm">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("roleName")}</Table.Th>
                <Table.Th>{t("roleTemplate")}</Table.Th>
                <Table.Th>{t("roleMode")}</Table.Th>
                <Table.Th>{t("roleChannels")}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {agent.roles.map((role) => (
                <Table.Tr key={role.id}>
                  <Table.Td>
                    <Group gap="xs">
                      <Anchor renderRoot={(props) => <Link {...roleLinkOptions(agent.id, role.id)} {...props} />}>
                        {role.name}
                      </Anchor>
                      {role.handOffWarning !== "none" && (
                        <Badge
                          color="yellow"
                          variant="light"
                          leftSection={<IconAlertTriangle size={12} />}
                          title={t(`handOffWarning.${role.handOffWarning}`)}
                        >
                          {t(`handOffWarning.${role.handOffWarning}`)}
                        </Badge>
                      )}
                    </Group>
                  </Table.Td>
                  <Table.Td>{t(`template.${role.template}`)}</Table.Td>
                  <Table.Td>{t(`replyMode.${role.replyMode}`, { threshold: role.threshold })}</Table.Td>
                  <Table.Td>{t("roleChannelCount", { count: role.channelIds.length })}</Table.Td>
                  <Table.Td>
                    <Anchor
                      size="sm"
                      renderRoot={(props) => <Link {...roleLinkOptions(agent.id, role.id)} {...props} />}
                    >
                      {t("openRole")}
                    </Anchor>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
      {agent.roles.some((role) => role.handOffWarning !== "none") && (
        <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
          {t("handOffHelp")}
        </Alert>
      )}
      <AddRoleModal agent={agent} opened={opened} onClose={close} />
    </Stack>
  );
};
