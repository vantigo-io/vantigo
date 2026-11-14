import { Alert, Badge, Button, Group, PasswordInput, Select, Stack, Switch, Table, Text, Title } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlugConnectedX } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, PageHeader, PageSection } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  BANK_FEED_PROVIDERS,
  type BankFeed,
  type BankFeedAccount,
  type BankFeedOffer,
  bankFeedOffersQueryOptions,
  bankFeedQueryOptions,
  deleteBankFeed,
  putBankFeed,
  putBankFeedAccount,
} from "../api/bank-feed";
import { invoicesMetaQueryOptions } from "../api/meta";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import "../i18n";
import { accountNumber } from "../lib/bank";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

const statusColours: Record<string, string> = { ok: "green", unauthorized: "red", failing: "yellow", refused: "red" };

/**
 * Workspace admin › Integrations › Bank (invoices bank feed design F3, F4,
 * F9): the bank connection — the bank and its API key, write-only, verified
 * by the bank before it is stored, and what a key can and cannot do — the
 * bank's accounts with a switch and a start day each, and Disconnect. The
 * host gates the page on the invoices module and `invoices:manage`.
 */
export const BankFeedSettingsPage = () => {
  const { t, date } = useInvoiceFormat();
  const feed = useQuery(bankFeedQueryOptions());
  return (
    <Stack gap="lg">
      <PageHeader title={t("bankFeed.title")} description={t("bankFeed.description")} />
      {feed.isError && !feed.data && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bankFeed.failedToLoad")}>
          {refusalMessage(feed.error, t, date)}
        </Alert>
      )}
      {feed.isPending && <ContentSkeleton rows={3} rowHeight={36} />}
      {feed.data && !feed.data.enabledOnInstance && (
        <Alert color="yellow" icon={<IconAlertCircle size={16} />} title={t("bankFeed.offOnInstanceTitle")}>
          {t("bankFeed.offOnInstance")}
        </Alert>
      )}
      {feed.data && <ConnectionCard feed={feed.data} />}
      {feed.data?.connected && feed.data.enabledOnInstance && <AccountsCard feed={feed.data} />}
    </Stack>
  );
};

/**
 * The connection: the bank and a key, typed and saved, never shown again but
 * as its last four characters; the two sentences of F3 on what a leaked key
 * can and cannot do; the status; and Disconnect, asked first. Every write
 * invalidates `[INVOICES_QUERY_KEY]`.
 */
