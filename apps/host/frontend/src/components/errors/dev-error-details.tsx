import { Badge, Button, Code, CopyButton, Group, Paper, Stack, Text } from "@mantine/core";
import { IconCheck, IconCopy } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";

type Props = { error?: unknown; componentStack?: string; context?: Record<string, unknown> };
const asError = (error: unknown) => (error instanceof Error ? error : undefined);
const apiFields = (error: unknown) => (error && typeof error === "object" ? (error as Record<string, unknown>) : {});
const formatValue = (value: unknown) => (typeof value === "string" ? value : JSON.stringify(value));
const apiKeys = ["status", "url", "body"] as const;

export const DevErrorDetails = ({ error, componentStack, context }: Props) => {
  const { t } = useI18n("host");
  if (!import.meta.env.DEV) return null;
  const current = asError(error);
  const fields = apiFields(error);
  const cause = current?.cause instanceof Error ? current.cause : undefined;
  const markdown = [
    "## Development error",
    current && `**${current.name}:** ${current.message}`,
    current?.stack && `\n\`\`\`\n${current.stack}\n\`\`\``,
    componentStack && `\nComponent stack:\n${componentStack}`,
    context &&
      `\nContext:\n${Object.entries(context)
        .map(([key, value]) => `- ${key}: ${formatValue(value)}`)
        .join("\n")}`,
  ]
    .filter(Boolean)
    .join("\n");
  return (
    <Paper p="md" w="100%" ta="left" withBorder style={{ borderColor: "var(--mantine-color-orange-4)" }}>
      <Stack gap="sm">
        <Group justify="space-between">
          <Text fw={700}>{t("developmentDetails")}</Text>
          <Badge color="orange" variant="light">
            {t("devBadge")}
          </Badge>
        </Group>
        {current && (
          <Text size="sm">
            <b>{current.name}:</b> {current.message}
          </Text>
        )}
        {current?.stack && (
          <Code block mah={220} style={{ overflow: "auto", whiteSpace: "pre-wrap" }}>
            {current.stack}
          </Code>
        )}
        {componentStack && (
          <Code block mah={140} style={{ overflow: "auto", whiteSpace: "pre-wrap" }}>
            {componentStack}
          </Code>
        )}
        {cause && (
          <Text size="sm">
            <b>{t("devCause")}:</b> {cause.name}: {cause.message}
          </Text>
        )}
        {apiKeys.some((key) => key in fields) && (
          <Stack gap={2}>
            <Text size="sm" fw={600}>
              {t("devApiError")}
            </Text>
            {apiKeys.map(
              (key) =>
                key in fields && (
                  <Text size="sm" key={key}>
                    <b>{key}:</b> {formatValue(fields[key])}
                  </Text>
                ),
            )}
          </Stack>
        )}
        {context && (
          <dl>
            {Object.entries(context).map(([key, value]) => (
              <div key={key}>
                <Text component="dt" size="xs" fw={700}>
                  {key}
                </Text>
                <Text component="dd" size="sm" ml={0}>
                  {formatValue(value)}
                </Text>
              </div>
            ))}
          </dl>
        )}
        <CopyButton value={markdown}>
          {({ copied, copy }) => (
            <Button
              size="xs"
              variant="light"
              leftSection={copied ? <IconCheck size={15} /> : <IconCopy size={15} />}
              onClick={copy}
            >
              {copied ? t("devCopied") : t("devCopyMarkdown")}
            </Button>
          )}
        </CopyButton>
      </Stack>
    </Paper>
  );
};
