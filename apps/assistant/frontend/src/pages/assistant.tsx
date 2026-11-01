import { Box, Button, Drawer, Group, Paper, Stack, Title } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { IconMenu2 } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useCallback } from "react";
import { meQueryKey, putMe } from "../api/assistant";
import { ConversationSidebar } from "../components/conversation-sidebar";
import { ConversationView } from "../components/conversation-view";
import "../i18n";

export interface AssistantPageProps {
  /** The conversation in the address; null opens a fresh one. */
  conversationId: string | null;
  /** Moves the address to a conversation (or to the app's own address for a fresh one). */
  onSelect: (id: string | null) => void;
  onNavigate?: (route: string) => void;
  appLabel?: (app: string) => string;
}

/**
 * The Assistant app (design S1): a sidebar of every conversation the person
 * has had anywhere in Vantigo and the open conversation beside it. Choosing
 * one resumes it, and what is open is remembered on the server so the panel
 * opens the same one.
 */
export const AssistantPage = ({ conversationId, onSelect, onNavigate, appLabel }: AssistantPageProps) => {
  const { t } = useI18n("assistant");
  const queryClient = useQueryClient();
  const [drawer, { open: openDrawer, close: closeDrawer }] = useDisclosure(false);

  const remember = useCallback(
    (id: string | null) => {
      void putMe(id)
        .then((me) => queryClient.setQueryData(meQueryKey, me))
        .catch(() => undefined);
    },
    [queryClient],
  );

  const select = (id: string) => {
    closeDrawer();
    remember(id);
    onSelect(id);
  };

  const sidebar = (
    <ConversationSidebar
      selectedId={conversationId}
      onSelect={select}
      onNew={() => {
        closeDrawer();
        onSelect(null);
      }}
      onDeleted={(id) => {
        if (id === conversationId) onSelect(null);
      }}
      appLabel={appLabel}
    />
  );

  return (
    <Stack gap="md" h="calc(100dvh - 60px - 2 * var(--mantine-spacing-md))" style={{ minHeight: 420 }}>
      <Group justify="space-between" wrap="nowrap">
        <Title order={2}>{t("assistant")}</Title>
        <Button hiddenFrom="sm" variant="default" leftSection={<IconMenu2 size={16} />} onClick={openDrawer}>
          {t("conversations")}
        </Button>
      </Group>
      <Group align="stretch" gap="md" wrap="nowrap" flex={1} style={{ minHeight: 0 }}>
        <Box visibleFrom="sm" w={300} style={{ flexShrink: 0, minHeight: 0 }}>
          <Paper withBorder p="sm" h="100%">
            {sidebar}
          </Paper>
        </Box>
        <Paper withBorder flex={1} style={{ minWidth: 0, minHeight: 0 }}>
          <ConversationView
            conversationId={conversationId}
            origin={{ app: "assistant", route: "/assistant" }}
            onCreated={(id) => {
              remember(id);
              onSelect(id);
            }}
            onMissing={() => onSelect(null)}
            onNavigate={onNavigate}
          />
        </Paper>
      </Group>
      <Drawer opened={drawer} onClose={closeDrawer} title={t("conversations")} size="xs" hiddenFrom="sm">
        {sidebar}
      </Drawer>
    </Stack>
  );
};
