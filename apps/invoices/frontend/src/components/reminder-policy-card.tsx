import { Alert, Button, Card, Group, Radio, Stack, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  POLICY_MODES,
  type ReminderPolicy,
  type ReminderPolicyMode,
  reminderPolicyQueryOptions,
  setReminderPolicy,
} from "../api/policy";
import { ApiValidationError, INVOICES_QUERY_KEY, NotFoundError } from "../api/request";
import "../i18n";
import { useWho } from "../lib/bank";
import { fieldRefusals, refusalMessage } from "../lib/errors";
import { useInvoiceFormat } from "../lib/format";

export interface ReminderPolicyCardProps {
  customerId: number;
  /** Whether the caller holds `invoices:payments`, which changing the policy needs; reading it needs `invoices:access`. */
  canChange: boolean;
  currentUserId?: string;
}

/**
 * Whether the customer is reminded and charged (invoices payments and
 * reminders design D7, D22): normal, without charges, or no reminders at all,
 * with a note and who set it. Read with `invoices:access`; changed with
 * `invoices:payments`. A customer with no invoice or draft here, or one
 * anonymised, takes no policy — that 404 is said in words.
 */
export const ReminderPolicyCard = ({ customerId, canChange, currentUserId }: ReminderPolicyCardProps) => {
  const { t, date } = useInvoiceFormat();
  const policy = useQuery(reminderPolicyQueryOptions(customerId));
  return (
    <Card withBorder padding="lg" radius="md" mt="md" data-testid="reminder-policy-card">
      <Stack gap="sm">
        <Text fw={600} component="h3">
          {t("policy.title")}
        </Text>
        {policy.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("policy.couldNotLoad")}>
            {refusalMessage(policy.error, t, date)}
          </Alert>
        )}
        {policy.isPending && <ContentSkeleton rows={2} rowHeight={36} />}
        {policy.data &&
          (canChange ? (
            <PolicyForm key={policy.data.updatedAt ?? "none"} policy={policy.data} currentUserId={currentUserId} />
          ) : (
            <PolicyRead policy={policy.data} currentUserId={currentUserId} />
          ))}
      </Stack>
    </Card>
  );
};

const PolicyRead = ({ policy, currentUserId }: { policy: ReminderPolicy; currentUserId?: string }) => {
  const { t } = useInvoiceFormat();
  return (
    <Stack gap={4}>
      <Text size="sm" data-testid="policy-mode">
        {t(`policy.mode.${policy.mode}`)}
      </Text>
      <Text size="xs" c="dimmed">
        {t(`policy.modeHint.${policy.mode}`)}
      </Text>
      {policy.note && <Text size="sm">{t("policy.noteIs", { note: policy.note })}</Text>}
      <SetBy policy={policy} currentUserId={currentUserId} />
    </Stack>
  );
};

const SetBy = ({ policy, currentUserId }: { policy: ReminderPolicy; currentUserId?: string }) => {
  const { t, dateTime } = useInvoiceFormat();
  const who = useWho(currentUserId);
  if (!policy.updatedAt) return null;
  return (
    <Text size="xs" c="dimmed">
      {t("policy.setBy", { at: dateTime(policy.updatedAt), who: who(policy.updatedBy) })}
    </Text>
  );
};

const PolicyForm = ({ policy, currentUserId }: { policy: ReminderPolicy; currentUserId?: string }) => {
  const { t, date } = useInvoiceFormat();
  const queryClient = useQueryClient();
  const [mode, setMode] = useState<ReminderPolicyMode>(policy.mode);
  const [note, setNote] = useState(policy.note);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const changed = mode !== policy.mode || note.trim() !== policy.note;
  const save = useMutation({
    mutationFn: () => setReminderPolicy(policy.customerId, { mode, note: note.trim() }),
    onSuccess: async (saved) => {
      queryClient.setQueryData(reminderPolicyQueryOptions(policy.customerId).queryKey, saved);
      await queryClient.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY] });
      notifications.show({ color: "green", message: t("policy.saved") });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        const { onInputs, elsewhere } = fieldRefusals(
          error,
          t,
          (field) => field === "mode" || field === "note",
          "policy",
        );
        setErrors(onInputs);
        if (elsewhere.length > 0)
          notifications.show({ color: "red", title: t("policy.couldNotSave"), message: elsewhere.join(" ") });
        return;
      }
      const message = error instanceof NotFoundError ? t("policy.notFound") : refusalMessage(error, t, date);
      notifications.show({ color: "red", title: t("policy.couldNotSave"), message });
    },
  });
  return (
    <Stack gap="sm">
      <Radio.Group
        label={t("policy.modeLabel")}
        value={mode}
        error={errors.mode}
        onChange={(v) => {
          setMode(v as ReminderPolicyMode);
          setErrors((current) => Object.fromEntries(Object.entries(current).filter(([f]) => f !== "mode")));
        }}
      >
        <Stack gap="xs" mt="xs">
          {POLICY_MODES.map((m) => (
            <Radio key={m} value={m} label={t(`policy.mode.${m}`)} description={t(`policy.modeHint.${m}`)} />
          ))}
        </Stack>
      </Radio.Group>
      <Textarea
        label={t("note")}
        description={t("policy.noteHint")}
        maxLength={500}
        value={note}
        error={errors.note}
        onChange={(e) => {
          setNote(e.currentTarget.value);
          setErrors((current) => Object.fromEntries(Object.entries(current).filter(([f]) => f !== "note")));
        }}
      />
      <SetBy policy={policy} currentUserId={currentUserId} />
      <Group justify="flex-end">
        <Button disabled={!changed || save.isPending} loading={save.isPending} onClick={() => save.mutate()}>
          {t("save")}
        </Button>
      </Group>
    </Stack>
  );
};
