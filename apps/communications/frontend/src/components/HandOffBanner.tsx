import { Alert, Button, Group, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconRobot } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { assignableUsersQueryOptions } from "../api/conversations";
import { giveBack, handOffQueryKey, handOffQueryOptions } from "../api/handoff";

/**
 * "Handed off · Give back to the agent" (virtual employees design V13): shown on a conversation an
 * agent handed to the people, with the reason, and a button that clears the marker so the agent
 * answers the next message. It asks only on an installation that has an agent among its users.
 */
export function HandOffBanner({ conversationId, canManage }: { conversationId: string; canManage: boolean }) {
  const { t } = useI18n("communications");
  const queryClient = useQueryClient();
  const users = useQuery(assignableUsersQueryOptions(""));
  const hasAgents = (users.data ?? []).some((user) => user.kind === "agent");
  const handOff = useQuery(handOffQueryOptions(conversationId, hasAgents));
  const back = useMutation({
    mutationFn: () => giveBack(conversationId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: handOffQueryKey(conversationId) });
      notifications.show({ color: "teal", title: t("givenBack"), message: "" });
    },
    onError: (error: Error) => notifications.show({ color: "red", title: t("giveBackFailed"), message: error.message }),
  });
  if (!handOff.data?.handedOff) return null;
  return (
    <Alert color="yellow" icon={<IconRobot size={16} />} mb="xs" data-testid="handed-off">
      <Group justify="space-between" wrap="nowrap">
        <Text size="sm">
          {t("handedOff")}
          {handOff.data.reason
            ? ` ${t("handedOffReason", { reason: t(`handOffReason.${handOff.data.reason}`, { defaultValue: handOff.data.reason }) })}`
            : ""}
        </Text>
        {canManage && (
          <Button size="xs" variant="light" loading={back.isPending} onClick={() => back.mutate()}>
            {t("giveBackToAgent")}
          </Button>
        )}
      </Group>
    </Alert>
  );
}
