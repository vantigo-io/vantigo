import { Paper, Stack, Text } from "@mantine/core";
import { useI18n } from "@vantigo/frontend-shell";
import type { AgentLanguages, DisclosureWording } from "../api/types";
import { disclosureLine, previewLanguages } from "../lib/disclosure";

/**
 * The opening line the customer will read, live as the persona is edited
 * (V11): the agent's name and the company's, in the agent's language, or in
 * both when it follows the customer.
 */
export const DisclosurePreview = ({
  wording,
  languages,
  agentName,
  companyName,
}: {
  wording: DisclosureWording;
  languages: AgentLanguages;
  agentName: string;
  companyName: string;
}) => {
  const { t } = useI18n("agents");
  const company = companyName.trim() || t("previewCompanyPlaceholder");
  const agent = agentName.trim() || t("previewAgentPlaceholder");
  return (
    <Paper withBorder p="sm" radius="md" aria-label={t("previewTitle")} data-testid="disclosure-preview">
      <Stack gap={6}>
        <Text size="xs" c="dimmed" fw={600}>
          {t("previewTitle")}
        </Text>
        {previewLanguages(languages).map((language) => (
          <Text key={language} size="sm">
            <Text span size="xs" c="dimmed" mr={6}>
              {t(language === "nb" ? "previewLanguageNb" : "previewLanguageEn")}
            </Text>
            {disclosureLine(wording, language, agent, company)}
          </Text>
        ))}
        {companyName.trim() === "" && (
          <Text size="xs" c="orange">
            {t("previewNeedsCompany")}
          </Text>
        )}
      </Stack>
    </Paper>
  );
};
