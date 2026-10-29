import { Alert, Badge, Button, Group, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconBrandWindows } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useEffect, useRef } from "react";
import {
  completeConnect,
  disconnect,
  type EntraOutlookConnection,
  type OutlookFeature,
  outlookConnectionQueryKey,
  outlookConnectionQueryOptions,
  startConnect,
} from "../api/outlook";
import { CONNECTIONS_PATH, safeReturnTo } from "../lib/return-to";
import { codeOf, outlookFailedText, outlookFeatureText, outlookReasonText, type Translate } from "../lib/words";
import { SectionCard } from "./step-card";

/** What the callback's redirect said: `?outlook=complete`, or `?outlook=failed&code=…`. */
export interface OutlookOutcome {
  outlook: "complete" | "failed";
  code?: string;
}

export interface OutlookConnectionCardProps {
  /** The redirect's outcome, when the page was opened by one. */
  outcome?: OutlookOutcome;
  /** Where to go once a connect completed: always a path on this installation. */
  onReturn: (to: string) => void;
}

export interface OutlookConnectButtonProps {
  feature: OutlookFeature;
  /** The path to come back to after the redirect; one that is not a path becomes the Connections page. */
  returnTo: string;
}

/** Starts a connect and leaves for Microsoft; a refusal is said in its own sentence and the state read again. */
const useConnect = (feature: OutlookFeature, returnTo: string) => {
  const { t } = useI18n("entra");
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => startConnect({ feature, returnTo: safeReturnTo(returnTo) }),
    onError: async (error) => {
      notifications.show({
        color: "red",
        title: t("outlook.couldNotConnect"),
        message: outlookFailedText(codeOf(error), t),
      });
      await queryClient.invalidateQueries({ queryKey: outlookConnectionQueryKey });
    },
  });
};

/** A live connection reads as connected; a closing one, or none, as not connected (design M3). */
const isConnected = (connection: EntraOutlookConnection) => connection.state === "active";

const ConnectedAs = ({ connection, t }: { connection: EntraOutlookConnection; t: Translate }) => (
  <Text size="sm" fw={500}>
    {t("outlook.connectedAs", { account: connection.account ?? "" })}
  </Text>
);

const ReconnectNeeded = ({ connection, t }: { connection: EntraOutlookConnection; t: Translate }) => (
  <Stack gap={0}>
    <Text size="sm" fw={500} c="orange">
      {t("outlook.reconnectNeeded")}
    </Text>
    <Text size="sm" c="dimmed">
      {outlookReasonText(connection.errorCode, t)}
    </Text>
  </Stack>
);

/**
 * The person's Outlook on the Connections page (Outlook connection design
 * M8): *Not connected* with **Connect Outlook**; *Connected as …* since a
 * date, the features using it, and **Disconnect**; or *Reconnect needed*
 * with Microsoft's reason and **Reconnect Outlook**. When nothing can be
 * connected — the switch is off or Vantigo knows no account of theirs — a
 * short line says so. Opened by the callback's redirect, it completes the
 * connect once (`?outlook=complete`) and returns where the connect began, or
 * says why it failed (`?outlook=failed`).
 */
