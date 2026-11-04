import {
  ActionIcon,
  Alert,
  Badge,
  Card,
  Code,
  CopyButton,
  Group,
  Stack,
  Text,
  ThemeIcon,
  Title,
  Tooltip,
} from "@mantine/core";
import { IconCheck, IconCopy, IconLock, IconRoute } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useId } from "react";

/**
 * One card of the page: a region named by its title, so a reader of the
 * accessibility tree — and a test — finds each step by its name.
 */
export const SectionCard = ({
  title,
  badge,
  leading,
  children,
}: {
  title: string;
  badge?: ReactNode;
  leading?: ReactNode;
  children: ReactNode;
}) => {
  const id = useId();
  return (
    <Card withBorder radius="md" padding="lg" component="section" aria-labelledby={id}>
      <Group justify="space-between" mb="sm" wrap="nowrap" align="flex-start">
        <Group gap="sm" wrap="nowrap">
          {leading}
          <Title order={3} fz="h4" id={id}>
            {title}
          </Title>
        </Group>
        {badge}
      </Group>
      <Stack gap="sm">{children}</Stack>
    </Card>
  );
};

/**
 * One step of the Connection tab: its number, its title, where at Google it
 * happens, and a *Done* mark the connection answers — `undefined` for a step
 * done at Google that Vantigo cannot see but through the test.
 */
export const StepCard = ({
  step,
  title,
  path,
  done,
  children,
}: {
  step: number;
  title: string;
  path?: string;
  done: boolean | undefined;
  children: ReactNode;
}) => {
  const { t } = useI18n("google");
  const badge =
    done === undefined ? undefined : done ? (
      <Badge color="teal" leftSection={<IconCheck size={12} />}>
        {t("done")}
      </Badge>
    ) : (
      <Badge color="gray" variant="outline">
        {t("notDone")}
      </Badge>
    );
  return (
    <SectionCard
      title={title}
      badge={badge}
      leading={
        <ThemeIcon variant="light" radius="xl" color={done ? "teal" : "blue"}>
          {step}
        </ThemeIcon>
      }
    >
      {path && <ConsolePath path={path} />}
      {children}
    </SectionCard>
  );
};

/** Where at Google a step happens, as a breadcrumb. */
export const ConsolePath = ({ path }: { path: string }) => (
  <Group gap={6} wrap="nowrap" align="flex-start">
    <IconRoute size={16} style={{ flexShrink: 0, marginTop: 2 }} aria-hidden />
    <Text size="sm" c="dimmed">
      {path}
    </Text>
  </Group>
);

/**
 * A value the Owner pastes at Google, with a button that copies it. The
 * button is named "Copy {label}" when a label names a value too long to read
 * aloud, such as the list of scopes, and "Copy {value}" otherwise.
 */
export const CopyValue = ({ value, label }: { value: string; label?: string }) => {
  const { t } = useI18n("google");
  return (
    <CopyButton value={value}>
      {({ copied, copy }) => (
        <Tooltip label={copied ? t("copied") : t("copy")}>
          <ActionIcon
            variant="subtle"
            size="sm"
            color={copied ? "teal" : "gray"}
            aria-label={t("copyValue", { value: label ?? value })}
            onClick={copy}
          >
            {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
          </ActionIcon>
        </Tooltip>
      )}
    </CopyButton>
  );
};

/** A labelled value to paste, shown in full with its copy button. */
export const CopyLine = ({ label, value, copyLabel }: { label: string; value: string; copyLabel?: string }) => (
  <Stack gap={2}>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Group gap="xs" wrap="nowrap" align="flex-start">
      <Code block style={{ flex: 1, whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
        {value}
      </Code>
      <CopyValue value={value} label={copyLabel} />
    </Group>
  </Stack>
);

/** What a step says to someone who may read it but not do it: the Owner does it. */
export const OwnerOnly = () => {
  const { t } = useI18n("google");
  return (
    <Alert color="gray" variant="light" icon={<IconLock size={16} />}>
      {t("ownerOnly")}
    </Alert>
  );
};

/** A fact: a small label above its value. */
export const Fact = ({ label, value }: { label: string; value: string }) => (
  <Stack gap={0}>
    <Text size="xs" c="dimmed">
      {label}
    </Text>
    <Text size="sm">{value}</Text>
  </Stack>
);
