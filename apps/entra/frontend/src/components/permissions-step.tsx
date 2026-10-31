import { Alert, Button, Group, List, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconPlugConnected } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type EntraConnection, type EntraConnectionTest, requiredPermissions, testConnection } from "../api/connection";
import { ENTRA_QUERY_KEY } from "../api/request";
import { codeOf, reasonText, type Translate } from "../lib/words";
import { CopyValue, MissingPermission, OwnerOnly, PortalPath } from "./step-card";

/** Step 3: the eight permissions to copy, the User Administrator role, and the Owner's *Test connection*. */
export const PermissionsStep = ({ connection, owner }: { connection: EntraConnection; owner: boolean }) => {
  const { t } = useI18n("entra");
  const queryClient = useQueryClient();
  const [result, setResult] = useState<EntraConnectionTest>();
  const present = new Set(connection.lastCheckedAt ? connection.permissionsPresent : []);
  const permissions = requiredPermissions;

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
        <CopyValue value={permissions.join("\n")} />
      </Group>
      <List size="sm" spacing={2} aria-label={t("permissionsToAdd")}>
        {permissions.map((name) => (
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
                <List.Item key={name}>
                  <MissingPermission name={name} />
                </List.Item>
              ))}
            </List>
          </>
        )}
        {!result.roleKnown && <Text size="sm">{t("roleUnknown")}</Text>}
      </Stack>
    </Alert>
  );
};
