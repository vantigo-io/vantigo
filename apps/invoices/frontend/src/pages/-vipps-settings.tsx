import {
  Alert,
  Badge,
  Button,
  Card,
  Group,
  PasswordInput,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { useState } from "react";
import { invoicesMetaQueryOptions } from "../api/meta";
import { ApiValidationError, INVOICES_QUERY_KEY } from "../api/request";
import {
  deleteVippsSettings,
  putVippsSettings,
  type VippsSettings,
  type VippsSettingsInput,
  verifyVippsSettings,
  vippsSettingsQueryOptions,
} from "../api/vipps";
import "../i18n";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

const keyFields = ["clientId", "clientSecret", "subscriptionKey"] as const;
type KeyField = (typeof keyFields)[number];

const verifyColours: Record<string, string> = { ok: "green", unauthorized: "red", unreachable: "yellow" };

/**
 * The Vipps credentials (invoices payments and reminders design D14), read
 * from the server: the form is drawn once the stored sales unit is known.
 */
export const VippsSettingsCard = () => {
  const { t, date } = useInvoiceFormat();
  const stored = useQuery(vippsSettingsQueryOptions());
  if (stored.isError && !stored.data) {
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("vipps.failedToLoad")}>
        {refusalMessage(stored.error, t, date)}
      </Alert>
    );
  }
  if (!stored.data) return <ContentSkeleton rows={3} rowHeight={36} />;
  return <VippsForm stored={stored.data} />;
};

/** A refusal said on the card: what was attempted, and why it was refused. */
interface Refused {
  title: string;
  message: string;
}

/**
 * The sales unit and its three keys, write-only: typed, saved, never shown
 * again. A key field left blank is left out of the save, never sent empty, so
 * the stored key is kept — the server refuses an empty one. Verify asks Vipps
 * with the stored keys and says how it went; Remove asks first. A refusal by
 * Vipps — the card's own `rejectedAt`, or meta's `vippsCredentialsRejected` —
 * is said with when it happened. Every write invalidates
 * `[INVOICES_QUERY_KEY]`, so meta's availability follows.
 */
const VippsForm = ({ stored }: { stored: VippsSettings }) => {
  const { t, date, dateTime } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const meta = useQuery(invoicesMetaQueryOptions());
  const [msn, setMsn] = useState(stored.msn ?? "");
  const [keys, setKeys] = useState<Record<KeyField, string>>({ clientId: "", clientSecret: "", subscriptionKey: "" });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [verified, setVerified] = useState<string | null>(null);
  const [refused, setRefused] = useState<Refused | null>(null);
  const hasKeys = stored.hasCredentials;
  const rejected = stored.rejectedAt !== undefined || (hasKeys && meta.data?.vippsCredentialsRejected === true);
  const refresh = () => queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
  const clear = () => {
    setVerified(null);
    setRefused(null);
  };
  const save = useMutation({
    mutationFn: () => {
      const input: VippsSettingsInput = { msn: msn.trim() };
      // A blank key keeps the stored one: it is left out, never sent empty.
      for (const field of keyFields) if (keys[field].trim()) input[field] = keys[field];
      return putVippsSettings(input);
    },
    onMutate: clear,
    onSuccess: async () => {
      setKeys({ clientId: "", clientSecret: "", subscriptionKey: "" });
      setErrors({});
      await refresh();
      notifications.show({ color: "green", message: t("vipps.saved") });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(
          error,
          t,
          (field) => field === "msn" || (keyFields as readonly string[]).includes(field),
          "vipps",
        );
        setErrors(onInputs);
        if (elsewhere.length > 0) setRefused({ title: t("vipps.couldNotSave"), message: elsewhere.join(" ") });
        return;
      }
      setRefused({ title: t("vipps.couldNotSave"), message: refusalMessage(error, t, date) });
    },
  });
  const verify = useMutation({
    mutationFn: verifyVippsSettings,
    onMutate: clear,
    onSuccess: async ({ result }) => {
      setVerified(result);
      await refresh();
    },
    onError: (error) => setRefused({ title: t("vipps.couldNotVerify"), message: refusalMessage(error, t, date) }),
  });
  const remove = useMutation({
    mutationFn: deleteVippsSettings,
    onMutate: clear,
    onSuccess: async () => {
      setKeys({ clientId: "", clientSecret: "", subscriptionKey: "" });
      setMsn("");
      await refresh();
      notifications.show({ color: "green", message: t("vipps.removed") });
    },
    onError: (error) => setRefused({ title: t("vipps.couldNotRemove"), message: refusalMessage(error, t, date) }),
  });
  const confirmRemove = () =>
    modals.openConfirmModal({
      title: t("vipps.removeTitle"),
      children: <Text size="sm">{t("vipps.removeConfirm")}</Text>,
      labels: { confirm: t("vipps.remove"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(),
    });
  const busy = save.isPending || verify.isPending || remove.isPending;
  const unsetError = (field: string) =>
    setErrors((current) => {
      const next = { ...current };
      delete next[field];
      return next;
    });
  return (
    <Card withBorder data-testid="vipps-settings">
      <Stack>
        <Group justify="space-between">
          <Title order={4}>{t("vipps.title")}</Title>
          {hasKeys && (
            <Badge color="green" variant="light">
              {t("vipps.keysStored")}
            </Badge>
          )}
        </Group>
        <Text size="sm" c="dimmed">
          {t("vipps.hint")}
        </Text>
        {rejected && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("vipps.rejectedTitle")}>
            {stored.rejectedAt ? t("vipps.rejectedOn", { at: dateTime(stored.rejectedAt) }) : t("vipps.rejected")}
          </Alert>
        )}
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t("vipps.msn")}
            inputMode="numeric"
            maxLength={10}
            error={errors.msn}
            value={msn}
            onChange={(e) => {
              setMsn(e.currentTarget.value);
              unsetError("msn");
            }}
          />
          {keyFields.map((field) => (
            <PasswordInput
              key={field}
              label={t(`vipps.${field}`)}
              description={hasKeys ? t("vipps.keyStoredHint") : t("vipps.keyNewHint")}
              autoComplete="new-password"
              maxLength={1000}
              error={errors[field]}
              value={keys[field]}
              onChange={(e) => {
                const value = e.currentTarget.value;
                setKeys((current) => ({ ...current, [field]: value }));
                unsetError(field);
              }}
            />
          ))}
        </SimpleGrid>
        {verified && (
          <Alert color={verifyColours[verified] ?? "gray"} data-verify-result={verified}>
            {t(`vipps.verify.${verified}`)}
          </Alert>
        )}
        {refused && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={refused.title}>
            {refused.message}
          </Alert>
        )}
        <Group justify="flex-end">
          <Button
            variant="default"
            color="red"
            leftSection={<IconTrash size={16} />}
            disabled={busy || !hasKeys}
            loading={remove.isPending}
            onClick={confirmRemove}
          >
            {t("vipps.remove")}
          </Button>
          <Button
            variant="default"
            disabled={busy || !hasKeys}
            loading={verify.isPending}
            onClick={() => verify.mutate()}
          >
            {t("vipps.verify")}
          </Button>
          <Button disabled={busy} loading={save.isPending} onClick={() => save.mutate()}>
            {t("vipps.save")}
          </Button>
        </Group>
      </Stack>
    </Card>
  );
};
