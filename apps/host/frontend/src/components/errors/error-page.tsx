import { Button, Center, Collapse, Group, Stack, Text, Title, UnstyledButton } from "@mantine/core";
import { IconChevronDown, IconChevronUp } from "@tabler/icons-react";
import { Link, useRouter } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import "../../i18n";
import { generateErrorId } from "./error-page-utils";

export type ErrorPageProps = {
  illustration: ReactNode;
  title: ReactNode;
  message: ReactNode;
  actions?: ReactNode;
  error?: unknown;
  errorId?: string;
  extra?: ReactNode;
  fullscreen?: boolean;
};

export const ErrorPage = ({
  illustration,
  title,
  message,
  actions,
  error,
  errorId,
  extra,
  fullscreen = false,
}: ErrorPageProps) => {
  const { t } = useI18n("host");
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const id = errorId ?? generateErrorId();
  return (
    <Center mih={fullscreen ? "100vh" : "60vh"} p="xl">
      <Stack align="center" maw={560} w="100%" gap="lg" ta="center">
        {illustration}
        <Title order={1} size="clamp(1.8rem, 4vw, 2.5rem)">
          {title}
        </Title>
        <Text c="dimmed" size="lg">
          {message}
        </Text>
        <Group justify="center">
          {actions ?? (
            <>
              <Button variant="default" onClick={() => router.history.back()}>
                {t("errorBack")}
              </Button>
              <Button component={Link} to="/">
                {t("errorHome")}
              </Button>
            </>
          )}
        </Group>
        {extra}
        {(error || errorId) && (
          <Stack w="100%" gap={4} align="stretch">
            <UnstyledButton onClick={() => setOpen((value) => !value)}>
              <Group justify="center" gap="xs" c="dimmed">
                <Text size="sm">{t("errorTechnicalDetails")}</Text>
                {open ? <IconChevronUp size={16} /> : <IconChevronDown size={16} />}
              </Group>
            </UnstyledButton>
            <Collapse expanded={open}>
              <Text size="xs" c="dimmed">
                {t("errorIdLabel")}: {id}
                {error instanceof Error && ` · ${error.name}: ${error.message}`}
              </Text>
            </Collapse>
          </Stack>
        )}
      </Stack>
    </Center>
  );
};
