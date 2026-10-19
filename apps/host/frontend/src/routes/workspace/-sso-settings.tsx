import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Code,
  CopyButton,
  Divider,
  Group,
  Modal,
  NumberInput,
  PasswordInput,
  SegmentedControl,
  Select,
  SimpleGrid,
  Skeleton,
  Stack,
  Text,
  Textarea,
  TextInput,
  ThemeIcon,
  Title,
  Tooltip,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconCheck, IconCopy, IconKey } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { ApiError } from "../../api/request";
import {
  endScimOverlap,
  fetchSsoSettings,
  issueScimToken,
  removeOidc,
  removeScim,
  type SsoOidcSettings,
  type SsoOidcSettingsRequest,
  type SsoProvider,
  type SsoScimToken,
  type SsoSettings,
  ssoSettingsQueryKey,
  updateOidcSettings,
} from "../../api/sso";

const googleAuthority = "https://accounts.google.com";
const maxOverlapHours = 24;

type Provider = SsoProvider | "off";
type Credential = "secret" | "file";

/** The OIDC form as the Owner edits it; the secret starts blank, which means "keep the stored one". */
interface OidcDraft {
  provider: Provider;
  authority: string;
  clientId: string;
  credential: Credential;
  clientSecret: string;
  workloadTokenFile: string;
  allowedDomains: string;
  displayName: string;
}

type OidcField = Exclude<keyof SsoOidcSettingsRequest, "confirmIssuerChange">;
const oidcFields: readonly OidcField[] = [
  "provider",
  "authority",
  "clientId",
  "clientSecret",
  "workloadTokenFile",
  "allowedDomains",
  "displayName",
];
type FieldErrors = Partial<Record<OidcField, string>>;

const draftFrom = (oidc: SsoOidcSettings): OidcDraft => ({
  provider: oidc.enabled && oidc.provider ? oidc.provider : "off",
  authority: oidc.authority ?? "",
  clientId: oidc.clientId ?? "",
  credential: oidc.workloadTokenFile ? "file" : "secret",
  clientSecret: "",
  workloadTokenFile: oidc.workloadTokenFile ?? "",
  allowedDomains: oidc.allowedDomains.join("\n"),
  displayName: oidc.displayName ?? "",
});

const sameDraft = (a: OidcDraft, b: OidcDraft) =>
  (Object.keys(a) as Array<keyof OidcDraft>).every((key) => a[key] === b[key]);

/** Commas, spaces or one per line: the server trims, lower-cases and drops the duplicates. */
const parseDomains = (text: string) => text.split(/[\s,;]+/).filter(Boolean);

/**
 * The PUT body: the whole provider, with only the credential the Owner chose — a token
 * file for Entra, or a secret, which is left out while blank so the sealed one is kept.
 */
const requestFrom = (draft: OidcDraft, provider: SsoProvider, confirmIssuerChange: boolean): SsoOidcSettingsRequest => {
  const body: SsoOidcSettingsRequest = {
    provider,
    authority: draft.authority.trim(),
    clientId: draft.clientId.trim(),
  };
  if (provider === "entra" && draft.credential === "file") {
    if (draft.workloadTokenFile.trim()) body.workloadTokenFile = draft.workloadTokenFile.trim();
  } else if (draft.clientSecret) {
    body.clientSecret = draft.clientSecret;
  }
  if (provider === "google") body.allowedDomains = parseDomains(draft.allowedDomains);
  if (draft.displayName.trim()) body.displayName = draft.displayName.trim();
  if (confirmIssuerChange) body.confirmIssuerChange = true;
  return body;
};

const errorCode = (error: unknown) => (error as ApiError | undefined)?.code;
const isStale = (error: unknown) =>
  (error as ApiError | undefined)?.status === 412 || errorCode(error) === "version_mismatch";
const linksExist = (error: unknown) => errorCode(error) === "oidc_links_exist";
const messageOf = (error: unknown, fallback: string) =>
  error instanceof Error && error.message ? error.message : fallback;