const ConnectionCard = ({ feed }: { feed: BankFeed }) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [provider, setProvider] = useState<string>(feed.provider ?? BANK_FEED_PROVIDERS[0]);
  const [apiKey, setApiKey] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [refused, setRefused] = useState<string | null>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
  const save = useMutation({
    mutationFn: () => putBankFeed({ provider, apiKey: apiKey.trim() }),
    onMutate: () => setRefused(null),
    onSuccess: async () => {
      setApiKey("");
      setErrors({});
      await refresh();
      notifications.show({ color: "green", message: t("bankFeed.saved") });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(
          error,
          t,
          (field) => field === "apiKey" || field === "provider",
          "bankFeed",
        );
        setErrors(onInputs);
        if (elsewhere.length > 0) setRefused(elsewhere.join(" "));
        return;
      }
      setRefused(refusalMessage(error, t, date));
    },
  });
  const disconnect = useMutation({
    mutationFn: deleteBankFeed,
    onSuccess: async () => {
      await refresh();
      notifications.show({ color: "green", message: t("bankFeed.disconnected") });
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("bankFeed.couldNotDisconnect"),
        message: refusalMessage(error, t, date),
      }),
  });
  const confirmDisconnect = () =>
    modals.openConfirmModal({
      title: t("bankFeed.disconnectTitle"),
      children: <Text size="sm">{t("bankFeed.disconnectConfirm")}</Text>,
      labels: { confirm: t("bankFeed.disconnect"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => disconnect.mutate(),
    });
  return (
    <PageSection data-testid="bank-feed-connection">
      <Stack>
        <Group justify="space-between">
          <Title order={4}>{t("bankFeed.connection")}</Title>
          {feed.connected && feed.status && (
            <Badge color={statusColours[feed.status] ?? "gray"} variant="light">
              {t(`bankFeed.status.${feed.status}`)}
            </Badge>
          )}
        </Group>
        <Alert color="blue" variant="light" title={t("bankFeed.keyReadsTitle")}>
          {t("bankFeed.keyReads")}
        </Alert>
        {feed.connected && (
          <Text size="sm" data-testid="bank-feed-key">
            {t("bankFeed.connectedWith", {
              bank: t(`bankFeed.provider.${feed.provider ?? "folio"}`),
              lastFour: feed.keyLastFour ?? "",
              at: feed.connectedAt ? dateTime(feed.connectedAt) : "",
            })}
          </Text>
        )}
        {feed.status === "unauthorized" && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bankFeed.rejectedTitle")}>
            {feed.rejectedAt ? t("bankFeed.rejectedOn", { at: dateTime(feed.rejectedAt) }) : t("bankFeed.rejected")}
          </Alert>
        )}
        <Select
          label={t("bankFeed.bank")}
          data={BANK_FEED_PROVIDERS.map((p) => ({ value: p, label: t(`bankFeed.provider.${p}`) }))}
          value={provider}
          allowDeselect={false}
          error={errors.provider}
          onChange={(value) => value && setProvider(value)}
        />
        <PasswordInput
          label={t("bankFeed.apiKey")}
          description={feed.connected ? t("bankFeed.apiKeyReplaceHint") : t("bankFeed.apiKeyHint")}
          autoComplete="new-password"
          maxLength={1000}
          error={errors.apiKey}
          value={apiKey}
          onChange={(e) => {
            setApiKey(e.currentTarget.value);
            setErrors({});
          }}
        />
        {refused && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bankFeed.couldNotSave")}>
            {refused}
          </Alert>
        )}
        <Group justify="flex-end">
          {feed.connected && (
            <Button
              variant="default"
              color="red"
              leftSection={<IconPlugConnectedX size={16} />}
              disabled={save.isPending || disconnect.isPending}
              onClick={confirmDisconnect}
            >
              {t("bankFeed.disconnect")}
            </Button>
          )}
          <Button
            loading={save.isPending}
            disabled={!apiKey.trim() || !feed.enabledOnInstance || disconnect.isPending}
            onClick={() => save.mutate()}
          >
            {feed.connected ? t("bankFeed.replaceKey") : t("bankFeed.connect")}
          </Button>
        </Group>
      </Stack>
    </PageSection>
  );
};

/**
 * The bank's accounts, read now: each with its switch — and why one cannot be
 * switched on — a start day for one being switched on, and the status of each
 * the feed reads.
 */
