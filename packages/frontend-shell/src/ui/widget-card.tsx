import { Button, Card, Group, Skeleton, Stack, Text } from "@mantine/core";
import { Component, type ErrorInfo, type ReactNode } from "react";
import { registerCatalog, useI18n } from "../i18n";
import { shellCatalog } from "../i18n/catalogs/shell";

registerCatalog("shell", shellCatalog);

export interface WidgetCardEmptyState {
  message: ReactNode;
  action?: ReactNode;
}

export interface WidgetCardProps {
  title: ReactNode;
  description?: ReactNode;
  freshness?: string;
  action?: ReactNode;
  loading?: boolean;
  empty?: boolean;
  emptyState?: WidgetCardEmptyState;
  children?: ReactNode;
}

const WidgetFailed = ({ retry }: { retry: () => void }) => {
  const { t } = useI18n("shell");
  return (
    <Stack align="center" gap="sm" py="xl" role="alert">
      <Text c="dimmed" ta="center">
        {t("widgetFailed")}
      </Text>
      <Button variant="light" size="xs" onClick={retry}>
        {t("widgetRetry")}
      </Button>
    </Stack>
  );
};

interface WidgetBoundaryState {
  crashed: boolean;
}

/**
 * Keeps one widget's crash inside its card. Widgets from several modules
 * share the dashboard route, so the router's per-route boundary would take
 * the whole page down for one bad widget; this leaves the title in place and
 * offers a retry, which re-mounts the widget's body.
 */
class WidgetBoundary extends Component<{ children: ReactNode }, WidgetBoundaryState> {
  state: WidgetBoundaryState = { crashed: false };

  static getDerivedStateFromError(): WidgetBoundaryState {
    return { crashed: true };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error("Widget failed to render", error, info.componentStack);
  }

  render() {
    return this.state.crashed ? <WidgetFailed retry={() => this.setState({ crashed: false })} /> : this.props.children;
  }
}

export const WidgetCard = ({
  title,
  description,
  freshness,
  action,
  loading = false,
  empty = false,
  emptyState,
  children,
}: WidgetCardProps) => (
  <Card withBorder padding="lg" radius="lg" h="100%">
    {loading ? (
      <>
        <Group justify="space-between" align="flex-start" wrap="nowrap">
          <Stack gap={4} flex={1}>
            <Skeleton h={18} w="40%" />
            {description && <Skeleton h={14} w="60%" />}
          </Stack>
          {(freshness || action) && (
            <Group gap="xs" wrap="nowrap">
              {freshness && <Skeleton h={12} w={96} />}
              {action && <Skeleton h={28} w={72} />}
            </Group>
          )}
        </Group>
        <Stack gap="sm" mt="lg">
          <Skeleton h={16} />
          <Skeleton h={16} w="85%" />
          <Skeleton h={120} />
        </Stack>
      </>
    ) : (
      <>
        <Group justify="space-between" align="flex-start" wrap="nowrap">
          <Stack gap={2} flex={1}>
            <Text fw={600}>{title}</Text>
            {description && (
              <Text size="sm" c="dimmed">
                {description}
              </Text>
            )}
          </Stack>
          {(freshness || action) && (
            <Group gap="xs" align="flex-start" wrap="nowrap">
              {freshness && (
                <Text size="xs" c="dimmed" ta="right">
                  {freshness}
                </Text>
              )}
              {action}
            </Group>
          )}
        </Group>
        {empty && emptyState ? (
          <Stack align="center" gap="sm" py="xl">
            <Text c="dimmed" ta="center">
              {emptyState.message}
            </Text>
            {emptyState.action}
          </Stack>
        ) : (
          <WidgetBoundary>{children}</WidgetBoundary>
        )}
      </>
    )}
  </Card>
);
