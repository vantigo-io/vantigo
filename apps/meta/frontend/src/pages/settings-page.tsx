import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Checkbox,
  Code,
  CopyButton,
  Group,
  Paper,
  PasswordInput,
  Radio,
  Stack,
  Text,
  TextInput,
  Title,
  Tooltip,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconCheck, IconCopy } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { ApiValidationError, META_QUERY_KEY } from "../api/request";
import {
  addPage,
  type MetaPage,
  type MetaSettings,
  regenerateVerifyToken,
  removePage,
  settingsQueryOptions,
  updateSettings,
} from "../api/settings";
import { choicesOf, graphErrorText, refusalText } from "../lib/words";

const settingsKey = settingsQueryOptions().queryKey;

const Card = ({ title, children }: { title: string; children: React.ReactNode }) => (
  <Paper withBorder p="md" radius="md" component="section" aria-label={title}>
    <Stack gap="sm">
      <Title order={3} fz="h4">
        {title}
      </Title>
      {children}
    </Stack>
  </Paper>
);

/** A value to paste, shown in full with its copy button. */
const CopyLine = ({ label, value }: { label: string; value: string }) => {
  const { t } = useI18n("meta");
  return (
    <Stack gap={2}>
      <Text size="xs" c="dimmed">
        {label}
      </Text>
      <Group gap="xs" wrap="nowrap" align="flex-start">
        <Code block style={{ flex: 1, whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
          {value}
        </Code>
        <CopyButton value={value}>
          {({ copied, copy }) => (
            <Tooltip label={copied ? t("copied") : t("copy")}>
              <ActionIcon
                variant="subtle"
                size="sm"
                color={copied ? "teal" : "gray"}
                aria-label={t("copyValue", { value: label })}
                onClick={copy}
              >
                {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
              </ActionIcon>
            </Tooltip>
          )}
        </CopyButton>
      </Group>
    </Stack>
  );
};

interface AppValues {
  appId: string;
  appSecret: string;
  humanAgentGranted: boolean;
}

/** The app id, the write-only secret and the Human Agent switch; saving waits for the public host. */
const AppCard = ({ settings }: { settings: MetaSettings }) => {
  const { t } = useI18n("meta");
  const queryClient = useQueryClient();
  const form = useForm<AppValues>({
    initialValues: {
      appId: settings.appId ?? "",
      appSecret: "",
      humanAgentGranted: settings.humanAgentGranted,
    },
  });

  const save = useMutation({
    mutationFn: (values: AppValues) =>
      updateSettings({
        ...(values.appId.trim() ? { appId: values.appId.trim() } : {}),
        // An empty secret keeps the stored one.
        ...(values.appSecret ? { appSecret: values.appSecret } : {}),
        humanAgentGranted: values.humanAgentGranted,
      }),
    onSuccess: (answer) => {
      // The secret leaves the page with the answer: nothing here holds it any longer.
      queryClient.setQueryData(settingsKey, answer);
      const next = { appId: answer.appId ?? "", appSecret: "", humanAgentGranted: answer.humanAgentGranted };
      form.setValues(next);
      form.resetDirty(next);
      notifications.show({ color: "teal", title: t("saved"), message: t("savedBody") });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError && !(error as { code?: string }).code) {
        const fields = Object.fromEntries(
          Object.entries(error.fieldErrors).filter(([field]) => field === "appId" || field === "appSecret"),
        );
        if (Object.keys(fields).length > 0) {
          form.setErrors(fields);
          return;
        }
      }
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalText(error, t) });
    },
  });

  return (
    <Card title={t("appTitle")}>
      <Text size="sm">{t("appBody")}</Text>
      <form onSubmit={form.onSubmit((values) => save.mutate(values))} aria-label={t("appTitle")}>
        <Stack gap="sm">
          <TextInput
            label={t("appId")}
            description={t("appIdDescription")}
            autoComplete="off"
            inputMode="numeric"
            {...form.getInputProps("appId")}
          />
          <PasswordInput
            label={t("appSecret")}
            description={
              settings.appSecretSet ? t("appSecretSet") : `${t("appSecretNotSet")} ${t("appSecretDescription")}`
            }
            autoComplete="new-password"
            {...form.getInputProps("appSecret")}
          />
          <Checkbox
            label={t("humanAgent")}
            description={t("humanAgentDescription")}
            {...form.getInputProps("humanAgentGranted", { type: "checkbox" })}
          />
          {settings.status.lastGraphErrorCode === "human_agent_not_permitted" ? (
            <Alert color="orange" variant="light" role="status">
              {t("humanAgentNotPermitted")}
            </Alert>
          ) : null}
          <Group justify="flex-end">
            <Button type="submit" loading={save.isPending} disabled={!settings.publicHostConfigured}>
              {t("save")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Card>
  );
};

/** The webhook's address and verify token to paste into Meta, and the token's regeneration. */
const WebhookCard = ({ settings }: { settings: MetaSettings }) => {
  const { t } = useI18n("meta");
  const queryClient = useQueryClient();
  const regenerate = useMutation({
    mutationFn: regenerateVerifyToken,
    onSuccess: (answer) => {
      queryClient.setQueryData(settingsKey, answer);
      notifications.show({ color: "teal", title: t("regenerated"), message: t("regeneratedBody") });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotRegenerate"), message: refusalText(error, t) }),
  });
  const confirmRegenerate = () =>
    modals.openConfirmModal({
      title: t("regenerateTitle"),
      children: <Text size="sm">{t("regenerateConfirm")}</Text>,
      labels: { confirm: t("regenerate"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => regenerate.mutate(),
    });

  return (
    <Card title={t("webhookTitle")}>
      <Text size="sm">{t("webhookBody")}</Text>
      {settings.webhookUrl ? (
        <CopyLine label={t("webhookUrl")} value={settings.webhookUrl} />
      ) : (
        <Text size="sm" c="dimmed">
          {t("webhookUrlMissing")}
        </Text>
      )}
      <CopyLine label={t("verifyToken")} value={settings.verifyToken} />
      <Group>
        <Button variant="default" onClick={confirmRegenerate} loading={regenerate.isPending}>
          {t("regenerate")}
        </Button>
      </Group>
    </Card>
  );
};

const PageRow = ({ page }: { page: MetaPage }) => {
  const { t, formatters } = useI18n("meta");
  const queryClient = useQueryClient();
  const remove = useMutation({
    mutationFn: () => removePage(page.pageId),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("pageRemoved"), message: page.name });
      await queryClient.invalidateQueries({ queryKey: [META_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotRemove"), message: refusalText(error, t) }),
  });
  const confirmRemove = () =>
    modals.openConfirmModal({
      title: t("removePageTitle", { name: page.name }),
      children: <Text size="sm">{t("removePageConfirm")}</Text>,
      labels: { confirm: t("removePage"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });
  const date = (value: string) => formatters.formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" });

  return (
    <Paper withBorder p="sm" radius="md" component="li" style={{ listStyle: "none" }}>
      <Stack gap="xs">
        <Group justify="space-between" wrap="nowrap" align="flex-start">
          <Stack gap={0}>
            <Group gap="xs">
              <Text fw={500}>{page.name}</Text>
              <Badge
                color={page.state === "active" ? "teal" : "red"}
                variant={page.state === "active" ? "light" : "filled"}
              >
                {t(`state.${page.state}`)}
              </Badge>
            </Group>
            <Text size="xs" c="dimmed">
              {t("pageId", { id: page.pageId })}
              {" · "}
              {t("pageAdded", { date: date(page.addedAt) })}
              {page.checkedAt ? ` · ${t("pageChecked", { date: date(page.checkedAt) })}` : ""}
            </Text>
          </Stack>
          <Button
            variant="subtle"
            color="red"
            size="compact-sm"
            aria-label={t("removePageLabel", { name: page.name })}
            onClick={confirmRemove}
            loading={remove.isPending}
          >
            {t("removePage")}
          </Button>
        </Group>
        {page.state === "token_rejected" ? (
          <Alert color="red" variant="light">
            {t("tokenRejectedHint")}
          </Alert>
        ) : null}
      </Stack>
    </Paper>
  );
};

/** The Pages: each with its state and Remove, and the form that adds one by its access token. */
const PagesCard = ({ settings }: { settings: MetaSettings }) => {
  const { t } = useI18n("meta");
  const queryClient = useQueryClient();
  const [token, setToken] = useState("");
  const [problem, setProblem] = useState<string | null>(null);
  // The Pages a token that manages several was refused for, and the one chosen.
  const [choices, setChoices] = useState<{ id: string; name: string }[]>([]);
  const [chosen, setChosen] = useState("");
  const add = useMutation({
    mutationFn: () => addPage(token.trim(), chosen || undefined),
    onSuccess: async (page) => {
      // The token leaves the page with the answer: nothing here holds it any longer.
      setToken("");
      setProblem(null);
      setChoices([]);
      setChosen("");
      notifications.show({
        color: "teal",
        title: t("pageAddedNotice"),
        message: t("pageAddedBody", { name: page.name }),
      });
      await queryClient.invalidateQueries({ queryKey: [META_QUERY_KEY] });
    },
    onError: (error) => {
      const listed = choicesOf(error);
      setChoices(listed);
      setChosen("");
      setProblem(listed.length > 0 ? null : refusalText(error, t));
    },
  });

  return (
    <Card title={t("pagesTitle")}>
      <Text size="sm">{t("pagesBody")}</Text>
      {settings.pages.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("noPages")}
        </Text>
      ) : (
        <Stack gap="xs" component="ul" p={0} m={0} aria-label={t("pagesTitle")}>
          {settings.pages.map((page) => (
            <PageRow key={page.pageId} page={page} />
          ))}
        </Stack>
      )}
      <form
        aria-label={t("addPage")}
        onSubmit={(event) => {
          event.preventDefault();
          if (!token.trim()) {
            setProblem(t("accessTokenRequired"));
            return;
          }
          if (choices.length > 0 && !chosen) return;
          add.mutate();
        }}
      >
        <Stack gap="sm">
          <PasswordInput
            label={t("accessToken")}
            description={t("accessTokenDescription")}
            autoComplete="new-password"
            value={token}
            onChange={(event) => {
              setToken(event.currentTarget.value);
              // Another token manages other Pages: the choice made for the last one is void.
              setChoices([]);
              setChosen("");
            }}
            error={problem}
          />
          {choices.length > 0 ? (
            <Radio.Group
              label={t("choosePage")}
              description={t("choosePageDescription")}
              value={chosen}
              onChange={setChosen}
              withAsterisk
            >
              <Stack gap="xs" mt="xs">
                {choices.map((choice) => (
                  <Radio
                    key={choice.id}
                    value={choice.id}
                    label={choice.name}
                    description={t("choicePageId", { id: choice.id })}
                  />
                ))}
              </Stack>
            </Radio.Group>
          ) : null}
          <Group justify="flex-end">
            <Button type="submit" loading={add.isPending}>
              {t("addPage")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Card>
  );
};

/** What the Owner reads to see whether Meta reaches the instance and the instance reaches Meta. */
const StatusCard = ({ settings }: { settings: MetaSettings }) => {
  const { t, formatters } = useI18n("meta");
  const { status } = settings;
  const when = (value?: string | null) =>
    value ? formatters.formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" }) : t("never");
  const rows: [string, string][] = [
    [t("lastWebhook"), when(status.lastWebhookAt)],
    [t("lastRejected"), when(status.lastRejectedAt)],
    [
      t("lastGraphError"),
      status.lastGraphErrorCode
        ? [
            graphErrorText(status.lastGraphErrorCode, t),
            status.lastGraphErrorMessage,
            status.lastGraphErrorAt ? when(status.lastGraphErrorAt) : null,
          ]
            .filter(Boolean)
            .join(" · ")
        : t("never"),
    ],
  ];
  return (
    <Card title={t("statusTitle")}>
      {rows.map(([label, value]) => (
        <Stack gap={0} key={label}>
          <Text size="xs" c="dimmed">
            {label}
          </Text>
          <Text size="sm">{value}</Text>
        </Stack>
      ))}
    </Card>
  );
};

/**
 * Integrations › Meta (Messenger design N9, N10): the Owner's page for the
 * Meta app, the webhook's address and verify token, the Human Agent switch,
 * the Pages and the status line. The host gates the page; the server refuses
 * anyone but an Owner regardless.
 */
export const MetaSettingsPage = () => {
  const { t } = useI18n("meta");
  const query = useQuery(settingsQueryOptions());
  return (
    <Stack gap="lg">
      <PageHeader title={t("meta")} description={t("metaDescription")} />
      {query.isPending ? (
        <ContentSkeleton />
      ) : query.isError ? (
        <Alert color="red" variant="light" role="alert" title={t("loadFailed")}>
          <Button variant="default" size="xs" onClick={() => void query.refetch()}>
            {t("tryAgain")}
          </Button>
        </Alert>
      ) : (
        <>
          {query.data.publicHostConfigured ? null : (
            <Alert color="yellow" variant="light" role="alert" title={t("publicHostMissingTitle")}>
              {t("publicHostMissing")}
            </Alert>
          )}
          <AppCard key={query.data.appId ?? ""} settings={query.data} />
          <WebhookCard settings={query.data} />
          <PagesCard settings={query.data} />
          <StatusCard settings={query.data} />
        </>
      )}
    </Stack>
  );
};
