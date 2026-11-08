import { Button, FileButton, Group, NativeSelect, NumberInput, Stack, Text, Textarea, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { agentQueryKey, agentsQueryKey, putAvatar, settingsQueryOptions, updateAgent } from "../api/agents";
import { isVersionConflict } from "../api/request";
import type { Agent, AgentLanguages, DisclosureWording } from "../api/types";
import { BUDGET_MIN, charsLeft, NAME_MAX, STYLE_MAX } from "../lib/modes";
import { AgentAvatar } from "./agent-avatar";
import { DisclosurePreview } from "./disclosure-preview";
import { useErrorText } from "./use-error-text";

const languageOptions: readonly AgentLanguages[] = ["customer", "nb", "en"];
const wordingOptions: readonly DisclosureWording[] = ["assistant", "talking", "compact"];

const languageKey = { customer: "languagesCustomer", nb: "languagesNb", en: "languagesEn" } as const;
const wordingKey = { assistant: "wordingAssistant", talking: "wordingTalking", compact: "wordingCompact" } as const;

/**
 * The persona (V10, screen 2): name, picture, style, language, model and the
 * opening line, with a live preview of what the customer reads. A save names
 * the version it read; a stale one reloads the agent and says so.
 */
export const PersonaTab = ({ agent, canManage }: { agent: Agent; canManage: boolean }) => {
  const { t } = useI18n("agents");
  const errorText = useErrorText();
  const queryClient = useQueryClient();
  const settings = useQuery(settingsQueryOptions());
  const [name, setName] = useState(agent.name);
  const [style, setStyle] = useState(agent.style);
  const [languages, setLanguages] = useState<AgentLanguages>(agent.languages);
  const [model, setModel] = useState(agent.model);
  const [wording, setWording] = useState<DisclosureWording>(agent.disclosureWording);
  // The daily token budget (V24): empty for none, else at least 1,000 tokens.
  const [budget, setBudget] = useState<number | string>(agent.dailyTokenBudget ?? "");

  const models = settings.data?.models ?? [];
  // The agent's own model is offered even when the allow-list has since moved on.
  const modelChoices = model !== "" && !models.includes(model) ? [...models, model] : models;
  const trimmed = name.trim();
  const budgetValid = budget === "" || (typeof budget === "number" && Number.isInteger(budget) && budget >= BUDGET_MIN);
  const valid = trimmed !== "" && trimmed.length <= NAME_MAX && style.length <= STYLE_MAX && budgetValid;

  const save = useMutation({
    mutationFn: () =>
      updateAgent(
        agent.id,
        {
          name: trimmed,
          style,
          languages,
          model,
          disclosureWording: wording,
          // 0 removes the budget; the field is empty for none.
          dailyTokenBudget: typeof budget === "number" ? budget : 0,
        },
        agent.version,
      ),
    onSuccess: (saved) => {
      queryClient.setQueryData(agentQueryKey(agent.id), saved);
      void queryClient.invalidateQueries({ queryKey: agentsQueryKey });
      notifications.show({ color: "teal", title: t("saved"), message: "" });
    },
    onError: (error) => {
      if (isVersionConflict(error)) {
        notifications.show({ color: "yellow", title: t("staleTitle"), message: t("staleBody") });
        void queryClient.invalidateQueries({ queryKey: agentQueryKey(agent.id) });
        return;
      }
      notifications.show({ color: "red", title: t("saveFailed"), message: errorText(error) });
    },
  });

  const upload = useMutation({
    mutationFn: (file: File) => putAvatar(agent.id, file),
    onSuccess: (saved) => {
      queryClient.setQueryData(agentQueryKey(agent.id), saved);
      void queryClient.invalidateQueries({ queryKey: agentsQueryKey });
      notifications.show({ color: "teal", title: t("avatarUploaded"), message: "" });
    },
    onError: (error) => notifications.show({ color: "red", title: t("avatarFailed"), message: errorText(error) }),
  });

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        if (valid && canManage) save.mutate();
      }}
    >
      <Stack gap="md" maw={640}>
        <Group>
          <AgentAvatar agent={agent} size={64} />
          {canManage && (
            <Stack gap={2}>
              <FileButton onChange={(file) => file && upload.mutate(file)} accept="image/png,image/jpeg">
                {(props) => (
                  <Button {...props} variant="default" size="xs" loading={upload.isPending}>
                    {t("avatarUpload")}
                  </Button>
                )}
              </FileButton>
              <Text size="xs" c="dimmed">
                {t("avatarHelp")}
              </Text>
            </Stack>
          )}
        </Group>
        <TextInput
          label={t("personaName")}
          value={name}
          onChange={(event) => setName(event.currentTarget.value)}
          maxLength={NAME_MAX}
          disabled={!canManage}
          required
        />
        <Textarea
          label={t("personaStyle")}
          description={t("personaStyleHelp")}
          value={style}
          onChange={(event) => setStyle(event.currentTarget.value)}
          autosize
          minRows={3}
          disabled={!canManage}
          error={charsLeft(style, STYLE_MAX) < 0 ? t("counter", { count: style.length, max: STYLE_MAX }) : undefined}
        />
        <Text size="xs" c={charsLeft(style, STYLE_MAX) < 0 ? "red" : "dimmed"} ta="right" mt={-12}>
          {t("counter", { count: style.length, max: STYLE_MAX })}
        </Text>
        <Group grow align="flex-start">
          <NativeSelect
            label={t("languages")}
            value={languages}
            onChange={(event) => setLanguages(event.currentTarget.value as AgentLanguages)}
            data={languageOptions.map((value) => ({ value, label: t(languageKey[value]) }))}
            disabled={!canManage}
          />
          <NativeSelect
            label={t("model")}
            value={model}
            onChange={(event) => setModel(event.currentTarget.value)}
            data={[
              { value: "", label: t("modelWorkspaceDefault") },
              ...modelChoices.map((value) => ({ value, label: value })),
            ]}
            disabled={!canManage}
          />
        </Group>
        <NativeSelect
          label={t("wording")}
          value={wording}
          onChange={(event) => setWording(event.currentTarget.value as DisclosureWording)}
          data={wordingOptions.map((value) => ({ value, label: t(wordingKey[value]) }))}
          disabled={!canManage}
        />
        <NumberInput
          label={t("dailyTokenBudget")}
          description={t("dailyTokenBudgetHelp", { min: BUDGET_MIN })}
          placeholder={t("dailyTokenBudgetNone")}
          value={budget}
          onChange={setBudget}
          min={BUDGET_MIN}
          step={1000}
          allowDecimal={false}
          allowNegative={false}
          thousandSeparator=" "
          disabled={!canManage}
          error={budgetValid ? undefined : t("dailyTokenBudgetInvalid", { min: BUDGET_MIN })}
        />
        <DisclosurePreview
          wording={wording}
          languages={languages}
          agentName={name}
          companyName={settings.data?.companyName ?? ""}
        />
        {canManage && (
          <Group>
            <Button type="submit" disabled={!valid} loading={save.isPending}>
              {t("save")}
            </Button>
          </Group>
        )}
      </Stack>
    </form>
  );
};
