import {
  Alert,
  Badge,
  Button,
  Card,
  Checkbox,
  Group,
  Modal,
  PasswordInput,
  Select,
  SimpleGrid,
  Stack,
  Table,
  TagsInput,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCircleCheck, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { sitesQueryOptions } from "../api/deliveries";
import { ApiValidationError, ORDERS_QUERY_KEY } from "../api/request";
import {
  carrierAccountsQueryOptions,
  carrierServicesQueryOptions,
  createCarrierAccount,
  saveSiteCarrierDefaults,
  siteCarrierDefaultsQueryOptions,
  updateCarrierAccount,
  verifyCarrierAccount,
} from "../api/shipping";
import type { CarrierAccount, CarrierAccountInput, CarrierVerification } from "../api/types";
import "../i18n";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";

/**
 * Workspace admin › Integrations › Shipping (orders design O51): the carrier
 * accounts — Bring's Mybring user and API key, write-only and sealed, the
 * customer numbers, the test mode, the sender and the default service —
 * each verified against the carrier, and the default account per site. The
 * host gates the page on the orders module and `orders:carriers-manage`.
 */
export const ShippingSettingsPage = () => {
  const { t } = useOrdersFormat();
  const accounts = useQuery(carrierAccountsQueryOptions());
  const [editing, setEditing] = useState<CarrierAccount | "new" | null>(null);
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("shippingTitle")}
        description={t("shippingDescription")}
        actions={
          <Button
            leftSection={<IconPlus size={16} />}
            onClick={() => setEditing("new")}
            data-assist="orders.shipping.add-account"
          >
            {t("addCarrierAccount")}
          </Button>
        }
      />
      {accounts.isPending && <ContentSkeleton rows={3} rowHeight={48} />}
      {accounts.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />}>
          {refusalMessage(accounts.error, t)}
        </Alert>
      )}
      {accounts.data && accounts.data.items.length === 0 && (
        <EmptyState title={t("noCarrierAccounts")} description={t("noCarrierAccountsDescription")} />
      )}
      {accounts.data?.items.map((account) => (
        <AccountCard key={`${account.id}:${account.version}`} account={account} onEdit={() => setEditing(account)} />
      ))}
      {accounts.data && accounts.data.items.length > 0 && <SiteDefaultsCard accounts={accounts.data.items} />}
      {editing && <AccountDialog account={editing === "new" ? null : editing} onClose={() => setEditing(null)} />}
    </Stack>
  );
};

const AccountCard = ({ account, onEdit }: { account: CarrierAccount; onEdit: () => void }) => {
  const { t, dateTime } = useOrdersFormat();
  const queryClient = useQueryClient();
  const [result, setResult] = useState<CarrierVerification | null>(null);
  const verify = useMutation({
    mutationFn: () => verifyCarrierAccount(account.id),
    onSuccess: async (answer) => {
      setResult(answer);
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY, "carrier-accounts"] });
    },
    onError: (error) => notifications.show({ color: "red", title: t("verify"), message: refusalMessage(error, t) }),
  });
  return (
    <Card withBorder padding="lg" data-testid="carrier-account">
      <Stack gap="xs">
        <Group justify="space-between">
          <Group gap="xs">
            <Title order={4}>{account.name}</Title>
            <Badge variant="light">{account.carrier === "bring" ? "Bring" : account.carrier}</Badge>
            {account.test && (
              <Badge variant="outline" color="yellow">
                {t("testBadge")}
              </Badge>
            )}
            {account.status === "archived" && <Badge color="gray">{t("accountArchived")}</Badge>}
          </Group>
          <Group gap="xs">
            <Button size="xs" variant="default" onClick={onEdit}>
              {t("editCarrierAccount")}
            </Button>
            <Button
              size="xs"
              variant="light"
              leftSection={<IconCircleCheck size={14} />}
              loading={verify.isPending}
              disabled={!account.carrierAllowed}
              onClick={() => verify.mutate()}
              data-assist="orders.shipping.verify"
            >
              {t("verify")}
            </Button>
          </Group>
        </Group>
        {!account.carrierAllowed && (
          <Alert color="yellow" p="xs">
            {t("carrierWithheld")}
          </Alert>
        )}
        <Text size="sm">
          {t("customerNumbers")}: {account.customerNumbers.join(", ")}
          {account.defaultService && ` · ${t("defaultService")}: ${account.defaultService}`}
        </Text>
        <Text size="xs" c="dimmed">
          {account.verifiedAt ? t("verified", { when: dateTime(account.verifiedAt) }) : t("notVerified")}
        </Text>
        {result && (
          <Alert
            color={result.result === "ok" ? (result.pricesAvailable ? "green" : "yellow") : "red"}
            p="xs"
            data-testid="verification"
          >
            <Stack gap={4}>
              <Text size="sm">{t(`verify.${result.result}`)}</Text>
              {result.message && <Text size="xs">{result.message}</Text>}
              {result.result === "ok" && !result.pricesAvailable && <Text size="xs">{t("pricesUnavailable")}</Text>}
              {result.services.length > 0 && (
                <Text size="xs" c="dimmed">
                  {t("servicesAvailable")}: {result.services.map((s) => `${s.code} ${s.name}`).join(", ")}
                </Text>
              )}
            </Stack>
          </Alert>
        )}
      </Stack>
    </Card>
  );
};

