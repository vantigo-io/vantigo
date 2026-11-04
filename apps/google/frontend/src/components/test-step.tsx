import { Button, Group, SimpleGrid, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type GoogleConnection, type GoogleConnectionTest, testConnection } from "../api/connection";
import { GOOGLE_QUERY_KEY } from "../api/request";
import { liveKeys } from "../lib/steps";
import { refusalText } from "../lib/words";
import { ReasonAlert, ScopeLists, StatusBadge } from "./status-card";
import { Fact, OwnerOnly } from "./step-card";

/** What a test found: its status and reason, the organisation, its domains and the scopes. */
const TestResult = ({ result }: { result: GoogleConnectionTest }) => {
  const { t } = useI18n("google");
  return (
    <Stack gap="sm" role="region" aria-label={t("testResult")}>
      <Group gap="xs">
        <Text size="sm" fw={500}>
          {t("testResult")}
        </Text>
        <StatusBadge status={result.status} />
      </Group>
      <ReasonAlert status={result.status} reason={result.reason} />
      {result.customer && (
        <SimpleGrid cols={{ base: 1, sm: 3 }}>
          <Fact label={t("organisation")} value={result.customer.organisationName ?? t("notReadYet")} />
          <Fact label={t("customerId")} value={result.customer.id} />
          <Fact label={t("primaryDomain")} value={result.customer.primaryDomain ?? t("notReadYet")} />
        </SimpleGrid>
      )}
      {result.domains.length > 0 && <Fact label={t("verifiedDomains")} value={result.domains.join(", ")} />}
      <ScopeLists granted={result.granted} missing={result.missing} unexpected={result.unexpected} />
    </Stack>
  );
};

/**
 * *Test the connection*: Google is asked for a token per scope as the admin,
 * then for the organisation; the answer is shown here and written to the
 * connection, so Status shows it too.
 */
export const TestStep = ({ connection, owner }: { connection: GoogleConnection; owner: boolean }) => {
  const { t } = useI18n("google");
  const queryClient = useQueryClient();
  const ready = liveKeys(connection).length > 0 && Boolean(connection.adminSubject);

  const test = useMutation({
    mutationFn: testConnection,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [GOOGLE_QUERY_KEY] });
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotTest"), message: refusalText(error, t) }),
  });

  if (!owner) return <OwnerOnly />;
  return (
    <Stack gap="sm">
      {!ready && (
        <Text size="sm" c="orange">
          {t("testNeedsKeyAndAdmin")}
        </Text>
      )}
      <Group>
        <Button onClick={() => test.mutate()} loading={test.isPending} disabled={!ready}>
          {t("testButton")}
        </Button>
      </Group>
      {test.data && <TestResult result={test.data} />}
    </Stack>
  );
};
