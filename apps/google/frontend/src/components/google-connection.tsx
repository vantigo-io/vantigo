import { Alert, Badge, Button, Group, Stack, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconBrandGoogle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useEffect, useRef } from "react";
import {
  completeConnect,
  disconnect,
  type GoogleFeature,
  type GooglePersonalConnection,
  personalConnectionQueryKey,
  personalConnectionQueryOptions,
  startConnect,
} from "../api/personal";
import "../i18n";
import { CONNECTIONS_PATH, safeReturnTo } from "../lib/return-to";
import { codeOf, personalFailedText, personalFeatureText, personalReasonText, type Translate } from "../lib/words";
import { SectionCard } from "./step-card";

/** What the callback's redirect said: `outcome=complete`, or `outcome=failed&code=…`. */
export interface GoogleConnectionOutcome {
  outcome: "complete" | "failed";
  code?: string;
}

/** The host's card props, so the Connections page lists this card as it is. */
export interface GoogleConnectionCardProps {
  /** The redirect's outcome, when the page was opened by Google's callback. */
  outcome?: GoogleConnectionOutcome;
  /** Where to go once a connect completed: always a path on this installation. */
  onReturn: (to: string) => void;
  /** Told whether Google offers the person anything, once the connection is read. */
  onOffered?: (offered: boolean) => void;
}

export interface GoogleConnectButtonProps {
  feature: GoogleFeature;
  /** The path to come back to after the redirect; one that is not a path becomes the Connections page. */
  returnTo: string;
}

/** Starts a connect and leaves for Google; a refusal is said in its own sentence and the state read again. */
const useConnect = (feature: GoogleFeature, returnTo: string) => {
  const { t } = useI18n("google");
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => startConnect({ feature, returnTo: safeReturnTo(returnTo) }),
    onError: async (error) => {
      notifications.show({
        color: "red",
        title: t("personal.couldNotConnect"),
        message: personalFailedText(codeOf(error), t),
      });
      await queryClient.invalidateQueries({ queryKey: personalConnectionQueryKey });
    },
  });
};

/** A live connection reads as connected; a closing one, or none, as not connected. */
const isConnected = (connection: GooglePersonalConnection) => connection.state === "active";

const ConnectedAs = ({ connection, t }: { connection: GooglePersonalConnection; t: Translate }) => (
  <Text size="sm" fw={500}>
    {t("personal.connectedAs", { account: connection.account ?? "" })}
  </Text>
);

const ReconnectNeeded = ({ connection, t }: { connection: GooglePersonalConnection; t: Translate }) => (
  <Stack gap={0}>
    <Text size="sm" fw={500} c="orange">
      {t("personal.reconnectNeeded")}
    </Text>
    <Text size="sm" c="dimmed">
      {personalReasonText(connection.errorCode, t)}
    </Text>
  </Stack>
);

/**
 * The person's Google on the Connections page (Google Workspace design G9):
 * *Not connected* with **Connect Google**; *Connected as …* since a date, the
 * features using it, and **Disconnect**; or *Reconnect needed* with Google's
 * reason and **Reconnect Google**. When nothing can be connected — the switch
 * is off or Vantigo knows no Google account of theirs — it renders nothing
 * and tells `onOffered` so; a failed callback still shows its sentence.
 * Opened by the callback's redirect, it completes the connect once
 * (`outcome=complete`) and returns where the connect began, or says why it
 * failed (`outcome=failed`).
 */