export const OutlookConnectionCard = ({ outcome, onReturn }: OutlookConnectionCardProps) => {
  const { t, formatters } = useI18n("entra");
  const queryClient = useQueryClient();
  const connection = useQuery(outlookConnectionQueryOptions());
  const connect = useConnect("calendar", CONNECTIONS_PATH);

  const complete = useMutation({
    mutationFn: completeConnect,
    onSuccess: (answer) => {
      queryClient.setQueryData(outlookConnectionQueryKey, answer.connection);
      notifications.show({ color: "teal", title: t("outlook.connected"), message: "" });
      onReturn(safeReturnTo(answer.returnTo));
    },
    onError: () => queryClient.invalidateQueries({ queryKey: outlookConnectionQueryKey }),
  });

  // The sealed cookie is spent by the first complete, so React's second effect run must not send another.
  const completing = useRef(false);
  const { mutate: runComplete } = complete;
  useEffect(() => {
    if (outcome?.outlook !== "complete" || completing.current) return;
    completing.current = true;
    runComplete();
  }, [outcome?.outlook, runComplete]);

  const leave = useMutation({
    mutationFn: disconnect,
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("outlook.disconnected"), message: "" });
      await queryClient.invalidateQueries({ queryKey: outlookConnectionQueryKey });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("outlook.couldNotDisconnect"), message: error.message }),
  });

  const askDisconnect = () =>
    modals.openConfirmModal({
      title: t("outlook.disconnectConfirmTitle"),
      children: <Text size="sm">{t("outlook.disconnectConfirmBody")}</Text>,
      labels: { confirm: t("outlook.disconnect"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => leave.mutate(),
    });

  const failure =
    outcome?.outlook === "failed"
      ? outlookFailedText(outcome.code, t)
      : complete.isError
        ? outlookFailedText(codeOf(complete.error), t)
        : undefined;
  const data = connection.data;

  return (
    <SectionCard title={t("outlook.title")} leading={<IconBrandWindows size={22} stroke={1.5} aria-hidden />}>
      {failure && (
        <Alert color="red" variant="light" title={t("outlook.couldNotConnect")}>
          {failure}
        </Alert>
      )}
      {complete.isPending && <Text size="sm">{t("outlook.completing")}</Text>}
      {connection.isPending && <ContentSkeleton rows={1} rowHeight={36} />}
      {connection.isError && (
        <Text size="sm" c="red">
          {t("outlook.loadFailed")}
        </Text>
      )}
      {data && !data.offered && (
        <Text size="sm" c="dimmed">
          {t("outlook.nothingToConnect")}
        </Text>
      )}
      {data?.offered && isConnected(data) && (
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <Stack gap={0}>
            <Group gap="xs">
              <ConnectedAs connection={data} t={t} />
              {data.features.length > 0 && (
                <Badge variant="light" color="teal">
                  {t("outlook.usedBy", {
                    features: data.features.map((feature) => outlookFeatureText(feature, t)).join(", "),
                  })}
                </Badge>
              )}
            </Group>
            {data.connectedAt && (
              <Text size="sm" c="dimmed">
                {t("outlook.connectedSince", {
                  date: formatters.formatDate(new Date(data.connectedAt), { dateStyle: "medium" }),
                })}
              </Text>
            )}
          </Stack>
          <Button variant="light" color="red" loading={leave.isPending} onClick={askDisconnect}>
            {t("outlook.disconnect")}
          </Button>
        </Group>
      )}
      {data?.offered && data.state === "reconnect" && (
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <ReconnectNeeded connection={data} t={t} />
          <Button loading={connect.isPending} onClick={() => connect.mutate()}>
            {t("outlook.reconnect")}
          </Button>
        </Group>
      )}
      {data?.offered && (data.state === "not_connected" || data.state === "closing") && (
        <Group justify="space-between" align="center" wrap="wrap">
          <Text size="sm">{t("outlook.notConnected")}</Text>
          <Button loading={connect.isPending} onClick={() => connect.mutate()}>
            {t("outlook.connect")}
          </Button>
        </Group>
      )}
    </SectionCard>
  );
};

/**
 * The `connection.button` slot's filler (Outlook connection design M8): the
 * person's Outlook inline in another module's card, in the same three states
 * as the Connections page, the connect returning to `returnTo`. Nothing
 * while it loads, fails to load, or when nothing can be connected.
 */
export const OutlookConnectButton = ({ feature, returnTo }: OutlookConnectButtonProps) => {
  const { t } = useI18n("entra");
  const connection = useQuery(outlookConnectionQueryOptions());
  const connect = useConnect(feature, returnTo);
  const data = connection.data;
  if (!data?.offered) return null;

  if (isConnected(data)) return <ConnectedAs connection={data} t={t} />;
  return (
    <Group justify="space-between" align="center" wrap="wrap" gap="sm">
      {data.state === "reconnect" ? (
        <ReconnectNeeded connection={data} t={t} />
      ) : (
        <Text size="sm">{t("outlook.notConnected")}</Text>
      )}
      <Button size="sm" loading={connect.isPending} onClick={() => connect.mutate()}>
        {t(data.state === "reconnect" ? "outlook.reconnect" : "outlook.connect")}
      </Button>
    </Group>
  );
};
