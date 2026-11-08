import { ActionIcon, Collapse, Group, Stack, Text, Tooltip, UnstyledButton } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconBrain, IconChevronDown, IconChevronRight, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageSection, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type AgentMemoryEntry,
  agentMemoryQueryKey,
  customerAgentMemoryQueryOptions,
  deleteAgentNote,
} from "../api/agent-memory";
import "../i18n";

/**
 * What the virtual employees remember about this customer (virtual employees design V27), in a section that
 * starts collapsed: the short notes an agent wrote from the customer's conversations, with a delete for a
 * reader who may manage the agents. It shows nothing at all when nothing is remembered, for a reader who may
 * not see it, and without the agents module.
 */
export const CustomerAgentMemory = ({ customerId }: { customerId: number }) => {
  const { t, formatters } = useI18n("customers");
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const { data } = useQuery(customerAgentMemoryQueryOptions(customerId));
  const remove = useMutation({
    mutationFn: (noteId: string) => deleteAgentNote(noteId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: agentMemoryQueryKey(customerId) }),
    onError: (error: Error) =>
      notifications.show({ color: "red", title: t("agentNoteDeleteFailed"), message: error.message }),
  });

  const entries = (data?.agents ?? []).filter((entry) => entry.notes.length > 0);
  if (entries.length === 0) return null;
  const canDelete = data?.canDelete ?? false;
  const full = entries.some((entry) => entry.full);
  const total = entries.reduce((sum, entry) => sum + entry.notes.length, 0);
  const names = entries.map((entry) => entry.agentName).join(", ");

  const confirmDelete = (entry: AgentMemoryEntry, noteId: string) =>
    modals.openConfirmModal({
      title: t("agentNoteDelete"),
      children: <Text size="sm">{t("agentNoteDeleteQuestion", { agent: entry.agentName })}</Text>,
      labels: { confirm: t("delete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(noteId),
    });

  return (
    <PageSection>
      <Stack gap="sm" data-testid="agent-memory">
        <UnstyledButton
          onClick={() => setOpen((value) => !value)}
          aria-expanded={open}
          aria-controls="agent-memory-notes"
        >
          <Group gap="xs">
            {open ? <IconChevronDown size={16} /> : <IconChevronRight size={16} />}
            <IconBrain size={18} stroke={1.5} />
            <Text fw={600}>{t("agentMemoryTitle", { agents: names })}</Text>
            <Text size="sm" c="dimmed">
              {t("agentMemoryCount", { count: total })}
            </Text>
            {full && (
              <Text size="sm" c="orange.8">
                {t("agentMemoryFullBadge")}
              </Text>
            )}
          </Group>
        </UnstyledButton>
        <Collapse expanded={open} id="agent-memory-notes">
          <Stack gap="md">
            <Text size="xs" c="dimmed">
              {t("agentMemoryHelp")}
            </Text>
            {entries.map((entry) => (
              <Stack key={entry.agentId} gap={4}>
                {entry.full && (
                  <Text size="xs" c="orange.8" data-testid="agent-memory-full">
                    {t("agentMemoryFull", { agent: entry.agentName })}
                  </Text>
                )}
                {entries.length > 1 && (
                  <Text size="sm" fw={500}>
                    {entry.agentName}
                  </Text>
                )}
                {entry.notes.map((note) => (
                  <Group key={note.id} justify="space-between" wrap="nowrap" align="flex-start">
                    <Stack gap={0}>
                      <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
                        {note.text}
                      </Text>
                      <Text size="xs" c="dimmed">
                        {formatters.formatDate(note.createdAt, { dateStyle: "medium" })}
                      </Text>
                    </Stack>
                    {canDelete && (
                      <Tooltip label={t("agentNoteDelete")}>
                        <ActionIcon
                          variant="subtle"
                          color="red"
                          aria-label={t("agentNoteDeleteNamed", { text: note.text })}
                          loading={remove.isPending && remove.variables === note.id}
                          onClick={() => confirmDelete(entry, note.id)}
                        >
                          <IconTrash size={16} />
                        </ActionIcon>
                      </Tooltip>
                    )}
                  </Group>
                ))}
              </Stack>
            ))}
          </Stack>
        </Collapse>
      </Stack>
    </PageSection>
  );
};
