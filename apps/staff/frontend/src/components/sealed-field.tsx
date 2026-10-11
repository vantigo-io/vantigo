import { Button, Group, Modal, Select, Stack, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconEye, IconEyeOff } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import {
  revealBankAccount,
  revealNationalId,
  type StaffBankAccountKind,
  type StaffNationalIdKind,
  type StaffPerson,
  setBankAccount,
  setNationalId,
} from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals } from "../lib/errors";
import { bankAccountKinds, nationalIdKinds } from "../lib/terms";

/** How long a revealed value stays on screen (design §7: each reveal confirmed, then shown briefly). */
export const REVEAL_SECONDS = 30;

export type SealedKind = "nationalId" | "bankAccount";

export interface SealedFieldProps {
  person: StaffPerson;
  kind: SealedKind;
}

/**
 * A sealed identifier (design S4) as the person page shows it: the national
 * id as "on file" with its kind, the bank account as its last four digits —
 * never more. A holder of `staff:personal-data` (`canRevealIdentifiers`)
 * may reveal it, after a confirmation, for {@link REVEAL_SECONDS} seconds,
 * and may set it.
 *
 * The revealed value lives in this component's state alone: the reveal is a
 * POST sent `no-store`, never a query, and its mutation is reset the moment
 * the value is taken, so neither cache keeps a copy once it is hidden.
 */
export const SealedField = ({ person, kind }: SealedFieldProps) => {
  const { t } = useI18n("staff");
  const canReveal = person.capabilities.canRevealIdentifiers;
  const [shown, setShown] = useState<{ value: string; left: number } | null>(null);
  const [setting, setSetting] = useState(false);

  const reveal = useMutation({
    mutationFn: (): Promise<{ value: string; country?: string }> =>
      kind === "nationalId" ? revealNationalId(person.id) : revealBankAccount(person.id),
    gcTime: 0,
  });

  // The countdown: a second at a time, then the value is gone.
  const counting = shown !== null;
  useEffect(() => {
    if (!counting) return;
    const timer = setInterval(
      () => setShown((current) => (current && current.left > 1 ? { ...current, left: current.left - 1 } : null)),
      1000,
    );
    return () => clearInterval(timer);
  }, [counting]);

  const nationalId = person.nationalId;
  const bank = person.bankAccount;
  const onFile = kind === "nationalId" ? nationalId?.onFile === true : bank !== undefined;
  const label = t(kind);
  const summary =
    kind === "nationalId"
      ? nationalId?.onFile
        ? t("onFile")
        : t("notOnFile")
      : bank
        ? t("endingIn", { last4: bank.last4 })
        : t("notOnFile");
  const kindLabel =
    kind === "nationalId"
      ? nationalId?.kind && t(`nationalIdKind_${nationalId.kind}`)
      : bank && t(`bankAccountKind_${bank.kind}`);

  const confirmReveal = () =>
    modals.openConfirmModal({
      title: t(kind === "nationalId" ? "revealNationalIdTitle" : "revealBankAccountTitle"),
      children: <Text size="sm">{t("revealExplained", { seconds: REVEAL_SECONDS })}</Text>,
      labels: { confirm: t("reveal"), cancel: t("cancel") },
      onConfirm: async () => {
        try {
          const answer = await reveal.mutateAsync();
          reveal.reset();
          const country = answer.country ? ` (${answer.country})` : "";
          setShown({ value: `${answer.value}${country}`, left: REVEAL_SECONDS });
        } catch (error) {
          reveal.reset();
          notifications.show({ color: "red", title: t("couldNotReveal"), message: (error as Error).message });
        }
      },
    });

  return (
    <Stack gap={4}>
      <Text size="xs" c="dimmed">
        {label}
      </Text>
      {shown ? (
        <Group gap="sm" wrap="wrap">
          <Text size="sm" ff="monospace" fw={600}>
            {shown.value}
          </Text>
          <Text size="xs" c="dimmed" aria-live="polite">
            {t("hiddenIn", { seconds: shown.left })}
          </Text>
          <Button
            size="compact-xs"
            variant="subtle"
            leftSection={<IconEyeOff size={14} />}
            onClick={() => setShown(null)}
          >
            {t("hide")}
          </Button>
        </Group>
      ) : (
        <Group gap="sm" wrap="wrap">
          <Text size="sm">{summary}</Text>
          {kindLabel && (
            <Text size="xs" c="dimmed">
              {kindLabel}
            </Text>
          )}
          {canReveal && onFile && (
            <Button
              size="compact-xs"
              variant="subtle"
              leftSection={<IconEye size={14} />}
              aria-label={t(kind === "nationalId" ? "revealNationalId" : "revealBankAccount")}
              loading={reveal.isPending}
              onClick={confirmReveal}
            >
              {t("reveal")}
            </Button>
          )}
          {canReveal && (
            <Button
              size="compact-xs"
              variant="subtle"
              aria-label={t(kind === "nationalId" ? "setNationalId" : "setBankAccount")}
              onClick={() => setSetting(true)}
            >
              {t("set")}
            </Button>
          )}
        </Group>
      )}
      {canReveal && <SetSealedModal person={person} kind={kind} opened={setting} onClose={() => setSetting(false)} />}
    </Stack>
  );
};