const AccountDialog = ({ account, onClose }: { account: CarrierAccount | null; onClose: () => void }) => {
  const { t } = useOrdersFormat();
  const queryClient = useQueryClient();
  const [name, setName] = useState(account?.name ?? "Bring");
  const [customers, setCustomers] = useState<string[]>(account?.customerNumbers ?? []);
  const [uid, setUid] = useState("");
  const [key, setKey] = useState("");
  const [test, setTest] = useState(account?.test ?? true);
  const [sender, setSender] = useState(account?.sender ?? {});
  const [service, setService] = useState<string | null>(account?.defaultService ?? null);
  const [archived, setArchived] = useState(account?.status === "archived");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const services = useQuery({ ...carrierServicesQueryOptions(account?.id ?? 0), enabled: account !== null });
  const save = useMutation({
    mutationFn: () => {
      const input: CarrierAccountInput = {
        carrier: "bring",
        name,
        customerNumbers: customers,
        test,
        sender,
        defaultService: service,
        status: archived ? "archived" : "active",
        ...(uid || key ? { credentials: { uid, key } } : {}),
        ...(account ? { version: account.version } : {}),
      };
      return account ? updateCarrierAccount(account.id, input) : createCarrierAccount(input);
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY, "carrier-accounts"] });
      notifications.show({ color: "green", message: t("carrierAccountSaved") });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError)
        setErrors(
          Object.fromEntries(Object.entries(error.errors).map(([field, messages]) => [field, messages[0] ?? ""])),
        );
      notifications.show({ color: "red", title: t("couldNotSaveCarrierAccount"), message: refusalMessage(error, t) });
    },
  });
  const senderField = (field: keyof typeof sender, label: string) => (
    <TextInput
      label={label}
      value={sender[field] ?? ""}
      error={errors[`sender.${field}`]}
      onChange={(e) => setSender({ ...sender, [field]: e.currentTarget.value })}
    />
  );
  return (
    <Modal opened onClose={onClose} size="lg" title={account ? t("editCarrierAccount") : t("addCarrierAccount")}>
      <Stack>
        <TextInput
          label={t("accountName")}
          value={name}
          error={errors.name}
          onChange={(e) => setName(e.currentTarget.value)}
        />
        <TagsInput
          label={t("customerNumbers")}
          description={t("customerNumbersHint")}
          value={customers}
          error={errors.customerNumbers}
          onChange={setCustomers}
        />
        <Text size="xs" c="dimmed">
          {account?.hasCredentials ? t("credentialsStored") : t("credentialsWriteOnly")}
        </Text>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t("mybringUid")}
            description={t("mybringUidHint")}
            autoComplete="off"
            value={uid}
            error={errors["credentials.uid"] ?? errors.credentials}
            onChange={(e) => setUid(e.currentTarget.value)}
          />
          <PasswordInput
            label={t("mybringKey")}
            autoComplete="new-password"
            value={key}
            error={errors["credentials.key"]}
            onChange={(e) => setKey(e.currentTarget.value)}
          />
        </SimpleGrid>
        <Checkbox
          label={t("testMode")}
          description={t("testModeHint")}
          checked={test}
          onChange={(e) => setTest(e.currentTarget.checked)}
        />
        {account && (
          <Select
            label={t("defaultService")}
            clearable
            data={(services.data?.items ?? []).map((s) => ({ value: s.code, label: `${s.code} ${s.name}` }))}
            value={service}
            error={errors.defaultService}
            onChange={setService}
          />
        )}
        <Title order={5}>{t("sender")}</Title>
        <Text size="xs" c="dimmed">
          {t("senderHint")}
        </Text>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          {senderField("name", t("senderName"))}
          {senderField("line1", t("senderLine1"))}
          {senderField("postalCode", t("senderPostalCode"))}
          {senderField("city", t("senderCity"))}
          {senderField("country", t("senderCountry"))}
          {senderField("phone", t("senderPhone"))}
          {senderField("email", t("senderEmail"))}
        </SimpleGrid>
        {account && (
          <Checkbox
            label={t("archiveAccount")}
            checked={archived}
            onChange={(e) => setArchived(e.currentTarget.checked)}
          />
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t("cancelDialog")}
          </Button>
          <Button loading={save.isPending} onClick={() => save.mutate()}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};