const AccountsCard = ({ feed }: { feed: BankFeed }) => {
  const { t, date } = useInvoiceFormat();
  const offers = useQuery(bankFeedOffersQueryOptions());
  const meta = useQuery(invoicesMetaQueryOptions());
  const byAccount = new Map(feed.accounts.map((a) => [a.account, a]));
  // An account the feed reads that the bank no longer answers is still listed,
  // so it can be switched off.
  const rows: BankFeedOffer[] = offers.data
    ? [
        ...offers.data.data,
        ...feed.accounts
          .filter((a) => a.enabled && !offers.data.data.some((o) => o.account === a.account))
          .map((a) => ({ account: a.account, name: "", kind: "other", canEnable: false })),
      ]
    : [];
  return (
    <PageSection data-testid="bank-feed-accounts">
      <Stack gap="sm">
        <Title order={4}>{t("bankFeed.accounts")}</Title>
        <Text size="sm" c="dimmed">
          {t("bankFeed.accountsDescription")}
        </Text>
        {offers.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("bankFeed.couldNotReadAccounts")}>
            {refusalMessage(offers.error, t, date)}
          </Alert>
        )}
        {offers.isPending && <ContentSkeleton rows={2} rowHeight={32} />}
        {offers.data && rows.length === 0 && (
          <Text size="sm" c="dimmed">
            {t("bankFeed.noAccounts")}
          </Text>
        )}
        {rows.length > 0 && meta.data && (
          <Table.ScrollContainer minWidth={720}>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("bank.account")}</Table.Th>
                  <Table.Th>{t("bankFeed.kind")}</Table.Th>
                  <Table.Th>{t("bankFeed.startDay")}</Table.Th>
                  <Table.Th>{t("bankFeed.state")}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {rows.map((offer) => (
                  <AccountRow
                    key={offer.account}
                    offer={offer}
                    reading={byAccount.get(offer.account)}
                    today={meta.data.today}
                  />
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Stack>
    </PageSection>
  );
};

/** One of the bank's accounts: its switch, its start day, and what the feed says of it. */
const AccountRow = ({
  offer,
  reading,
  today,
}: {
  offer: BankFeedOffer;
  reading: BankFeedAccount | undefined;
  today: string;
}) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const enabled = reading?.enabled === true;
  const [startDay, setStartDay] = useState<string | null>(today);
  const toggle = useMutation({
    mutationFn: (on: boolean) =>
      putBankFeedAccount(offer.account, on ? { enabled: true, startDay: startDay ?? today } : { enabled: false }),
    onSuccess: async (_, on) => {
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: on ? t("bankFeed.switchedOn") : t("bankFeed.switchedOff") });
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("bankFeed.couldNotSwitch"),
        message:
          error instanceof ApiValidationError
            ? fieldRefusals(error, t, () => false, "bankFeed").elsewhere.join(" ")
            : refusalMessage(error, t, date),
      }),
  });
  const label = offer.name ? `${accountNumber(offer.account)} · ${offer.name}` : accountNumber(offer.account);
  return (
    <Table.Tr data-testid={`bank-feed-account-${offer.account}`}>
      <Table.Td>
        <Stack gap={0}>
          <Text size="sm">{accountNumber(offer.account)}</Text>
          <Text size="xs" c="dimmed">
            {offer.name}
          </Text>
        </Stack>
      </Table.Td>
      <Table.Td>{t(`bankFeed.kind.${offer.kind}`)}</Table.Td>
      <Table.Td>
        {enabled && reading ? (
          date(reading.startDay)
        ) : offer.canEnable ? (
          <DateInput
            aria-label={t("bankFeed.startDayOf", { account: label })}
            size="xs"
            valueFormat={t("dateInputFormat")}
            maxDate={today}
            value={startDay}
            onChange={setStartDay}
          />
        ) : (
          t("notAvailable")
        )}
      </Table.Td>
      <Table.Td>
        {reading ? (
          <Stack gap={0}>
            <Badge color={statusColours[reading.status] ?? "gray"} variant="light">
              {t(`bankFeed.accountStatus.${reading.status}`)}
            </Badge>
            {reading.lastSyncedAt && (
              <Text size="xs" c="dimmed">
                {t("bankFeed.lastSynced", { at: dateTime(reading.lastSyncedAt) })}
              </Text>
            )}
          </Stack>
        ) : (
          offer.whyNot && (
            <Text size="xs" c="dimmed">
              {t(`bankFeed.whyNot.${offer.whyNot}`)}
            </Text>
          )
        )}
      </Table.Td>
      <Table.Td ta="right">
        <Switch
          aria-label={t("bankFeed.switchOf", { account: label })}
          checked={enabled}
          disabled={toggle.isPending || (!enabled && !offer.canEnable)}
          onChange={(e) => toggle.mutate(e.currentTarget.checked)}
        />
      </Table.Td>
    </Table.Tr>
  );
};