export const GoogleConnectionCard = ({ outcome, onReturn, onOffered }: GoogleConnectionCardProps) => {
  const { t, formatters } = useI18n("google");
  const queryClient = useQueryClient();
  const connection = useQuery(personalConnectionQueryOptions());
  const connect = useConnect("calendar", CONNECTIONS_PATH);

  const complete = useMutation({
    mutationFn: completeConnect,
    onSuccess: (answer) => {
      queryClient.setQueryData(personalConnectionQueryKey, answer.connection);
      notifications.show({ color: "teal", title: t("personal.connected"), message: "" });
      onReturn(safeReturnTo(answer.returnTo));
    },
    onError: () => queryClient.invalidateQueries({ queryKey: personalConnectionQueryKey }),
  });

  // The sealed cookie is spent by the first complete, so React's second effect run must not send another.
  const completing = useRef(false);
  const { mutate: runComplete } = complete;
  useEffect(() => {
    if (outcome?.outcome !== "complete" || completing.current) return;
    completing.current = true;
    runComplete();
  }, [outcome?.outcome, runComplete]);

  const leave = useMutation({
    mutationFn: disconnect,
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("personal.disconnected"), message: "" });
      await queryClient.invalidateQueries({ queryKey: personalConnectionQueryKey });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("personal.couldNotDisconnect"), message: error.message }),
  });

  const askDisconnect = () =>
    modals.openConfirmModal({
      title: t("personal.disconnectConfirmTitle"),
      children: <Text size="sm">{t("personal.disconnectConfirmBody")}</Text>,
      labels: { confirm: t("personal.disconnect"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => leave.mutate(),
    });

  const failure =
    outcome?.outcome === "failed"
      ? personalFailedText(outcome.code, t)
      : complete.isError
        ? personalFailedText(codeOf(complete.error), t)
        : undefined;
  const data = connection.data;
  const offered = data?.offered;
  useEffect(() => {
    if (offered !== undefined) onOffered?.(offered);
  }, [offered, onOffered]);

  if (data && !data.offered && !failure && !complete.isPending) return null;

  return (
    <SectionCard title={t("personal.title")} leading={<IconBrandGoogle size={22} stroke={1.5} aria-hidden />}>
      {failure && (
        <Alert color="red" variant="light" title={t("personal.couldNotConnect")}>
          {failure}
        </Alert>
      )}
      {complete.isPending && <Text size="sm">{t("personal.completing")}</Text>}
      {connection.isPending && <ContentSkeleton rows={1} rowHeight={36} />}
      {connection.isError && (
        <Text size="sm" c="red">
          {t("personal.loadFailed")}
        </Text>
      )}
      {data?.offered && isConnected(data) && (
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <Stack gap={0}>
            <Group gap="xs">
              <ConnectedAs connection={data} t={t} />
              {data.features.length > 0 && (
                <Badge variant="light" color="teal">
                  {t("personal.usedBy", {
                    features: data.features.map((feature) => personalFeatureText(feature, t)).join(", "),
                  })}
                </Badge>
              )}
            </Group>
            {data.connectedAt && (
              <Text size="sm" c="dimmed">
                {t("personal.connectedSince", {
                  date: formatters.formatDate(new Date(data.connectedAt), { dateStyle: "medium" }),
                })}
              </Text>
            )}
          </Stack>
          <Button variant="light" color="red" loading={leave.isPending} onClick={askDisconnect}>
            {t("personal.disconnect")}
          </Button>
        </Group>
      )}
      {data?.offered && data.state === "reconnect" && (
        <Group justify="space-between" align="flex-start" wrap="wrap">
          <ReconnectNeeded connection={data} t={t} />
          <Button loading={connect.isPending} onClick={() => connect.mutate()}>
            {t("personal.reconnect")}
          </Button>
        </Group>
      )}
      {data?.offered && (data.state === "not_connected" || data.state === "closing") && (
        <Group justify="space-between" align="center" wrap="wrap">
          <Text size="sm">{t("personal.notConnected")}</Text>
          <Button loading={connect.isPending} onClick={() => connect.mutate()}>
            {t("personal.connect")}
          </Button>
        </Group>
      )}
    </SectionCard>
  );
};

/**
 * The `connection.button` slot's filler: the person's Google inline in
 * another module's card, in the same three states as the Connections page,
 * the connect returning to `returnTo`. Nothing while it loads, fails to load,
 * or when nothing can be connected.
 */
export const GoogleConnectButton = ({ feature, returnTo }: GoogleConnectButtonProps) => {
  const { t } = useI18n("google");
  const connection = useQuery(personalConnectionQueryOptions());
  const connect = useConnect(feature, returnTo);
  const data = connection.data;
  if (!data?.offered) return null;

  if (isConnected(data) && data.features.includes(feature)) {
    return <ConnectedAs connection={data} t={t} />;
  }

  return (
    <Group justify="space-between" align="center" wrap="wrap" gap="sm">
      {isConnected(data) ? (
        <ConnectedAs connection={data} t={t} />
      ) : data.state === "reconnect" ? (
        <ReconnectNeeded connection={data} t={t} />
      ) : (
        <Text size="sm">{t("personal.notConnected")}</Text>
      )}
      <Button size="sm" loading={connect.isPending} onClick={() => connect.mutate()}>
        {t(data.state === "reconnect" ? "personal.reconnect" : "personal.connect")}
      </Button>
    </Group>
  );
};
