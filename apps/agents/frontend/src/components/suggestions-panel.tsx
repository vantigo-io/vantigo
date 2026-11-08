import { Alert, Anchor, Button, Card, Group, Loader, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { PageSection, useI18n } from "@vantigo/frontend-shell";
import {
  agentQueryKey,
  applySuggestion,
  dismissSuggestion,
  suggestionsQueryKey,
  suggestionsQueryOptions,
} from "../api/agents";
import { isSuggestionDecided, isSuggestionStale, isVersionConflict } from "../api/request";
import type { AgentRole, InstructionSuggestion } from "../api/types";
import { reviewItemLinkOptions } from "../lib/routes";
import { useErrorText } from "./use-error-text";

/**
 * What the weekly check proposes for a role's instructions (V28), each with the corrections it rests on. The
 * Owner, or whoever holds agents:manage, applies one with a click, which is an ordinary role edit, or dismisses
 * it. Nothing here changes the instructions by itself, and a role with nothing proposed shows nothing.
 */
export const SuggestionsPanel = ({ agentId, role }: { agentId: string; role: AgentRole }) => {
  const { t } = useI18n("agents");
  const errorText = useErrorText();
  const queryClient = useQueryClient();
  const query = useQuery(suggestionsQueryOptions(role.id));

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: suggestionsQueryKey(role.id) });
    void queryClient.invalidateQueries({ queryKey: agentQueryKey(agentId) });
  };
  const apply = useMutation({
    mutationFn: (suggestion: InstructionSuggestion) => applySuggestion(suggestion.id, role.version),
    onSuccess: () => {
      refresh();
      notifications.show({ color: "teal", title: t("suggestionApplied"), message: "" });
    },
    onError: (error) => {
      refresh();
      const message = isVersionConflict(error)
        ? t("staleBody")
        : isSuggestionStale(error)
          ? t("suggestionStale")
          : isSuggestionDecided(error)
            ? t("suggestionDecided")
            : errorText(error);
      notifications.show({ color: "red", title: t("suggestionApplyFailed"), message });
    },
  });
  const dismiss = useMutation({
    mutationFn: (suggestion: InstructionSuggestion) => dismissSuggestion(suggestion.id),
    onSuccess: refresh,
    onError: (error) => {
      refresh();
      notifications.show({ color: "red", title: t("suggestionDismissFailed"), message: errorText(error) });
    },
  });

  if (query.isPending) return <Loader size="sm" />;
  if (query.isError)
    return (
      <Alert color="red" data-testid="suggestions">
        {t("suggestionsLoadFailed")}
      </Alert>
    );
  const suggestions = query.data;
  if (suggestions.length === 0) return null;

  return (
    <PageSection>
      <Stack gap="sm" data-testid="suggestions">
        <Stack gap={2}>
          <Text fw={600}>{t("suggestionsTitle")}</Text>
          <Text size="sm" c="dimmed">
            {t("suggestionsHelp")}
          </Text>
        </Stack>
        {suggestions.map((suggestion) => (
          <Card key={suggestion.id} withBorder padding="sm" data-testid="suggestion">
            <Stack gap="xs">
              <Text size="sm">{suggestion.rationale}</Text>
              {suggestion.oldText ? (
                <Stack gap={2}>
                  <Text size="xs" c="dimmed">
                    {t("suggestionReplace")}
                  </Text>
                  <Text size="sm" td="line-through" c="red.8" style={{ whiteSpace: "pre-wrap" }}>
                    {suggestion.oldText}
                  </Text>
                </Stack>
              ) : (
                <Text size="xs" c="dimmed">
                  {t("suggestionAdd")}
                </Text>
              )}
              <Text size="sm" c="teal.9" style={{ whiteSpace: "pre-wrap" }} data-testid="suggestion-new-text">
                {suggestion.newText}
              </Text>
              <Group gap="xs" wrap="wrap">
                <Text size="xs" c="dimmed">
                  {t("suggestionRestsOn", { count: suggestion.itemIds.length })}
                </Text>
                {suggestion.itemIds.map((id, index) => (
                  <Anchor key={id} size="xs" renderRoot={(props) => <Link {...reviewItemLinkOptions(id)} {...props} />}>
                    {index + 1}
                  </Anchor>
                ))}
              </Group>
              <Group gap="xs">
                <Button
                  size="xs"
                  loading={apply.isPending && apply.variables?.id === suggestion.id}
                  disabled={apply.isPending || dismiss.isPending}
                  onClick={() => apply.mutate(suggestion)}
                >
                  {t("suggestionApply")}
                </Button>
                <Button
                  size="xs"
                  variant="default"
                  loading={dismiss.isPending && dismiss.variables?.id === suggestion.id}
                  disabled={apply.isPending || dismiss.isPending}
                  onClick={() => dismiss.mutate(suggestion)}
                >
                  {t("suggestionDismiss")}
                </Button>
              </Group>
            </Stack>
          </Card>
        ))}
      </Stack>
    </PageSection>
  );
};
