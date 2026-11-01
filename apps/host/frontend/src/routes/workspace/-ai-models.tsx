import { Alert, Badge, Card, Group, Select, Skeleton, Stack, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { type AiSettings, type AiTask, aiSettingsQueryKey, fetchAiSettings, updateAiTask } from "../../api/ai";
import type { ApiError } from "../../api/request";
import { NotFoundPage } from "../../components/errors";
import "../../i18n";

const providerNames: Record<string, string> = { openai: "OpenAI", "openai-compatible": "OpenAI-compatible" };

const isStale = (error: unknown) =>
  (error as ApiError | undefined)?.status === 412 || (error as ApiError | undefined)?.code === "version_mismatch";
const isNotConfigured = (error: unknown) => (error as ApiError | undefined)?.status === 404;
const messageOf = (error: unknown, fallback: string) =>
  error instanceof Error && error.message ? error.message : fallback;

/** One task in the cached settings replaced by the answer to its save. */
const withTask = (settings: AiSettings, saved: AiTask): AiSettings => ({
  ...settings,
  modules: settings.modules.map((group) => ({
    ...group,
    tasks: group.tasks.map((task) => (task.key === saved.key ? saved : task)),
  })),
});

/** A read-only fact of the plan, with the label above it. */
const Fact = ({ label, children }: { label: string; children: React.ReactNode }) => (
  <Stack gap={4}>
    <Text size="sm" c="dimmed">
      {label}
    </Text>
    {children}
  </Stack>
);

/**
 * One AI task: what it is, a select of the models the plan allows and, while
 * nobody has chosen, a badge saying where the model comes from. The save is the
 * change itself, under If-Match on the task's version; a version that has moved
 * on reloads the page's settings and says so, as the single sign-on card does.
 * One allowed model locks the select with that model shown.
 */
const TaskRow = ({ task, allowed }: { task: AiTask; allowed: readonly string[] }) => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const locked = allowed.length <= 1;

  const save = useMutation({
    mutationFn: (model: string) => updateAiTask(task.key, model, task.version),
    onSuccess: (saved) => {
      queryClient.setQueryData<AiSettings>(aiSettingsQueryKey, (current) => current && withTask(current, saved));
      notifications.show({
        color: "teal",
        title: t("admin.ai.saved"),
        message: t("admin.ai.savedBody", { task: saved.display, model: saved.model }),
      });
    },
    onError: (error) => {
      if (isStale(error)) {
        notifications.show({ color: "yellow", title: t("admin.ai.staleTitle"), message: t("admin.ai.staleBody") });
        void queryClient.invalidateQueries({ queryKey: aiSettingsQueryKey });
        return;
      }
      notifications.show({
        color: "red",
        title: t("admin.ai.saveFailed"),
        message: messageOf(error, t("common.requestFailed")),
      });
    },
  });

  const badge =
    task.source === "default"
      ? t("admin.ai.sourceDefault")
      : task.source === "plan"
        ? t("admin.ai.sourcePlan")
        : undefined;

  return (
    <Group justify="space-between" align="flex-start" wrap="wrap" gap="md">
      <Stack gap={2} maw={520} style={{ flex: "1 1 320px" }}>
        <Text fw={500}>{task.display}</Text>
        <Text size="sm" c="dimmed">
          {task.description}
        </Text>
      </Stack>
      <Group gap="sm" align="center" wrap="nowrap">
        {badge && (
          <Badge variant="light" color="gray">
            {badge}
          </Badge>
        )}
        <Select
          aria-label={t("admin.ai.modelFor", { task: task.display })}
          data={[...allowed]}
          value={task.model}
          allowDeselect={false}
          disabled={locked || save.isPending}
          w={220}
          onChange={(model) => {
            if (model && model !== task.model) save.mutate(model);
          }}
        />
      </Group>
    </Group>
  );
};

/**
 * Integrations › AI models (Integrations design T13): the AI plan Vantigo
 * provisioned, shown as facts with no field, and a model per task the enabled
 * modules perform. The key is never shown; the page says only that one is
 * configured.
 */
export const AiModelsPage = () => {
  const { t } = useI18n("host");
  const settings = useQuery({ queryKey: aiSettingsQueryKey, queryFn: fetchAiSettings, retry: false });
  const moduleLabel = (module: string) => {
    const key = `navigation.${module}`;
    const label = t(key);
    return label === key ? module : label;
  };

  if (settings.isError && isNotConfigured(settings.error)) return <NotFoundPage />;
  const data = settings.data;
  return (
    <Stack gap="lg">
      <PageHeader title={t("admin.ai.title")} description={t("admin.ai.description")} />
      {settings.isPending && <Skeleton h={200} />}
      {settings.isError && !isNotConfigured(settings.error) && <Alert color="red">{t("admin.ai.loadFailed")}</Alert>}
      {data && (
        <>
          <Card withBorder radius="md" padding="lg">
            <Title order={3} fz="h4" mb="md">
              {t("admin.ai.plan")}
            </Title>
            <Stack gap="md">
              <Fact label={t("admin.ai.provider")}>
                <Text>{providerNames[data.provider] ?? data.provider}</Text>
              </Fact>
              <Fact label={t("admin.ai.allowedModels")}>
                <Group gap="xs">
                  {data.allowedModels.map((model) => (
                    <Badge key={model} variant="outline">
                      {model}
                    </Badge>
                  ))}
                </Group>
              </Fact>
              <Fact label={t("admin.ai.apiKey")}>
                <Text>{data.keyConfigured ? t("admin.ai.keyConfigured") : t("admin.ai.keyNotConfigured")}</Text>
              </Fact>
            </Stack>
          </Card>
          {data.allowedModels.length <= 1 && <Alert color="blue">{t("admin.ai.lockedHint")}</Alert>}
          {data.modules.length === 0 && <Text c="dimmed">{t("admin.ai.noTasks")}</Text>}
          {data.modules.map((group) => (
            <Card key={group.module} withBorder radius="md" padding="lg">
              <Title order={3} fz="h4" mb="md">
                {moduleLabel(group.module)}
              </Title>
              <Stack gap="lg">
                {group.tasks.map((task) => (
                  <TaskRow key={task.key} task={task} allowed={data.allowedModels} />
                ))}
              </Stack>
            </Card>
          ))}
        </>
      )}
    </Stack>
  );
};