/** A read-only value the Owner registers elsewhere, with a button that copies it. */
const CopyField = ({ label, description, value }: { label: string; description?: string; value: string }) => {
  const { t } = useI18n("host");
  return (
    <TextInput
      label={label}
      description={description}
      value={value}
      readOnly
      rightSection={
        <CopyButton value={value}>
          {({ copied, copy }) => (
            <Tooltip label={copied ? t("admin.sso.copied") : t("admin.sso.copy")}>
              <ActionIcon
                variant="subtle"
                color={copied ? "teal" : "gray"}
                aria-label={t("admin.sso.copyLabel", { label })}
                onClick={copy}
              >
                {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
              </ActionIcon>
            </Tooltip>
          )}
        </CopyButton>
      }
    />
  );
};

/**
 * The installation's single sign-on (Entra design D2): the workforce OIDC provider
 * and the SCIM credential, held in one row every server reads within 20 seconds of a
 * save. Every write answers the whole row with its new version, and the card keeps
 * that answer, so the next save is checked against the version it really holds.
 */
export const SsoSettingsCard = () => {
  const { t } = useI18n("host");
  const settings = useQuery({ queryKey: ssoSettingsQueryKey, queryFn: fetchSsoSettings });

  return (
    <Card withBorder radius="md" padding="lg">
      <Group gap="sm" mb="sm">
        <ThemeIcon variant="light" size="lg">
          <IconKey size={20} />
        </ThemeIcon>
        <Title order={3} fz="h4">
          {t("admin.sso.title")}
        </Title>
      </Group>
      <Text size="sm" c="dimmed" mb="lg">
        {t("admin.sso.description")}
      </Text>
      {settings.isPending && (
        <Stack>
          <Skeleton height={16} />
          <Skeleton height={16} width="75%" />
        </Stack>
      )}
      {settings.isError && (
        <Alert color="red" title={t("admin.sso.loadFailed")}>
          <Button size="compact-sm" variant="light" onClick={() => void settings.refetch()}>
            {t("common.tryAgain")}
          </Button>
        </Alert>
      )}
      {settings.data && <SsoSettingsForms settings={settings.data} reload={() => void settings.refetch()} />}
    </Card>
  );
};

const SsoSettingsForms = ({ settings, reload }: { settings: SsoSettings; reload: () => void }) => {
  const { t } = useI18n("host");
  const [stale, setStale] = useState(false);
  // A new draft for every version reloaded after a 412, so the form starts from the row it is saved against.
  const [generation, setGeneration] = useState(0);
  const onStale = () => setStale(true);
  const reloadAll = () => {
    setStale(false);
    setGeneration((value) => value + 1);
    reload();
  };

  return (
    <Stack>
      {settings.importedFromEnvironment && (
        <Alert color="yellow" title={t("admin.sso.importedTitle")}>
          {t("admin.sso.importedBody")}
        </Alert>
      )}
      {stale && (
        <Alert color="orange" title={t("admin.sso.staleTitle")}>
          <Stack gap="xs" align="flex-start">
            <Text size="sm">{t("admin.sso.staleBody")}</Text>
            <Button size="compact-sm" variant="light" onClick={reloadAll}>
              {t("admin.sso.reload")}
            </Button>
          </Stack>
        </Alert>
      )}
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="xl">
        <OidcSection key={generation} settings={settings} onStale={onStale} />
        <ScimSection settings={settings} />
      </SimpleGrid>
    </Stack>
  );
};

const OidcSection = ({ settings, onStale }: { settings: SsoSettings; onStale: () => void }) => {
  const { t, formatters } = useI18n("host");
  const queryClient = useQueryClient();
  const date = (value: string) => formatters.formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" });
  const oidc = settings.oidc;
  const saved = draftFrom(oidc);
  const [draft, setDraft] = useState<OidcDraft>();
  const form = draft ?? saved;
  const [errors, setErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string>();
  const changed = !sameDraft(form, saved);

  const keep = (answer: SsoSettings) => {
    queryClient.setQueryData(ssoSettingsQueryKey, answer);
    setDraft(undefined);
    setErrors({});
    setFormError(undefined);
  };
  const update = (patch: Partial<OidcDraft>) => {
    setDraft({ ...form, ...patch });
    setErrors((current) => {
      const next = { ...current };
      for (const key of Object.keys(patch)) delete next[key as OidcField];
      if ("credential" in patch || "clientSecret" in patch || "workloadTokenFile" in patch) {
        delete next.clientSecret;
        delete next.workloadTokenFile;
      }
      return next;
    });
  };

  const refuse = (error: unknown) => {
    if (isStale(error)) {
      onStale();
      return;
    }
    const fields = (error as ApiError).fields;
    const known: FieldErrors = {};
    for (const field of oidcFields) {
      const message = fields?.[field]?.[0];
      if (message) known[field] = message;
    }
    setErrors(known);
    setFormError(Object.keys(known).length > 0 ? undefined : messageOf(error, t("common.requestFailed")));
  };

  const save = useMutation({
    mutationFn: ({ provider, confirmIssuerChange }: { provider: SsoProvider; confirmIssuerChange: boolean }) =>
      updateOidcSettings(requestFrom(form, provider, confirmIssuerChange), settings.version),
    onSuccess: (answer) => {
      keep(answer);
      notifications.show({ title: t("admin.sso.saved"), message: t("admin.sso.savedBody"), color: "teal" });
    },
    onError: (error, variables) => {
      if (linksExist(error) && !variables.confirmIssuerChange) {
        modals.openConfirmModal({
          title: t("admin.sso.issuerChangeTitle"),
          children: (
            <Text size="sm">
              {t("admin.sso.issuerChangeConfirm", { from: oidc.authority ?? "", to: form.authority.trim() })}
            </Text>
          ),
          labels: { confirm: t("admin.sso.issuerChangeButton"), cancel: t("common.cancel") },
          confirmProps: { color: "red" },
          onConfirm: () => save.mutate({ provider: variables.provider, confirmIssuerChange: true }),
        });
        return;
      }
      refuse(error);
    },
  });

  const remove = useMutation({
    mutationFn: (confirm: boolean) => removeOidc(confirm),
    onSuccess: (answer) => {
      keep(answer);
      notifications.show({ title: t("admin.sso.oidcTurnedOff"), message: t("admin.sso.oidcTurnedOffBody") });
    },
    onError: (error, confirm) => {
      if (linksExist(error) && !confirm) {
        modals.openConfirmModal({
          title: t("admin.sso.oidcLinkedTitle"),
          children: <Text size="sm">{t("admin.sso.oidcLinkedConfirm")}</Text>,
          labels: { confirm: t("admin.sso.turnOff"), cancel: t("common.cancel") },
          confirmProps: { color: "red" },
          onConfirm: () => remove.mutate(true),
        });
        return;
      }
      if (isStale(error)) {
        onStale();
        return;
      }
      notifications.show({
        color: "red",
        title: t("admin.sso.oidcTurnOffFailed"),
        message: messageOf(error, t("common.requestFailed")),
      });
    },
  });

  const turnOff = () =>
    modals.openConfirmModal({
      title: t("admin.sso.oidcTurnOffTitle"),
      children: <Text size="sm">{t("admin.sso.oidcTurnOffConfirm")}</Text>,
      labels: { confirm: t("admin.sso.turnOff"), cancel: t("common.cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(false),
    });

  const submit = () => {
    if (!changed) return;
    if (form.provider === "off") {
      turnOff();
      return;
    }
    save.mutate({ provider: form.provider, confirmIssuerChange: false });
  };

  const chooseProvider = (value: string | null) => {
    const provider = (value ?? "off") as Provider;
    let authority = form.authority;
    if (provider === saved.provider) authority = saved.authority;
    else if (provider === "google") authority = googleAuthority;
    else if (authority === googleAuthority) authority = "";
    update({ provider, authority, credential: provider === "google" ? "secret" : form.credential });
  };

  const provider = form.provider;
  const usesFile = provider === "entra" && form.credential === "file";
  const credentialError = errors.clientSecret ?? errors.workloadTokenFile;
  // The stored secret is kept only while the provider and client id stay what they are.
  const secretKept =
    oidc.clientSecretSetAt !== undefined && provider === saved.provider && form.clientId.trim() === saved.clientId;

  return (
    <Stack>
      <Group justify="space-between">
        <Title order={4} fz="h5">
          {t("admin.sso.oidc")}
        </Title>
        <Badge color={oidc.enabled ? "teal" : "gray"}>{oidc.enabled ? t("common.on") : t("common.off")}</Badge>
      </Group>
      <Select
        label={t("admin.sso.provider")}
        allowDeselect={false}
        data={[
          { value: "off", label: t("admin.sso.providerOff") },
          { value: "entra", label: t("admin.sso.providerEntra") },
          { value: "google", label: t("admin.sso.providerGoogle") },
        ]}
        value={provider}
        error={errors.provider}
        onChange={chooseProvider}
      />
      {provider !== "off" && (
        <>
          <TextInput
            label={t("admin.sso.authority")}
            description={provider === "entra" ? t("admin.sso.authorityEntra") : t("admin.sso.authorityGoogle")}
            value={form.authority}
            error={errors.authority}
            onChange={(event) => update({ authority: event.currentTarget.value })}
          />
          <TextInput
            label={t("admin.sso.clientId")}
            value={form.clientId}
            error={errors.clientId}
            onChange={(event) => update({ clientId: event.currentTarget.value })}
          />
          {provider === "entra" && (
            <Stack gap={4}>
              <Text size="sm" fw={500}>
                {t("admin.sso.credential")}
              </Text>
              <SegmentedControl
                value={form.credential}
                onChange={(value) => update({ credential: value as Credential })}
                data={[
                  { value: "secret", label: t("admin.sso.credentialSecret") },
                  { value: "file", label: t("admin.sso.credentialFile") },
                ]}
              />
            </Stack>
          )}
          {usesFile ? (
            <TextInput
              label={t("admin.sso.workloadTokenFile")}
              description={t("admin.sso.workloadTokenFileDescription")}
              value={form.workloadTokenFile}
              error={credentialError}
              onChange={(event) => update({ workloadTokenFile: event.currentTarget.value })}
            />
          ) : (
            <PasswordInput
              label={t("admin.sso.clientSecret")}
              description={
                secretKept && oidc.clientSecretSetAt
                  ? t("admin.sso.clientSecretKept", { date: date(oidc.clientSecretSetAt) })
                  : t("admin.sso.clientSecretRequired")
              }
              autoComplete="new-password"
              value={form.clientSecret}
              error={credentialError}
              onChange={(event) => update({ clientSecret: event.currentTarget.value })}
            />
          )}
          {provider === "google" && (
            <Textarea
              label={t("admin.sso.allowedDomains")}
              description={t("admin.sso.allowedDomainsDescription")}
              autosize
              minRows={2}
              value={form.allowedDomains}
              error={errors.allowedDomains}
              onChange={(event) => update({ allowedDomains: event.currentTarget.value })}
            />
          )}
          <TextInput
            label={t("admin.sso.displayName")}
            description={t("admin.sso.displayNameDescription")}
            placeholder={t("admin.sso.displayNameDefault")}
            value={form.displayName}
            error={errors.displayName}
            onChange={(event) => update({ displayName: event.currentTarget.value })}
          />
        </>
      )}
      <CopyField
        label={t("admin.sso.callbackUrl")}
        description={t("admin.sso.callbackUrlDescription")}
        value={oidc.callbackUrl}
      />
      {formError && (
        <Alert color="red" title={t("admin.sso.saveFailed")}>
          {formError}
        </Alert>
      )}
      <Group>
        <Button loading={save.isPending} disabled={!changed} onClick={submit}>
          {t("admin.sso.save")}
        </Button>
        {oidc.enabled && (
          <Button variant="light" color="red" loading={remove.isPending} onClick={turnOff}>
            {t("admin.sso.turnOff")}
          </Button>
        )}
      </Group>
      <Group justify="space-between">
        <Text size="sm">{t("admin.sso.lastSignIn")}</Text>
        <Text size="sm" c="dimmed">
          {settings.lastOidcSignInAt ? date(settings.lastOidcSignInAt) : t("admin.sso.noSignIn")}
        </Text>
      </Group>
    </Stack>
  );
};

const ScimSection = ({ settings }: { settings: SsoSettings }) => {
  const { t, formatters } = useI18n("host");
  const queryClient = useQueryClient();
  const date = (value: string) => formatters.formatDate(new Date(value), { dateStyle: "medium", timeStyle: "short" });
  const scim = settings.scim;
  const [rotating, setRotating] = useState(false);
  // When the rotation was asked for: the overlap's end is shown from it, since a render must not read the clock.
  const [rotationAskedAt, setRotationAskedAt] = useState(0);
  const [overlapHours, setOverlapHours] = useState<number>(maxOverlapHours);
  const [overlapError, setOverlapError] = useState<string>();
  // The token lives in this state only while its modal is open: it is never shown again.
  const [issued, setIssued] = useState<SsoScimToken>();

  const keep = (answer: SsoSettings) => queryClient.setQueryData(ssoSettingsQueryKey, answer);
  const failed = (title: string) => (error: unknown) =>
    notifications.show({ color: "red", title, message: messageOf(error, t("common.requestFailed")) });

  const issue = useMutation({
    mutationFn: (previousExpiresAt: string | undefined) =>
      issueScimToken(previousExpiresAt ? { previousExpiresAt } : {}),
    onSuccess: (answer) => {
      keep(answer.settings);
      setRotating(false);
      setOverlapError(undefined);
      setIssued(answer);
    },
    onError: (error) => {
      const onField = (error as ApiError).fields?.previousExpiresAt?.[0];
      if (rotating) setOverlapError(onField ?? messageOf(error, t("common.requestFailed")));
      else failed(t("admin.sso.tokenFailed"))(error);
    },
  });
  const endOverlap = useMutation({
    mutationFn: endScimOverlap,
    onSuccess: (answer) => {
      keep(answer);
      notifications.show({ title: t("admin.sso.overlapEnded"), message: t("admin.sso.overlapEndedBody") });
    },
    onError: failed(t("admin.sso.overlapEndFailed")),
  });
  const remove = useMutation({
    mutationFn: removeScim,
    onSuccess: (answer) => {
      keep(answer);
      notifications.show({ title: t("admin.sso.scimTurnedOff"), message: t("admin.sso.scimTurnedOffBody") });
    },
    onError: failed(t("admin.sso.scimTurnOffFailed")),
  });

  const newToken = () => {
    if (!scim.tokenSetAt) {
      issue.mutate(undefined);
      return;
    }
    setOverlapHours(maxOverlapHours);
    setOverlapError(undefined);
    setRotationAskedAt(Date.now());
    setRotating(true);
  };
  // The full 24 hours is the server's own default, sent as nothing so a clock ahead of the server's is never refused.
  const overlapEnd = (hours: number) =>
    hours >= maxOverlapHours ? undefined : new Date(Date.now() + hours * 3_600_000).toISOString();

  return (
    <Stack>
      <Group justify="space-between">
        <Title order={4} fz="h5">
          {t("admin.sso.scim")}
        </Title>
        <Badge color={scim.enabled ? "teal" : "gray"}>{scim.enabled ? t("common.on") : t("common.off")}</Badge>
      </Group>
      <Text size="sm" c="dimmed">
        {t("admin.sso.scimDescription")}
      </Text>
      <CopyField label={t("admin.sso.endpointUrl")} value={scim.endpointUrl} />
      <Group justify="space-between">
        <Text size="sm">{t("admin.sso.tokenSetAt")}</Text>
        <Text size="sm" c="dimmed">
          {scim.tokenSetAt ? date(scim.tokenSetAt) : t("admin.sso.noToken")}
        </Text>
      </Group>
      {scim.previousExpiresAt && (
        <Alert color="blue">{t("admin.sso.previousAccepted", { date: date(scim.previousExpiresAt) })}</Alert>
      )}
      <Group>
        <Button loading={issue.isPending && !rotating} onClick={newToken}>
          {t("admin.sso.newToken")}
        </Button>
        {scim.previousExpiresAt && (
          <Button
            variant="light"
            loading={endOverlap.isPending}
            onClick={() =>
              modals.openConfirmModal({
                title: t("admin.sso.endOverlapTitle"),
                children: <Text size="sm">{t("admin.sso.endOverlapConfirm")}</Text>,
                labels: { confirm: t("admin.sso.endOverlap"), cancel: t("common.cancel") },
                onConfirm: () => endOverlap.mutate(),
              })
            }
          >
            {t("admin.sso.endOverlap")}
          </Button>
        )}
        {scim.enabled && (
          <Button
            variant="light"
            color="red"
            loading={remove.isPending}
            onClick={() =>
              modals.openConfirmModal({
                title: t("admin.sso.scimTurnOffTitle"),
                children: <Text size="sm">{t("admin.sso.scimTurnOffConfirm")}</Text>,
                labels: { confirm: t("admin.sso.turnOff"), cancel: t("common.cancel") },
                confirmProps: { color: "red" },
                onConfirm: () => remove.mutate(),
              })
            }
          >
            {t("admin.sso.turnOff")}
          </Button>
        )}
      </Group>
      <Group justify="space-between">
        <Text size="sm">{t("admin.sso.lastRequest")}</Text>
        <Text size="sm" c="dimmed">
          {settings.lastScimRequestAt ? date(settings.lastScimRequestAt) : t("admin.sso.noRequest")}
        </Text>
      </Group>

      <Modal opened={rotating} onClose={() => setRotating(false)} title={t("admin.sso.rotateTitle")} centered>
        <Stack>
          <Text size="sm">{t("admin.sso.rotateBody")}</Text>
          <NumberInput
            label={t("admin.sso.overlapHours")}
            description={t("admin.sso.overlapUntil", {
              date: date(new Date(rotationAskedAt + overlapHours * 3_600_000).toISOString()),
            })}
            min={1}
            max={maxOverlapHours}
            clampBehavior="strict"
            allowDecimal={false}
            value={overlapHours}
            error={overlapError}
            onChange={(value) => {
              setOverlapError(undefined);
              setOverlapHours(typeof value === "number" ? value : maxOverlapHours);
            }}
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setRotating(false)}>
              {t("common.cancel")}
            </Button>
            <Button loading={issue.isPending} onClick={() => issue.mutate(overlapEnd(overlapHours))}>
              {t("admin.sso.issueToken")}
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal
        opened={issued !== undefined}
        onClose={() => setIssued(undefined)}
        title={t("admin.sso.tokenTitle")}
        closeOnClickOutside={false}
        centered
      >
        {issued && (
          <Stack>
            <Text size="sm" fw={500}>
              {t("admin.sso.tokenOnce")}
            </Text>
            <Code block>{issued.token}</Code>
            <CopyButton value={issued.token}>
              {({ copied, copy }) => (
                <Button
                  variant="light"
                  w="fit-content"
                  leftSection={copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                  onClick={copy}
                >
                  {copied ? t("admin.sso.copied") : t("admin.sso.copyToken")}
                </Button>
              )}
            </CopyButton>
            <CopyField label={t("admin.sso.endpointUrl")} value={issued.endpointUrl} />
            {issued.previousExpiresAt && (
              <Text size="sm">{t("admin.sso.previousAccepted", { date: date(issued.previousExpiresAt) })}</Text>
            )}
            <Divider />
            <Group justify="flex-end">
              <Button onClick={() => setIssued(undefined)}>{t("admin.sso.done")}</Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </Stack>
  );
};