interface SealedFormValues {
  kind: string | null;
  value: string;
  country: string;
}

/**
 * Sets a sealed identifier. What is typed is sent once and dropped with the
 * form: the modal's fields are not mounted while it is closed, so nothing of
 * the number outlives the save. A refusal never echoes a digit (S4); it lands
 * on the field it names.
 */
const SetSealedModal = ({
  person,
  kind,
  opened,
  onClose,
}: {
  person: StaffPerson;
  kind: SealedKind;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  const title = t(kind === "nationalId" ? "setNationalId" : "setBankAccount");
  return (
    <Modal opened={opened} onClose={onClose} title={title} fullScreen={phone}>
      {opened && <SetSealedForm person={person} kind={kind} onDone={onClose} />}
    </Modal>
  );
};

const SetSealedForm = ({ person, kind, onDone }: { person: StaffPerson; kind: SealedKind; onDone: () => void }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const national = kind === "nationalId";
  const form = useForm<SealedFormValues>({
    initialValues: { kind: null, value: "", country: "" },
    validate: {
      kind: (value) => (value ? null : t("kindRequired")),
      value: (value) => (value.trim() ? null : t("valueRequired")),
      country: (value, values) =>
        national && values.kind === "foreign" && !/^[A-Za-z]{2}$/.test(value.trim()) ? t("countryInvalid") : null,
    },
  });

  const save = useMutation({
    mutationFn: (values: SealedFormValues) =>
      national
        ? setNationalId(person.id, {
            kind: values.kind as StaffNationalIdKind,
            value: values.value,
            ...(values.kind === "foreign" ? { country: values.country.trim().toUpperCase() } : {}),
          })
        : setBankAccount(person.id, { kind: values.kind as StaffBankAccountKind, value: values.value }),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t(national ? "nationalIdSaved" : "bankAccountSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error) => {
      const { fields, rest } = fieldRefusals(error, ["kind", "value", "country"]);
      form.setErrors(fields);
      if (rest.length > 0) notifications.show({ color: "red", title: t("couldNotSave"), message: rest.join(" ") });
    },
  });

  const kinds = (national ? nationalIdKinds : bankAccountKinds).map((value) => ({
    value,
    label: t(`${national ? "nationalIdKind" : "bankAccountKind"}_${value}`),
  }));

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Text size="sm" c="dimmed">
          {t("sealedExplained")}
        </Text>
        <Select label={t("kind")} withAsterisk allowDeselect={false} data={kinds} {...form.getInputProps("kind")} />
        <TextInput
          label={t(national ? "nationalIdValue" : "bankAccountValue")}
          description={t(national ? "nationalIdValueDescription" : "bankAccountValueDescription")}
          withAsterisk
          autoComplete="off"
          spellCheck={false}
          {...form.getInputProps("value")}
        />
        {national && form.values.kind === "foreign" && (
          <TextInput
            label={t("country")}
            description={t("issuingCountryDescription")}
            withAsterisk
            {...form.getInputProps("country")}
          />
        )}
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("save")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
