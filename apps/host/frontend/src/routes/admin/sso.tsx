import {
  Alert,
  Badge,
  Button,
  Card,
  CopyButton,
  Divider,
  Group,
  Loader,
  Modal,
  Select,
  Stack,
  TagsInput,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconCopy,
  IconKey,
  IconPlugConnected,
  IconPlus,
  IconRefresh,
  IconTrash,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, redirect } from "@tanstack/react-router";
import { appUrl } from "@vantigo/frontend-shell";
import { useState } from "react";
import { fetchSession } from "../../api/auth";
import {
  createFederationConnection,
  deleteFederationConnection,
  type FederationConnection,
  type FederationConnectionInput,
  listFederationConnections,
  setFederationConnectionEnabled,
  updateFederationConnection,
  validateFederationConnection,
} from "../../api/federation";
import {
  createScimConnection,
  listScimConnections,
  revokeScimToken,
  rotateScimToken,
  type ScimConnection,
  type ScimProvisioningMode,
  setScimEnabled,
} from "../../api/scim";

const errorText = (error: unknown) => (error instanceof Error ? error.message : "The request could not be completed.");
const status = (connection: FederationConnection) => {
  if (connection.validationState === "Failed" || connection.validationErrorCode)
    return { label: "Failed", color: "red" };
  if (connection.isEnabled) return { label: connection.isDefault ? "Default" : "Enabled", color: "teal" };
  if (connection.validationState === "Succeeded") return { label: "Validated", color: "blue" };
  return { label: "Draft", color: "gray" };
};

