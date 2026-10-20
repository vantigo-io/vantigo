import { Alert, Anchor, Button, Group, List, SimpleGrid, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconPlugConnected } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { appUrl, ContentSkeleton, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  connectionQueryOptions,
  type EntraConnection,
  type EntraConnectionTest,
  requiredPermissions,
  testConnection,
  updateConnection,
} from "../api/connection";
import { ApiConflictError, ENTRA_QUERY_KEY } from "../api/request";
import { callerIsOwnerQueryOptions } from "../api/session";
import { AddSecretForm, CredentialList } from "../components/credentials";
import { GroupsCard } from "../components/groups-card";
import { SettingsForm } from "../components/settings-form";
import { StatusCard } from "../components/status-card";
import { CopyValue, OwnerOnly, PortalPath, StepCard } from "../components/step-card";
import "../i18n";
import { entraAuthority, stepsDone } from "../lib/steps";
import { codeOf, fieldRefusals, isStale, reasonText, type Translate } from "../lib/words";

/** What step 5 hands the host's sign-in form: the tenant's authority and the client, for an empty form only. */
export interface SignInDefaults {
  provider: "entra";
  authority: string;
  clientId: string;
}

export interface EntraPageProps {
  /** Step 5: identity's OIDC form, which the host embeds pre-filled (Entra design D15). */
  signIn: (defaults: SignInDefaults) => ReactNode;
  /** Step 6: identity's SCIM token card, which the host embeds. */
  provisioning: () => ReactNode;
}

/**
 * The module's one page (Entra design D15): the **Status** card once the
 * connection is configured, then the wizard's six steps (research §7.5),
 * each with the portal path, what to copy and its *Done* mark. Steps 1 to 3
 * — and identity's forms in 5 and 6 — are the Owner's: an `entra:manage`
 * holder who is not one reads them without their forms.
 */
export function EntraPage({ signIn, provisioning }: EntraPageProps) {
  const { t } = useI18n("entra");
  const connection = useQuery(connectionQueryOptions());
  const owner = useQuery(callerIsOwnerQueryOptions());

  return (
    <Stack gap="lg">
      <PageHeader title={t("entra")} description={t("entraDescription")} />
      {(connection.isPending || owner.isPending) && <ContentSkeleton rows={4} rowHeight={80} />}
      {connection.isError && (
        <Alert color="red" title={t("loadFailed")}>
          <Button size="compact-sm" variant="light" onClick={() => void connection.refetch()}>
            {t("tryAgain")}
          </Button>
        </Alert>
      )}
      {connection.data && !owner.isPending && (
        <Wizard connection={connection.data} owner={owner.data === true} signIn={signIn} provisioning={provisioning} />
      )}
    </Stack>
  );
}

const Wizard = ({
  connection,
  owner,
  signIn,
  provisioning,
}: { connection: EntraConnection; owner: boolean } & EntraPageProps) => {
  const { t } = useI18n("entra");
  const done = stepsDone(connection);
  const { tenantId, clientId } = connection;

  return (
    <Stack gap="lg">
      {connection.status !== "unconfigured" && <StatusCard connection={connection} owner={owner} />}
      {connection.status !== "unconfigured" && <GroupsCard />}

      <StepCard step={1} title={t("step1Title")} path={t("step1Path")} done={done.register}>
        <Text size="sm">{t("step1Body")}</Text>
        {owner ? <RegisterForm connection={connection} /> : <RegisterRead connection={connection} />}
      </StepCard>

      <StepCard step={2} title={t("step2Title")} path={t("step2Path")} done={done.secret}>
        <Text size="sm">{t("step2Body")}</Text>
        <CredentialList credentials={connection.credentials} owner={owner} />
        {owner ? <AddSecretForm /> : <OwnerOnly />}
      </StepCard>

      <StepCard step={3} title={t("step3Title")} path={t("step3Path")} done={done.permissions}>
        <PermissionsStep connection={connection} owner={owner} />
      </StepCard>

      <StepCard step={4} title={t("step4Title")} path={t("step4Path")} done={done.accounts}>
        <Text size="sm">{t("step4Body")}</Text>
        <SettingsForm connection={connection} />
      </StepCard>

      <StepCard step={5} title={t("step5Title")} path={t("step5Path")} done={undefined}>
        <Text size="sm">{t("step5Body")}</Text>
        <Text size="sm" c="dimmed">
          {t("step5Without")}
        </Text>
        {!owner ? (
          <OwnerOnly />
        ) : tenantId && clientId ? (
          signIn({ provider: "entra", authority: entraAuthority(tenantId), clientId })
        ) : (
          <Text size="sm" c="orange">
            {t("step5NeedsRegister")}
          </Text>
        )}
      </StepCard>

      <StepCard step={6} title={t("step6Title")} path={t("step6Path")} done={undefined}>
        <Text size="sm">{t("step6Body")}</Text>
        <Text size="sm">{t("step6Extension")}</Text>
        <Text size="sm" c="dimmed">
          {t("step6Without")}
        </Text>
        {owner ? provisioning() : <OwnerOnly />}
        <Stack gap={4}>
          <Text size="sm">{t("defaultKind")}</Text>
          <Anchor size="sm" href={appUrl("/staff/settings")}>
            {t("openStaffSettings")}
          </Anchor>
        </Stack>
      </StepCard>
    </Stack>
  );
};

const guidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

interface RegisterValues {
  tenantId: string;
  clientId: string;
}

const registerValuesOf = (connection: EntraConnection): RegisterValues => ({
  tenantId: connection.tenantId ?? "",
  clientId: connection.clientId ?? "",
});

/** Step 1 for the Owner: the tenant and the client, saved under If-Match, confirmed when the module keeps accounts. */
const RegisterForm = ({ connection }: { connection: EntraConnection }) => {
  const { t } = useI18n("entra");
  const queryClient = useQueryClient();
  const guid = (value: string) => (guidPattern.test(value.trim()) ? null : t("guidInvalid"));
  const form = useForm<RegisterValues>({
    initialValues: registerValuesOf(connection),
    validate: { tenantId: guid, clientId: guid },
  });

  const loaded = JSON.stringify(registerValuesOf(connection));
  const [seeded, setSeeded] = useState(loaded);
  if (seeded !== loaded) {
    setSeeded(loaded);
    if (!form.isDirty()) {
      form.setInitialValues(registerValuesOf(connection));
      form.setValues(registerValuesOf(connection));
    }
  }

  const save = useMutation({
    mutationFn: ({ values, confirmChange }: { values: RegisterValues; confirmChange: boolean }) =>
      updateConnection(
        {
          tenantId: values.tenantId.trim().toLowerCase(),
          clientId: values.clientId.trim().toLowerCase(),
          ...(confirmChange ? { confirmChange: true } : {}),
        },
        connection.version,
      ),
    onSuccess: async (answer) => {
      queryClient.setQueryData([ENTRA_QUERY_KEY, "connection"], answer);
      form.resetDirty();
      notifications.show({ color: "teal", title: t("registerSaved"), message: t("registerSavedBody") });
      await queryClient.invalidateQueries({ queryKey: [ENTRA_QUERY_KEY] });
    },
    onError: async (error, { values, confirmChange }) => {
      if (error instanceof ApiConflictError && codeOf(error) === "connection_in_use" && !confirmChange) {
        modals.openConfirmModal({
          title: t("connectionInUseTitle"),
          children: <Text size="sm">{t("connectionInUseConfirm", { count: connection.accounts.count })}</Text>,
          labels: { confirm: t("connectionInUseButton"), cancel: t("cancel") },
          confirmProps: { color: "red" },
          onConfirm: () => save.mutate({ values, confirmChange: true }),
        });
        return;
      }
      if (isStale(error)) {
        form.resetDirty();
        notifications.show({ color: "yellow", title: t("versionMismatch"), message: t("versionMismatchHint") });
        await queryClient.invalidateQueries({ queryKey: [ENTRA_QUERY_KEY] });
        return;
      }
      const { fields, rest } = fieldRefusals(error, ["tenantId", "clientId"], t);
      form.setErrors(fields);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSaveRegister"), message: rest.join(" ") });
    },
  });

  return (
    <form
      onSubmit={form.onSubmit((values) => save.mutate({ values, confirmChange: false }))}
      aria-label={t("step1Title")}
    >
      <Stack gap="sm">
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t("tenantId")}
            description={t("tenantIdDescription")}
            withAsterisk
            {...form.getInputProps("tenantId")}
          />
          <TextInput
            label={t("clientId")}
            description={t("clientIdDescription")}
            withAsterisk
            {...form.getInputProps("clientId")}
          />
        </SimpleGrid>
        {connection.accounts.count > 0 && (
          <Text size="sm" c="orange">
            {t("registerChangeWarning", { count: connection.accounts.count })}
          </Text>
        )}
        <Group justify="flex-end">
          <Button type="submit" loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/** Step 1 for anyone else: what is set, and that the Owner sets it. */
const RegisterRead = ({ connection }: { connection: EntraConnection }) => {
  const { t } = useI18n("entra");
  return (
    <Stack gap="xs">
      <Text size="sm">{t("tenantIdValue", { value: connection.tenantId ?? t("notSet") })}</Text>
      <Text size="sm">{t("clientIdValue", { value: connection.clientId ?? t("notSet") })}</Text>
      <OwnerOnly />
    </Stack>
  );
};

/** Step 3: the eight permissions to copy, the User Administrator role, and the Owner's *Test connection*. */
const PermissionsStep = ({ connection, owner }: { connection: EntraConnection; owner: boolean }) => {
  const { t } = useI18n("entra");
  const queryClient = useQueryClient();
  const [result, setResult] = useState<EntraConnectionTest>();
  const present = new Set(connection.lastCheckedAt ? connection.permissionsPresent : []);

  const test = useMutation({
    mutationFn: testConnection,
    onSuccess: async (answer) => {
      setResult(answer);
      await queryClient.invalidateQueries({ queryKey: [ENTRA_QUERY_KEY] });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotTest"), message: reasonOrMessage(error, t) }),
  });

  return (
    <>
      <Text size="sm">{t("step3Body")}</Text>
      <Group gap="xs">
        <Text size="sm" fw={500}>
          {t("permissionsToAdd")}
        </Text>
        <CopyValue value={requiredPermissions.join("\n")} />
      </Group>
      <List size="sm" spacing={2} aria-label={t("permissionsToAdd")}>
        {requiredPermissions.map((name) => (
          <List.Item key={name}>
            <Group gap={4} wrap="nowrap">
              <Text size="sm" ff="monospace">
                {name}
              </Text>
              <CopyValue value={name} />
              {connection.lastCheckedAt && (
                <Text size="xs" c={present.has(name) ? "teal" : "red"}>
                  {present.has(name) ? t("permissionGranted") : t("permissionMissing")}
                </Text>
              )}
            </Group>
          </List.Item>
        ))}
      </List>
      <Text size="sm">{t("adminConsent")}</Text>
      <Alert color="yellow" variant="light" title={t("roleTitle")}>
        <Stack gap={4}>
          <Text size="sm">{t("roleBody")}</Text>
          <PortalPath path={t("rolePath")} />
        </Stack>
      </Alert>
      {owner ? (
        <Group>
          <Button
            leftSection={<IconPlugConnected size={16} />}
            loading={test.isPending}
            onClick={() => {
              setResult(undefined);
              test.mutate();
            }}
          >
            {t("testConnection")}
          </Button>
        </Group>
      ) : (
        <OwnerOnly />
      )}
      {result && <TestResult result={result} />}
    </>
  );
};

/** A test's failure that is not a 200: the code's words when it has one, else the message. */
const reasonOrMessage = (error: unknown, t: Translate) => {
  const code = codeOf(error);
  if (code) return reasonText(code, t);
  return error instanceof Error ? error.message : String(error);
};

/** What *Test connection* found, in words: the organisation, the domains, each missing permission, why it failed. */
const TestResult = ({ result }: { result: EntraConnectionTest }) => {
  const { t } = useI18n("entra");
  const connected = result.status === "connected";
  return (
    <Alert
      role="status"
      color={connected ? (result.permissionsMissing.length > 0 ? "yellow" : "teal") : "red"}
      title={
        connected
          ? t("testConnected", { organisation: result.organisationName ?? t("organisationUnnamed") })
          : t("testFailed")
      }
    >
      <Stack gap={4}>
        {!connected && result.reason && <Text size="sm">{reasonText(result.reason, t)}</Text>}
        {connected && (
          <Text size="sm">
            {result.verifiedDomains.length > 0
              ? t("testDomains", { domains: result.verifiedDomains.join(", ") })
              : t("testNoDomains")}
          </Text>
        )}
        {connected && result.permissionsMissing.length === 0 && <Text size="sm">{t("permissionsAllPresent")}</Text>}
        {result.permissionsMissing.length > 0 && (
          <>
            <Text size="sm">{t("permissionsMissingList", { count: result.permissionsMissing.length })}</Text>
            <List size="sm" aria-label={t("permissionsMissing")}>
              {result.permissionsMissing.map((name) => (
                <List.Item key={name}>{name}</List.Item>
              ))}
            </List>
          </>
        )}
        {!result.roleKnown && <Text size="sm">{t("roleUnknown")}</Text>}
      </Stack>
    </Alert>
  );
};
