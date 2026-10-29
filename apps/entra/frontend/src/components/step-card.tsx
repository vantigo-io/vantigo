import {
  ActionIcon,
  Alert,
  Badge,
  Card,
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
 * One of the wizard's six steps (research §7.5): its number, its title, the
 * portal path it happens on, and a *Done* mark the connection answers —
 * `undefined` for an optional step whose state is another module's.
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
  path: string;
  done: boolean | undefined;
  children: ReactNode;
}) => {
  const { t } = useI18n("entra");
  const badge =
    done === undefined ? (
      <Badge color="gray" variant="light">
        {t("optional")}
      </Badge>
    ) : done ? (
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
      <PortalPath path={path} />
      {children}
    </SectionCard>
  );
};

/** Where in the Microsoft Entra admin centre a step happens, as a breadcrumb. */
export const PortalPath = ({ path }: { path: string }) => {
  const { t } = useI18n("entra");
  return (
    <Group gap={6} wrap="nowrap" align="flex-start">
      <IconRoute size={16} style={{ flexShrink: 0, marginTop: 2 }} aria-hidden />
      <Text size="sm" c="dimmed">
        <Text span size="sm" fw={500} c="dimmed">
          {t("inThePortal")}
        </Text>{" "}
        {path}
      </Text>
    </Group>
  );
};

/** A value the Owner pastes in the portal, with a button that copies it. */
export const CopyValue = ({ value }: { value: string }) => {
  const { t } = useI18n("entra");
  return (
    <CopyButton value={value}>
      {({ copied, copy }) => (
        <Tooltip label={copied ? t("copied") : t("copy")}>
          <ActionIcon
            variant="subtle"
            size="sm"
            color={copied ? "teal" : "gray"}
            aria-label={t("copyValue", { value })}
            onClick={copy}
          >
            {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
          </ActionIcon>
        </Tooltip>
      )}
    </CopyButton>
  );
};

/** What a step says to someone who may read it but not do it: the Owner does it. */
export const OwnerOnly = () => {
  const { t } = useI18n("entra");
  return (
    <Alert color="gray" variant="light" icon={<IconLock size={16} />}>
      {t("ownerOnly")}
    </Alert>
  );
};

/** A missing permission by name. */
export const MissingPermission = ({ name }: { name: string }) => <span>{name}</span>;