const SsoPage = () => {
  const qc = useQueryClient();
  const connections = useQuery({ queryKey: ["federation-connections"], queryFn: listFederationConnections });
  const scim = useQuery({ queryKey: ["scim-connections"], queryFn: listScimConnections });
  const [editing, setEditing] = useState<FederationConnection | null | false>(false);
  const [scimModal, setScimModal] = useState(false);
  const [scimFederationId, setScimFederationId] = useState<string | null>(null);
  const [scimMode, setScimMode] = useState<ScimProvisioningMode>("Authoritative");
  const [revealedToken, setRevealedToken] = useState<string | null>(null);
  const [validationMessage, setValidationMessage] = useState<string | null>(null);
  const callbackUrl =
    typeof window === "undefined"
      ? appUrl("/api/v1/identity/federation/callback")
      : `${window.location.origin}${appUrl("/api/v1/identity/federation/callback")}`;
  const scimBaseUrl =
    typeof window === "undefined"
      ? appUrl("/api/v1/identity/scim/v2")
      : `${window.location.origin}${appUrl("/api/v1/identity/scim/v2")}`;
  const form = useForm<FederationConnectionInput>({
    initialValues: {
      providerType: "Generic",
      displayName: "",
      authority: "",
      clientId: "",
      clientSecretReference: "",
      allowedDomains: [],
      isEnabled: false,
      isDefault: false,
      jitCreationMode: "Disabled",
    },
    validate: {
      displayName: (v) => (v.trim() ? null : "Enter a display name"),
      authority: (v) => (/^https:\/\//.test(v) ? null : "Use an HTTPS issuer URL"),
      clientId: (v) => (v.trim() ? null : "Enter the client ID"),
      allowedDomains: (v) => (v.length ? null : "Add at least one email domain"),
      clientSecretReference: (v) =>
        v && !/^VANTIGO_SSO_[A-Z0-9]+(?:_[A-Z0-9]+)*_CLIENT_SECRET$/.test(v)
          ? "Use a VANTIGO_SSO_*_CLIENT_SECRET environment-variable name"
          : null,
    },
  });
  const close = () => {
    setEditing(false);
    form.reset();
  };
  const open = (connection: FederationConnection | null) => {
    setEditing(connection);
    form.setValues(
      connection
        ? {
            providerType: connection.providerType,
            displayName: connection.displayName,
            authority: connection.authority,
            clientId: connection.clientId,
            clientSecretReference: connection.clientSecretReference ?? "",
            allowedDomains: connection.allowedDomains,
            isEnabled: connection.isEnabled,
            isDefault: false,
            jitCreationMode: connection.jitCreationMode,
            concurrencyStamp: connection.concurrencyStamp,
          }
        : {
            providerType: "Generic",
            displayName: "",
            authority: "",
            clientId: "",
            clientSecretReference: "",
            allowedDomains: [],
            isEnabled: false,
            isDefault: false,
            jitCreationMode: "Disabled",
          },
    );
  };
  const save = useMutation({
    mutationFn: async (values: FederationConnectionInput) =>
      editing && typeof editing !== "boolean"
        ? updateFederationConnection(editing.id, values)
        : createFederationConnection(values),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["federation-connections"] });
      close();
      notifications.show({
        title: "SSO connection saved",
        message: "Validate it before enabling sign-in.",
        color: "teal",
      });
    },
    onError: (e) => notifications.show({ title: "Could not save connection", message: errorText(e), color: "red" }),
  });
  const action = useMutation({
    mutationFn: async ({
      kind,
      connection,
    }: {
      kind: "validate" | "enable" | "disable" | "delete";
      connection: FederationConnection;
    }) => {
      if (kind === "validate") return validateFederationConnection(connection.id, connection.concurrencyStamp);
      if (kind === "delete") return deleteFederationConnection(connection.id, connection.concurrencyStamp);
      return setFederationConnectionEnabled(connection.id, connection.concurrencyStamp, kind === "enable");
    },
    onSuccess: (result) => {
      if (result && "message" in result) setValidationMessage(result.message);
      void qc.invalidateQueries({ queryKey: ["federation-connections"] });
    },
    onError: (e) => notifications.show({ title: "Action failed", message: errorText(e), color: "red" }),
  });
  const scimAction = useMutation({
    mutationFn: async ({
      kind,
      connection,
    }: {
      kind: "enable" | "disable" | "rotate" | "revoke";
      connection: ScimConnection;
    }) => {
      if (kind === "rotate") return rotateScimToken(connection.id, connection.concurrencyStamp);
      if (kind === "revoke") return revokeScimToken(connection.id, connection.concurrencyStamp);
      return setScimEnabled(connection.id, connection.concurrencyStamp, kind === "enable");
    },
    onSuccess: (result) => {
      if (result && "token" in result) setRevealedToken(result.token);
      void qc.invalidateQueries({ queryKey: ["scim-connections"] });
    },
    onError: (e) => notifications.show({ title: "SCIM action failed", message: errorText(e), color: "red" }),
  });
  const createScim = useMutation({
    mutationFn: () => {
      if (!scimFederationId) throw new Error("Choose a federation connection.");
      return createScimConnection(scimFederationId, scimMode);
    },
    onSuccess: (result) => {
      setScimModal(false);
      setRevealedToken(result.token);
      void qc.invalidateQueries({ queryKey: ["scim-connections"] });
    },
    onError: (e) =>
      notifications.show({ title: "Could not create SCIM connection", message: errorText(e), color: "red" }),
  });
  return (
    <Stack maw={1100} mx="auto" p={{ base: "md", sm: "xl" }}>
      <Group justify="space-between" align="flex-start">
        <div>
          <Title order={2}>Single sign-on</Title>
          <Text c="dimmed" mt={4}>
            Connect your team’s identity provider without storing provider secrets.
          </Text>
        </div>
        <Button leftSection={<IconPlus size={16} />} onClick={() => open(null)}>
          Add connection
        </Button>
      </Group>
      <Alert color="blue" icon={<IconKey size={18} />} title="Secrets stay in your deployment">
        <Text size="sm">
          Enter the environment-variable name that holds the client secret, such as{" "}
          <strong>VANTIGO_SSO_ENTRA_CLIENT_SECRET</strong>. Vantigo never receives or stores the raw secret.
        </Text>
      </Alert>
      <Card withBorder>
        <Stack gap="xs">
          <Text fw={600}>Provider setup</Text>
          <Text size="sm" c="dimmed">
            Use this callback URL in your provider registration.
          </Text>
          <CopyButton value={callbackUrl}>
            {({ copied, copy }) => (
              <Button variant="light" size="compact-sm" onClick={copy}>
                {copied ? "Callback URL copied" : "Copy callback URL"}
              </Button>
            )}
          </CopyButton>
          <Text size="xs" ff="monospace" style={{ overflowWrap: "anywhere" }}>
            {callbackUrl}
          </Text>
        </Stack>
      </Card>
      {connections.isPending && (
        <Group justify="center" py="xl">
          <Loader size="sm" />
        </Group>
      )}
      {connections.isError && (
        <Alert color="red" icon={<IconAlertCircle size={18} />}>
          {errorText(connections.error)}
        </Alert>
      )}
      {!connections.isPending && !connections.isError && connections.data?.length === 0 && (
        <Card withBorder p="xl">
          <Stack align="center">
            <IconKey size={28} />
            <Text fw={600}>No connections yet</Text>
            <Text c="dimmed" size="sm">
              Add an identity provider to offer a SSO button on the sign-in page.
            </Text>
            <Button variant="light" onClick={() => open(null)}>
              Add your first connection
            </Button>
          </Stack>
        </Card>
      )}
      {validationMessage && (
        <Alert color="teal" icon={<IconRefresh size={18} />} title="Validation result">
          {validationMessage}
        </Alert>
      )}
      <Stack>
        {connections.data?.map((connection) => {
          const state = status(connection);
          return (
            <Card key={connection.id} withBorder shadow="xs">
              <Group justify="space-between" align="flex-start">
                <div>
                  <Group gap="xs">
                    <Text fw={650}>{connection.displayName}</Text>
                    <Badge color={state.color} variant="light">
                      {state.label}
                    </Badge>
                    {connection.providerType === "Generic" && <Badge variant="outline">OIDC</Badge>}
                  </Group>
                  <Text size="sm" c="dimmed" mt={5}>
                    {connection.providerType} · {connection.allowedDomains.join(", ")}
                  </Text>
                </div>
                <Group gap="xs">
                  <Button variant="subtle" size="compact-sm" onClick={() => open(connection)}>
                    Edit
                  </Button>
                  <Button
                    variant="subtle"
                    size="compact-sm"
                    onClick={() =>
                      window.confirm(
                        "Disable this SSO provider? Existing users will not be removed, but its sign-in button will stop working.",
                      ) && action.mutate({ kind: connection.isEnabled ? "disable" : "enable", connection })
                    }
                  >
                    {connection.isEnabled ? "Disable" : "Enable"}
                  </Button>
                </Group>
              </Group>
              <Divider my="md" />
              <Group justify="space-between">
                <Text size="sm" c={connection.validationErrorCode ? "red" : "dimmed"}>
                  {connection.validationErrorCode
                    ? `Validation failed: ${connection.validationErrorCode}`
                    : connection.isDefault
                      ? "Default sign-in provider"
                      : "Ready for validation"}
                </Text>
                <Group gap="xs">
                  <Button
                    variant="light"
                    size="compact-sm"
                    leftSection={<IconRefresh size={14} />}
                    loading={action.isPending}
                    onClick={() => action.mutate({ kind: "validate", connection })}
                  >
                    Validate
                  </Button>
                  <Button
                    color="red"
                    variant="subtle"
                    size="compact-sm"
                    aria-label={`Delete ${connection.displayName}`}
                    onClick={() =>
                      window.confirm("Delete this SSO connection? Existing users will not be removed.") &&
                      action.mutate({ kind: "delete", connection })
                    }
                  >
                    <IconTrash size={15} />
                  </Button>
                </Group>
              </Group>
            </Card>
          );
        })}
      </Stack>
      <Card withBorder mt="md">
        <Group justify="space-between" align="flex-start">
          <div>
            <Group gap="xs">
              <IconPlugConnected size={20} />
              <Title order={4}>SCIM provisioning</Title>
            </Group>
            <Text c="dimmed" size="sm" mt={4}>
              Provision and deactivate users from a directory. SCIM uses its own bearer token, not browser cookies.
            </Text>
          </div>
          <Button
            variant="light"
            onClick={() => setScimModal(true)}
            disabled={!connections.data?.some((item) => item.isEnabled)}
          >
            Add SCIM connection
          </Button>
        </Group>
        <Alert color="yellow" mt="md" title="Deployment prerequisite">
          <Text size="sm">
            Configure the token pepper through an environment-variable reference such as{" "}
            <strong>VANTIGO_SCIM_DIRECTORY_TOKEN_PEPPER</strong>. The pepper and raw token never appear here.
          </Text>
        </Alert>
        <Alert color="red" mt="sm" title="High-impact access control">
          <Text size="sm">
            Authoritative provisioning can change user access. Owner overrides are exceptional and should be documented.
          </Text>
        </Alert>
        {scim.isPending && <Loader size="sm" mt="md" />}
        {scim.data?.length === 0 && (
          <Text c="dimmed" size="sm" mt="md">
            No SCIM connections configured.
          </Text>
        )}
        <Stack mt="md">
          {scim.data?.map((item) => {
            const federation = connections.data?.find((entry) => entry.id === item.federationConnectionId);
            return (
              <Card key={item.id} withBorder bg="gray.0">
                <Group justify="space-between">
                  <div>
                    <Text fw={600}>{federation?.displayName ?? "Federation connection"}</Text>
                    <Group gap="xs" mt={4}>
                      <Badge color={item.isEnabled ? "teal" : "gray"}>{item.isEnabled ? "Enabled" : "Disabled"}</Badge>
                      <Badge variant="outline">{item.mode}</Badge>
                      <Text size="xs" c="dimmed">
                        Token v{item.tokenVersion}
                      </Text>
                    </Group>
                  </div>
                  <Group gap="xs">
                    <Button
                      size="compact-sm"
                      variant="subtle"
                      onClick={() =>
                        window.confirm("Disable SCIM provisioning? Directory changes will stop being applied.") &&
                        scimAction.mutate({ kind: item.isEnabled ? "disable" : "enable", connection: item })
                      }
                    >
                      {item.isEnabled ? "Disable" : "Enable"}
                    </Button>
                    <Button
                      size="compact-sm"
                      variant="light"
                      onClick={() =>
                        window.confirm(
                          "Rotate this token? The current token will stop being current and the new token must be installed in your directory.",
                        ) && scimAction.mutate({ kind: "rotate", connection: item })
                      }
                    >
                      Rotate token
                    </Button>
                    <Button
                      size="compact-sm"
                      color="red"
                      variant="subtle"
                      onClick={() =>
                        window.confirm(
                          "Revoke this SCIM token and disable provisioning? Directory calls will stop working.",
                        ) && scimAction.mutate({ kind: "revoke", connection: item })
                      }
                    >
                      Revoke
                    </Button>
                  </Group>
                </Group>
                <Text size="sm" mt="md">
                  SCIM base URL
                </Text>
                <CopyButton value={scimBaseUrl}>
                  {({ copied, copy }) => (
                    <Button variant="light" size="compact-sm" onClick={copy}>
                      {copied ? "Base URL copied" : "Copy SCIM base URL"}
                    </Button>
                  )}
                </CopyButton>
                <Text size="xs" ff="monospace" style={{ overflowWrap: "anywhere" }}>
                  {scimBaseUrl}
                </Text>
              </Card>
            );
          })}
        </Stack>
      </Card>
      <Modal opened={scimModal} onClose={() => setScimModal(false)} title="Add SCIM connection">
        <Stack>
          <Select
            label="Federation connection"
            placeholder="Choose a validated connection"
            data={connections.data
              ?.filter(
                (item) =>
                  item.validationState === "Succeeded" &&
                  !scim.data?.some((scimItem) => scimItem.federationConnectionId === item.id),
              )
              .map((item) => ({ value: item.id, label: item.displayName }))}
            value={scimFederationId}
            onChange={setScimFederationId}
          />
          <Select
            label="Provisioning mode"
            data={[
              { value: "Authoritative", label: "Authoritative — directory controls access" },
              { value: "Additive", label: "Additive — directory adds users" },
            ]}
            value={scimMode}
            onChange={(value) => setScimMode((value as ScimProvisioningMode) || "Authoritative")}
          />
          <Alert color="yellow">
            <Text size="sm">
              The token is shown once after creation. Copy it into your directory now; it cannot be retrieved later.
            </Text>
          </Alert>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setScimModal(false)}>
              Cancel
            </Button>
            <Button loading={createScim.isPending} disabled={!scimFederationId} onClick={() => createScim.mutate()}>
              Create and show token
            </Button>
          </Group>
        </Stack>
      </Modal>
      <Modal opened={revealedToken !== null} onClose={() => setRevealedToken(null)} title="Copy your SCIM token">
        <Stack>
          <Alert color="red" title="Shown once">
            <Text size="sm">
              This token cannot be retrieved again. Store it in your directory’s secure secret store, then close this
              window.
            </Text>
          </Alert>
          <TextInput
            value={revealedToken ?? ""}
            readOnly
            rightSection={
              <CopyButton value={revealedToken ?? ""}>
                {({ copied, copy }) => (
                  <Button variant="subtle" size="compact-sm" onClick={copy}>
                    {copied ? "Copied" : <IconCopy size={16} />}
                  </Button>
                )}
              </CopyButton>
            }
          />
          <Button onClick={() => setRevealedToken(null)}>Done</Button>
        </Stack>
      </Modal>
      <Modal
        opened={editing !== false}
        onClose={close}
        title={editing && typeof editing !== "boolean" ? "Edit SSO connection" : "Add SSO connection"}
        size="lg"
      >
        <form
          onSubmit={form.onSubmit((values) => {
            if (
              editing &&
              typeof editing !== "boolean" &&
              editing.isEnabled &&
              !window.confirm(
                "This provider is live. Saving changes will require revalidation and re-enabling before it can sign users in again.",
              )
            )
              return;
            save.mutate({
              ...values,
              isDefault: false,
              isEnabled: false,
              clientSecretReference: values.clientSecretReference || undefined,
            });
          })}
        >
          <Stack>
            <Select
              label="Provider"
              data={[
                { value: "Entra", label: "Microsoft Entra ID" },
                { value: "Google", label: "Google" },
                { value: "Generic", label: "Generic OIDC" },
              ]}
              {...form.getInputProps("providerType")}
            />
            {form.values.providerType === "Entra" && (
              <Text size="sm" c="dimmed">
                Use your tenant-specific issuer, for example https://login.microsoftonline.com/&lt;tenant-id&gt;/v2.0.
                Avoid the multi-tenant common endpoint.
              </Text>
            )}
            {form.values.providerType === "Google" && (
              <Text size="sm" c="dimmed">
                Use Google’s accounts.google.com issuer and restrict access with your Workspace domain below.
              </Text>
            )}
            {form.values.providerType === "Generic" && (
              <Text size="sm" c="dimmed">
                Use the provider’s HTTPS issuer. Validation fetches its standard OIDC discovery document.
              </Text>
            )}
            <TextInput label="Display name" placeholder="Acme workforce" {...form.getInputProps("displayName")} />
            <TextInput
              label="Issuer / authority"
              placeholder="https://login.example.com"
              description="Use the HTTPS issuer URL from your provider."
              {...form.getInputProps("authority")}
            />
            <TextInput label="Client ID" {...form.getInputProps("clientId")} />
            <TextInput
              label="Client secret reference"
              placeholder="VANTIGO_SSO_ENTRA_CLIENT_SECRET"
              description="An environment-variable name injected into the host. Do not paste the secret itself."
              {...form.getInputProps("clientSecretReference")}
            />
            <MultiDomainInput
              value={form.values.allowedDomains}
              onChange={(value) => form.setFieldValue("allowedDomains", value)}
              error={typeof form.errors.allowedDomains === "string" ? form.errors.allowedDomains : undefined}
            />
            <Select
              label="Just-in-time user creation"
              data={[
                { value: "Disabled", label: "Disabled" },
                { value: "CreateUser", label: "Create users after sign-in" },
              ]}
              {...form.getInputProps("jitCreationMode")}
            />
            <Alert color="yellow" title="Safe rollout">
              <Text size="sm">
                Save and validate first. Enabling changes which sign-in options your team sees; disabling does not
                delete users.
              </Text>
            </Alert>
            <Group justify="flex-end">
              <Button variant="default" onClick={close}>
                Cancel
              </Button>
              <Button type="submit" loading={save.isPending}>
                {editing && typeof editing !== "boolean" ? "Save changes" : "Create connection"}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Stack>
  );
};

const MultiDomainInput = ({
  value,
  onChange,
  error,
}: {
  value: string[];
  onChange: (value: string[]) => void;
  error?: string;
}) => (
  <TagsInput
    label="Allowed email domains"
    placeholder={value.length ? "Add another domain" : "example.com"}
    description="Press Enter after each bare domain. Users outside these domains cannot use this connection."
    value={value}
    error={error}
    onChange={onChange}
    splitChars={[",", " "]}
  />
);

export const Route = createFileRoute("/admin/sso")({
  beforeLoad: async () => {
    const session = await fetchSession();
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
  },
  component: SsoPage,
});
