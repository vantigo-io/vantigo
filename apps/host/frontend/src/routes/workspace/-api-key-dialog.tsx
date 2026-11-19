import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Checkbox,
  Code,
  CopyButton,
  Group,
  Modal,
  Select,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
  Tooltip,
} from "@mantine/core";
import { DatePickerInput } from "@mantine/dates";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconCheck, IconCopy } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { appUrl, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { PermissionDescriptor } from "../../api/authorization";
import {
  type ApiKey,
  type ApiKeySecret,
  createApiKey,
  type IntegrationDetail,
  integrationQueryKey,
  integrationsQueryKey,
  rollApiKey,
} from "../../api/integrations";
import { translateHostPermissionKey, translateHostPermissionModule } from "../../i18n";
import { fieldErrorsOf, mcpEndpoint, mcpSnippets } from "./-api-access-shared";

/** The expiries a key may be given (API access design K12): days, or a date at most 365 days ahead. */
type ExpiryChoice = "30" | "90" | "180" | "365" | "date";

const dayMs = 24 * 60 * 60 * 1000;
const isoDay = (date: Date) =>
  `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;

/** The body's expiry: the days chosen, or the start of the chosen day where the browser is. */
const expiryBody = (choice: ExpiryChoice, date: string | null) => {
  if (choice !== "date") return { expiresInDays: Number(choice) };
  if (!date) return {};
  const [year, month, day] = date.split("-").map(Number);
  return { expiresAt: new Date(year, month - 1, day).toISOString() };
};

const ExpiryFields = ({
  choice,
  date,
  onChoice,
  onDate,
  error,
}: {
  choice: ExpiryChoice;
  date: string | null;
  onChoice: (choice: ExpiryChoice) => void;
  onDate: (date: string | null) => void;
  error?: string;
}) => {
  const { t } = useI18n("host");
  const today = new Date();
  return (
    <>
      <Select
        label={t("apiAccess.expiry")}
        description={t("apiAccess.expiryHint")}
        allowDeselect={false}
        data={[
          { value: "30", label: t("apiAccess.expiryDays", { days: 30 }) },
          { value: "90", label: t("apiAccess.expiryDays", { days: 90 }) },
          { value: "180", label: t("apiAccess.expiryDays", { days: 180 }) },
          { value: "365", label: t("apiAccess.expiryDays", { days: 365 }) },
          { value: "date", label: t("apiAccess.expiryDate") },
        ]}
        value={choice}
        error={choice === "date" ? undefined : error}
        onChange={(value) => onChoice((value ?? "90") as ExpiryChoice)}
      />
      {choice === "date" && (
        <DatePickerInput
          label={t("apiAccess.expiresOn")}
          valueFormat="YYYY-MM-DD"
          minDate={isoDay(new Date(today.getTime() + dayMs))}
          maxDate={isoDay(new Date(today.getTime() + 365 * dayMs))}
          value={date}
          error={error}
          onChange={(value) => onDate(value)}
        />
      )}
    </>
  );
};

/** A value with a Copy button beside it. */
export const Copyable = ({ label, value, multiline }: { label: string; value: string; multiline?: boolean }) => {
  const { t } = useI18n("host");
  return (
    <Stack gap={4}>
      <Group justify="space-between" wrap="nowrap">
        <Text size="sm" fw={500}>
          {label}
        </Text>
        <CopyButton value={value}>
          {({ copied, copy }) => (
            <Tooltip label={copied ? t("apiAccess.copied") : t("apiAccess.copy")}>
              <ActionIcon
                variant="subtle"
                color={copied ? "teal" : "gray"}
                aria-label={t("apiAccess.copyLabel", { label })}
                onClick={copy}
              >
                {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
              </ActionIcon>
            </Tooltip>
          )}
        </CopyButton>
      </Group>
      <Code block={multiline} style={{ wordBreak: "break-all", whiteSpace: multiline ? "pre-wrap" : undefined }}>
        {value}
      </Code>
    </Stack>
  );
};

/**
 * How an MCP client connects (MCP server design M16): the Claude Code command
 * and the Cursor and VS Code blocks for the endpoint at url, named after the
 * integration, carrying apiKey or the vtg_… placeholder.
 */
export const McpSnippets = ({ url, name, apiKey }: { url: string; name: string; apiKey?: string }) => {
  const { t } = useI18n("host");
  const snippets = mcpSnippets(url, name, apiKey);
  return (
    <>
      <Copyable label={t("apiAccess.mcpClaudeCode")} value={snippets.claude} multiline />
      <Copyable label={t("apiAccess.mcpJson")} value={snippets.json} multiline />
      <Copyable label={t("apiAccess.mcpVsCode")} value={snippets.vscode} multiline />
    </>
  );
};

/**
 * The MCP connection of a key just made, beside the curl hint (M16): the
 * snippets with its secret when the integration's MCP is on, a hint to turn it
 * on when it is off, and nothing when the installation has switched MCP off.
 */
const SecretMcp = ({ secret, detail }: { secret: ApiKeySecret; detail: IntegrationDetail }) => {
  const { t } = useI18n("host");
  if (!detail.mcpAvailable) return null;
  if (detail.mcpAccess === "off") {
    return (
      <Text size="sm" c="dimmed">
        {t("apiAccess.mcpKeyOff")}
      </Text>
    );
  }
  return (
    <Stack gap="sm">
      <Text fw={600}>{t("apiAccess.mcpKeyTitle")}</Text>
      <McpSnippets url={mcpEndpoint()} name={detail.integration.name} apiKey={secret.secret} />
      <Text size="sm" c="dimmed">
        {secret.key.readOnly && detail.mcpAccess === "read_write"
          ? `${t("apiAccess.mcpKeyHint")} ${t("apiAccess.mcpKeyReadOnly")}`
          : t("apiAccess.mcpKeyHint")}
      </Text>
    </Stack>
  );
};

/**
 * The secret of a key just made or rolled (K11, K22): shown this once, with
 * Copy, the warning that it cannot be shown again and a curl example against
 * this installation's own address; for a key just made, also how an MCP
 * client connects with it (detail given).
 */
const SecretPanel = ({
  secret,
  detail,
  onDone,
}: {
  secret: ApiKeySecret;
  detail?: IntegrationDetail;
  onDone: () => void;
}) => {
  const { t } = useI18n("host");
  const example = `curl -H "Authorization: Bearer ${secret.secret}" ${window.location.origin}${appUrl("/api/v1/customers")}`;
  return (
    <Stack>
      <Alert color="orange" icon={<IconAlertTriangle size={16} />} title={t("apiAccess.secretTitle")}>
        {t("apiAccess.secretWarning")}
      </Alert>
      <Copyable label={t("apiAccess.secret")} value={secret.secret} />
      <Copyable label={t("apiAccess.curlExample")} value={example} multiline />
      <Text size="sm" c="dimmed">
        {t("apiAccess.curlHint")}
      </Text>
      {detail && <SecretMcp secret={secret} detail={detail} />}
      <Button onClick={onDone}>{t("apiAccess.secretDone")}</Button>
    </Stack>
  );
};

type KeyDraft = {
  name: string;
  expiry: ExpiryChoice;
  date: string | null;
  narrow: boolean;
  showUngranted: boolean;
  permissions: string[];
  readOnly: boolean;
  networks: string;
};

const emptyKeyDraft: KeyDraft = {
  name: "",
  expiry: "90",
  date: null,
  narrow: false,
  showUngranted: false,
  permissions: [],
  readOnly: false,
  networks: "",
};

type ChecklistEntry = { key: string; descriptor?: PermissionDescriptor; granted: boolean; apiNever: boolean };

/**
 * The narrowing checklist, grouped by module as the role editor is (K11,
 * K22): what the roles grant, a permission no key ever holds shown and not
 * offered, and, when asked for, the rest of the catalog marked "not granted
 * by the roles", which a key may name and which does nothing until they do.
 */
const NarrowingChecklist = ({
  detail,
  catalog,
  draft,
  setDraft,
}: {
  detail: IntegrationDetail;
  catalog: readonly PermissionDescriptor[];
  draft: KeyDraft;
  setDraft: (draft: KeyDraft) => void;
}) => {
  const { t } = useI18n("host");
  const byKey = new Map(catalog.map((descriptor) => [descriptor.key, descriptor]));
  const effective = new Map(detail.effectivePermissions.map((permission) => [permission.key, permission.apiNever]));
  const entries: ChecklistEntry[] = [
    ...detail.effectivePermissions.map((permission) => ({
      key: permission.key,
      descriptor: byKey.get(permission.key),
      granted: true,
      apiNever: permission.apiNever,
    })),
    ...(draft.showUngranted
      ? catalog
          .filter((descriptor) => !effective.has(descriptor.key))
          .map((descriptor) => ({ key: descriptor.key, descriptor, granted: false, apiNever: false }))
      : []),
  ];
  const groups = new Map<string, ChecklistEntry[]>();
  for (const entry of entries) {
    const group = translateHostPermissionModule(entry.key, entry.descriptor?.module ?? entry.key.split(":")[0], t);
    groups.set(group, [...(groups.get(group) ?? []), entry]);
  }
  const toggle = (key: string, checked: boolean) =>
    setDraft({
      ...draft,
      permissions: checked ? [...draft.permissions, key] : draft.permissions.filter((one) => one !== key),
    });
  return (
    <Stack gap="sm">
      <Switch
        label={t("apiAccess.showUngranted")}
        checked={draft.showUngranted}
        onChange={(event) => {
          const showUngranted = event.currentTarget.checked;
          // Hiding the rest of the catalog drops what was ticked in it: the
          // key is never narrowed to a permission the dialog no longer shows.
          setDraft({
            ...draft,
            showUngranted,
            permissions: showUngranted ? draft.permissions : draft.permissions.filter((key) => effective.has(key)),
          });
        }}
      />
      {entries.length === 0 && (
        <Text size="sm" c="dimmed">
          {t("apiAccess.noPermissions")}
        </Text>
      )}
      {[...groups.entries()].map(([group, items]) => (
        <Stack key={group} gap="xs">
          <Text fw={600}>{group}</Text>
          {items.map((entry) => (
            <Checkbox
              key={entry.key}
              disabled={entry.apiNever}
              label={
                <Group gap="xs">
                  <span>{translateHostPermissionKey(entry.key, t, entry.descriptor?.displayName)}</span>
                  {entry.descriptor?.sensitive && <Badge color="orange">{t("apiAccess.sensitive")}</Badge>}
                  {entry.apiNever && <Badge color="gray">{t("apiAccess.apiNever")}</Badge>}
                  {!entry.granted && (
                    <Badge color="yellow" variant="outline">
                      {t("apiAccess.notGranted")}
                    </Badge>
                  )}
                </Group>
              }
              checked={!entry.apiNever && draft.permissions.includes(entry.key)}
              onChange={(event) => toggle(entry.key, event.currentTarget.checked)}
            />
          ))}
        </Stack>
      ))}
    </Stack>
  );
};

/** The networks a draft names, one per line or separated by commas. */
const networksOf = (text: string) =>
  text
    .split(/[\s,]+/)
    .map((value) => value.trim())
    .filter(Boolean);

/**
 * *New API key* (K11, K12, K22): its name, expiry, narrowing, *Read only* and
 * networks, then its secret, shown once. Closing the dialog forgets the
 * secret: it is in no query, and the mutation that brought it is reset.
 */
export const NewKeyDialog = ({
  opened,
  onClose,
  detail,
  catalog,
}: {
  opened: boolean;
  onClose: () => void;
  detail: IntegrationDetail;
  catalog: readonly PermissionDescriptor[];
}) => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<KeyDraft>(emptyKeyDraft);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [secret, setSecret] = useState<ApiKeySecret | null>(null);
  const id = detail.integration.id;
  const create = useMutation({
    gcTime: 0,
    mutationFn: (d: KeyDraft) =>
      createApiKey(id, {
        name: d.name.trim(),
        ...expiryBody(d.expiry, d.date),
        ...(d.narrow ? { permissions: d.permissions } : {}),
        readOnly: d.readOnly,
        allowedNetworks: networksOf(d.networks),
      }),
    onSuccess: async (made) => {
      setSecret(made);
      await queryClient.invalidateQueries({ queryKey: integrationQueryKey(id) });
      await queryClient.invalidateQueries({ queryKey: integrationsQueryKey, exact: true });
    },
    onError: (error) => {
      setErrors(fieldErrorsOf(error));
      notifications.show({ color: "red", title: t("apiAccess.couldNotSave"), message: (error as Error).message });
    },
  });
  const close = () => {
    setSecret(null);
    create.reset();
    setDraft(emptyKeyDraft);
    setErrors({});
    onClose();
  };
  return (
    <Modal
      opened={opened}
      onClose={close}
      title={secret ? t("apiAccess.secretFor", { name: secret.key.name }) : t("apiAccess.newKey")}
      size="lg"
      closeOnClickOutside={secret === null}
      closeOnEscape={secret === null}
      withCloseButton={secret === null}
      centered
    >
      {secret ? (
        <SecretPanel secret={secret} detail={detail} onDone={close} />
      ) : (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate(draft);
          }}
        >
          <Stack>
            <TextInput
              label={t("apiAccess.keyName")}
              description={t("apiAccess.keyNameHint")}
              required
              maxLength={100}
              value={draft.name}
              error={errors.name}
              onChange={(event) => setDraft({ ...draft, name: event.currentTarget.value })}
            />
            <ExpiryFields
              choice={draft.expiry}
              date={draft.date}
              error={errors.expiresInDays ?? errors.expiresAt}
              onChoice={(expiry) => setDraft({ ...draft, expiry })}
              onDate={(date) => setDraft({ ...draft, date })}
            />
            <Switch
              label={t("apiAccess.narrow")}
              description={t("apiAccess.narrowHint")}
              checked={draft.narrow}
              onChange={(event) => setDraft({ ...draft, narrow: event.currentTarget.checked })}
            />
            {draft.narrow && <NarrowingChecklist detail={detail} catalog={catalog} draft={draft} setDraft={setDraft} />}
            {errors.permissions && (
              <Text size="sm" c="red">
                {errors.permissions}
              </Text>
            )}
            <Switch
              label={t("apiAccess.readOnly")}
              description={t("apiAccess.readOnlyHint")}
              checked={draft.readOnly}
              onChange={(event) => setDraft({ ...draft, readOnly: event.currentTarget.checked })}
            />
            <Textarea
              label={t("apiAccess.networks")}
              description={t("apiAccess.networksHint")}
              autosize
              minRows={2}
              value={draft.networks}
              error={errors.allowedNetworks}
              onChange={(event) => setDraft({ ...draft, networks: event.currentTarget.value })}
            />
            <Button
              type="submit"
              loading={create.isPending}
              disabled={!draft.name.trim() || (draft.expiry === "date" && !draft.date)}
            >
              {t("apiAccess.createKey")}
            </Button>
          </Stack>
        </form>
      )}
    </Modal>
  );
};

/** The overlaps a roll offers, in hours (K13): 0 to 7 days, 24 hours by default. */
const overlapChoices = [0, 1, 24, 72, 168] as const;

/**
 * *Roll* (K13): a new key in place of one, with its name, narrowing,
 * read-only flag and networks and a new expiry, the old one working on for
 * the overlap; then the new secret, shown once.
 */
export const RollKeyDialog = ({
  apiKey,
  integrationId,
  onClose,
}: {
  apiKey: ApiKey | null;
  integrationId: string;
  onClose: () => void;
}) => {
  const { t } = useI18n("host");
  const queryClient = useQueryClient();
  const [overlap, setOverlap] = useState("24");
  const [expiry, setExpiry] = useState<ExpiryChoice>("90");
  const [date, setDate] = useState<string | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [secret, setSecret] = useState<ApiKeySecret | null>(null);
  const roll = useMutation({
    gcTime: 0,
    mutationFn: (key: ApiKey) =>
      rollApiKey(integrationId, key.id, { overlapHours: Number(overlap), ...expiryBody(expiry, date) }),
    onSuccess: async (made) => {
      setSecret(made);
      await queryClient.invalidateQueries({ queryKey: integrationQueryKey(integrationId) });
      await queryClient.invalidateQueries({ queryKey: integrationsQueryKey, exact: true });
    },
    onError: (error) => {
      setErrors(fieldErrorsOf(error));
      notifications.show({ color: "red", title: t("apiAccess.couldNotRoll"), message: (error as Error).message });
    },
  });
  const close = () => {
    setSecret(null);
    roll.reset();
    setOverlap("24");
    setExpiry("90");
    setDate(null);
    setErrors({});
    onClose();
  };
  return (
    <Modal
      opened={apiKey !== null}
      onClose={close}
      title={
        secret
          ? t("apiAccess.secretFor", { name: secret.key.name })
          : t("apiAccess.rollTitle", { name: apiKey?.name ?? "" })
      }
      size="lg"
      closeOnClickOutside={secret === null}
      closeOnEscape={secret === null}
      withCloseButton={secret === null}
      centered
    >
      {secret ? (
        <SecretPanel secret={secret} onDone={close} />
      ) : (
        apiKey && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              roll.mutate(apiKey);
            }}
          >
            <Stack>
              <Text size="sm">{t("apiAccess.rollBody")}</Text>
              <Alert color="orange" icon={<IconAlertTriangle size={16} />}>
                {t("apiAccess.rollLeaked")}
              </Alert>
              <Select
                label={t("apiAccess.overlap")}
                description={t("apiAccess.overlapHint")}
                allowDeselect={false}
                data={overlapChoices.map((hours) => ({
                  value: String(hours),
                  label: t(`apiAccess.overlap_${hours}`),
                }))}
                value={overlap}
                error={errors.overlapHours}
                onChange={(value) => setOverlap(value ?? "24")}
              />
              <ExpiryFields
                choice={expiry}
                date={date}
                error={errors.expiresInDays ?? errors.expiresAt}
                onChoice={setExpiry}
                onDate={setDate}
              />
              <Button type="submit" loading={roll.isPending} disabled={expiry === "date" && !date}>
                {t("apiAccess.roll")}
              </Button>
            </Stack>
          </form>
        )
      )}
    </Modal>
  );
};