const SiteDefaultsCard = ({ accounts }: { accounts: CarrierAccount[] }) => {
  const { t } = useOrdersFormat();
  const queryClient = useQueryClient();
  const sites = useQuery(sitesQueryOptions());
  const defaults = useQuery(siteCarrierDefaultsQueryOptions());
  const [chosen, setChosen] = useState<Record<number, number | null>>({});
  const current = (siteId: number) =>
    siteId in chosen ? chosen[siteId] : (defaults.data?.items.find((d) => d.siteId === siteId)?.accountId ?? null);
  const fulfilling = (sites.data?.items ?? []).filter((site) => site.active && site.fulfilsOrders);
  const save = useMutation({
    mutationFn: () =>
      saveSiteCarrierDefaults({
        items: fulfilling.flatMap((site) => {
          const accountId = current(site.id);
          return accountId == null ? [] : [{ siteId: site.id, accountId }];
        }),
      }),
    onSuccess: async () => {
      setChosen({});
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY, "carrier-accounts", "site-defaults"] });
      notifications.show({ color: "green", message: t("siteDefaultsSaved") });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(error, t) }),
  });
  if (fulfilling.length === 0) return null;
  const options = accounts.filter((a) => a.status === "active").map((a) => ({ value: String(a.id), label: a.name }));
  return (
    <Card withBorder padding="lg" data-testid="site-defaults">
      <Stack>
        <Title order={4}>{t("siteDefaults")}</Title>
        <Text size="sm" c="dimmed">
          {t("siteDefaultsHint")}
        </Text>
        <Table>
          <Table.Tbody>
            {fulfilling.map((site) => (
              <Table.Tr key={site.id}>
                <Table.Td>
                  {site.code} · {site.name}
                </Table.Td>
                <Table.Td>
                  <Select
                    aria-label={`${site.code} ${t("siteDefaults")}`}
                    placeholder={t("noDefaultAccount")}
                    clearable
                    data={options}
                    value={current(site.id) == null ? null : String(current(site.id))}
                    onChange={(value) => setChosen({ ...chosen, [site.id]: value ? Number(value) : null })}
                  />
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
        <Group>
          <Button disabled={Object.keys(chosen).length === 0} loading={save.isPending} onClick={() => save.mutate()}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </Card>
  );
};
