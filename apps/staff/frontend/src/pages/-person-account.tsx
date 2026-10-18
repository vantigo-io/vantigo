import { Alert, Anchor, Badge, Button, Group, Radio, Stack, Switch, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconPlayerPlay, IconUserOff } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { appUrl, ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  disablePersonAccount,
  personAccountQueryOptions,
  runPersonAccount,
  type StaffAccountEmailKind,
  type StaffAccountLink,
  type StaffPersonAccount,
  updatePersonAccount,
} from "../api/accounts";
import type { StaffPerson } from "../api/people";
import { FieldGroup, Fields } from "../components/field-group";
import { staffCatalog } from "../i18n";
import { refusalText } from "../lib/absence";
import { useAccountModeText, WORKSPACE_USERS_PATH } from "../lib/accounts";
import { isRevisionConflict } from "../lib/errors";
import { useStaffFormat } from "../lib/format";

/** The reasons for which only an Owner, adopting the user for Staff, lets Staff manage them. */
const adoptionReasons = ["account_not_managed", "employee_number_taken"];

/** Disable now's refusals, each said with the flag already off (the owner's answer 2). */
const disableRefusals = ["account_not_managed", "account_privileged", "account_mismatch"];

const codeOf = (error: unknown): string | undefined => {
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

/**
 * The person's Account tab (delivery C, design S22, S27): the authority mode
 * in words, the account flag with *Account from* — the day derived from the
 * employments explained when HR set none — and the address it uses, warned
 * when the person does not have it; the link the accounts worker drives,
 * its state, its reason and its invitation in words, and the next attempt;
 * *Run now* and *Disable now* (which also turns the flag off — the owner's
 * answer 2) for `staff:manage`; and for an existing user Staff does not
 * manage, the Owner's adoption step on identity's users page.
 *
 * The person reads it, read-only. An erased record offers HR *Disable now*
 * alone — the server takes it there, since the access must end too — and
 * nothing it would refuse with `person_erased`.
 */
export const PersonAccount = ({
  person,
  timeZone,
  erased = false,
}: {
  person: StaffPerson;
  timeZone?: string;
  erased?: boolean;
}) => {
  const { t } = useI18n("staff");
  const account = useQuery(personAccountQueryOptions(person.id));
  const canManage = person.capabilities.canManageAccount;

  if (account.isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadAccount")}>
        {account.error.message}
      </Alert>
    );
  if (!account.data) return <ContentSkeleton rows={4} rowHeight={44} />;
  const data = account.data;
  return (
    <Stack gap="lg">
      <FieldGroup title={t("accountFlag")} description={t("accountFlagExplained")}>
        {canManage && !erased ? (
          // A new revision is a fresh form: a save, a disable or a conflict's reload.
          <AccountFlagForm key={data.revision} person={person} account={data} />
        ) : (
          <AccountFlagFields person={person} account={data} />
        )}
      </FieldGroup>
      <AccountLinkGroup person={person} account={data} timeZone={timeZone} canManage={canManage} erased={erased} />
    </Stack>
  );
};

/** Whether the person has the address `kind` names: the server's word for the saved kind, the record's for another. */
const hasAddress = (person: StaffPerson, account: StaffPersonAccount, kind: StaffAccountEmailKind) =>
  kind === account.address.kind
    ? account.address.present
    : Boolean(kind === "work" ? person.workEmail : person.privateEmail);

const AddressWarning = ({ person, kind }: { person: StaffPerson; kind: StaffAccountEmailKind }) => {
  const { t } = useI18n("staff");
  return (
    <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
      {t(kind === "work" ? "accountNoWorkEmail" : "accountNoPrivateEmail", { name: person.displayName })}
    </Alert>
  );
};

const AccountFlagFields = ({ person, account }: { person: StaffPerson; account: StaffPersonAccount }) => {
  const { t, date } = useStaffFormat();
  const from = account.provisionFromEffective
    ? account.provisionFrom
      ? date(account.provisionFromEffective)
      : t("accountFromDerived", { date: date(account.provisionFromEffective) })
    : t("accountFromNone");
  return (
    <Stack gap="sm">
      <Fields
        items={[
          [t("provisionAccount"), account.provisionAccount ? t("yes") : t("no")],
          [t("accountFrom"), from],
          [t("accountAddress"), t(`accountEmailKind_${account.accountEmailKind}`)],
        ]}
      />
      {account.provisionAccount && !account.address.present && (
        <AddressWarning person={person} kind={account.accountEmailKind} />
      )}
    </Stack>
  );
};

interface FlagValues {
  provisionAccount: boolean;
  provisionFrom: string | null;
  accountEmailKind: StaffAccountEmailKind;
}

/** HR's full replace of the flag's three fields, guarded by the flag's own revision. */
const AccountFlagForm = ({ person, account }: { person: StaffPerson; account: StaffPersonAccount }) => {
  const { t, date } = useStaffFormat();
  const queryClient = useQueryClient();
  const queryKey = personAccountQueryOptions(person.id).queryKey;
  const form = useForm<FlagValues>({
    initialValues: {
      provisionAccount: account.provisionAccount,
      provisionFrom: account.provisionFrom ?? null,
      accountEmailKind: account.accountEmailKind,
    },
  });

  const save = useMutation({
    mutationFn: (values: FlagValues) =>
      updatePersonAccount(
        person.id,
        {
          provisionAccount: values.provisionAccount,
          provisionFrom: values.provisionFrom || null,
          accountEmailKind: values.accountEmailKind,
        },
        account.revision,
      ),
    onSuccess: (answer) => {
      queryClient.setQueryData(queryKey, answer);
      notifications.show({ color: "teal", title: t("accountFlagSaved"), message: "" });
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey });
        return;
      }
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalText(error, t) });
    },
  });

  const { provisionFrom, accountEmailKind } = form.values;
  const fromExplained = provisionFrom
    ? t("accountFromSetExplained")
    : account.provisionFrom
      ? t("accountFromDerivedLater")
      : account.provisionFromEffective
        ? t("accountFromDerivedExplained", { date: date(account.provisionFromEffective) })
        : t("accountFromNoneExplained");

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack gap="sm">
        <Switch
          label={t("provisionAccount")}
          description={t("provisionAccountDescription")}
          {...form.getInputProps("provisionAccount", { type: "checkbox" })}
        />
        <DateInput
          label={t("accountFrom")}
          description={fromExplained}
          clearable
          valueFormat={t("dateInputFormat")}
          {...form.getInputProps("provisionFrom")}
        />
        <Radio.Group label={t("accountAddress")} {...form.getInputProps("accountEmailKind")}>
          <Group gap="md" mt={4}>
            <Radio value="work" label={t("accountEmailKind_work")} />
            <Radio value="private" label={t("accountEmailKind_private")} />
          </Group>
        </Radio.Group>
        {!hasAddress(person, account, accountEmailKind) && <AddressWarning person={person} kind={accountEmailKind} />}
        <Group justify="flex-end">
          <Button type="submit" loading={save.isPending}>
            {t("saveAccountFlag")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/** The link's state in words: a disable is Staff's unless an Owner's is recorded. */
const stateText = (t: (key: string, values?: Record<string, unknown>) => string, link: StaffAccountLink) =>
  link.state === "disabled" && link.reason === "disabled_by_owner"
    ? t("accountLinkState_disabledByOwner")
    : t(`accountLinkState_${link.state}`);

const AccountLinkGroup = ({
  person,
  account,
  timeZone,
  canManage,
  erased,
}: {
  person: StaffPerson;
  account: StaffPersonAccount;
  timeZone?: string;
  canManage: boolean;
  erased: boolean;
}) => {
  const { t, formatters } = useI18n("staff");
  const modeText = useAccountModeText();
  const queryClient = useQueryClient();
  const queryKey = personAccountQueryOptions(person.id).queryKey;
  const [outcome, setOutcome] = useState<{ color: string; text: string } | null>(null);
  const link = account.link;
  const at = (instant: string) =>
    formatters.formatDate(new Date(instant), { dateStyle: "medium", timeStyle: "short", timeZone });
  // A code a later server writes is still said, by its code, rather than as a raw key.
  const reasonText = (code: string) =>
    `accountReason_${code}` in staffCatalog.en ? t(`accountReason_${code}`) : t("accountReasonUnknown", { code });

  const run = useMutation({
    mutationFn: () => runPersonAccount(person.id),
    onSuccess: (answer) => {
      queryClient.setQueryData(queryKey, answer.account);
      setOutcome({ color: "teal", text: t(answer.invitationSent ? "runNowSent" : "runNowNotSent") });
    },
    onError: (error) => setOutcome({ color: "red", text: refusalText(error, t) }),
  });

  const disable = useMutation({
    mutationFn: () => disablePersonAccount(person.id),
    onSuccess: (answer) => {
      queryClient.setQueryData(queryKey, answer);
      setOutcome({ color: "teal", text: t("disableNowDone") });
    },
    onError: async (error) => {
      const code = codeOf(error);
      setOutcome({
        color: code === "accounts_busy" ? "yellow" : "red",
        text: code === "accounts_unavailable" ? t("disableUnavailable") : refusalText(error, t),
      });
      // The flag is off whatever identity answered, unless the worker held the link.
      if (code && (disableRefusals.includes(code) || code === "accounts_unavailable"))
        await queryClient.invalidateQueries({ queryKey });
    },
  });

  const confirmDisable = () =>
    modals.openConfirmModal({
      title: t("disableNowTitle", { name: person.displayName }),
      children: <Text size="sm">{t("disableNowExplained")}</Text>,
      labels: { confirm: t("disableNow"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => {
        setOutcome(null);
        disable.mutate();
      },
    });

  const adopt =
    canManage &&
    link !== undefined &&
    ((!link.managed && link.user !== undefined) ||
      (link.reason !== undefined && adoptionReasons.includes(link.reason)));
  const canRun = canManage && !erased && account.provisionAccount;
  const canDisable = canManage && (account.provisionAccount || (link?.user !== undefined && link.state !== "disabled"));
  const invitation = link?.invitation;

  return (
    <FieldGroup title={t("userAccount")} description={modeText(account.mode)}>
      {link ? (
        <>
          <Fields
            items={[
              [t("accountState"), stateText(t, link)],
              [
                t("user"),
                link.user && (
                  <Group gap="xs" component="span">
                    <span>{link.user.displayName}</span>
                    {!link.user.active && (
                      <Badge color="gray" variant="light">
                        {t("userDisabled")}
                      </Badge>
                    )}
                  </Group>
                ),
              ],
              [t("managedByStaff"), link.managed ? t("yes") : t("no")],
              [
                t("accountInvitation"),
                invitation &&
                  `${t(`accountInvitation_${invitation.state}`)} · ${t("invitationsIssued", { count: invitation.issued })}`,
              ],
              [t("nextAttempt"), link.nextAttemptAt && at(link.nextAttemptAt)],
              [t("lastSynced"), link.lastSyncedAt && at(link.lastSyncedAt)],
            ]}
          />
          {link.reason && <Text size="sm">{reasonText(link.reason)}</Text>}
          {link.state === "failed" && link.attempts > 0 && (
            <Text size="sm" c="red">
              {t("accountFailing", { count: link.attempts })}
            </Text>
          )}
        </>
      ) : (
        <Text size="sm" c="dimmed">
          {t("accountNoUser")}
        </Text>
      )}
      {adopt && (
        <Stack gap={4}>
          <Text size="sm">{t("accountAdoptExplained", { number: person.employeeNumber })}</Text>
          <Anchor size="sm" href={appUrl(WORKSPACE_USERS_PATH)}>
            {t("openUsers")}
          </Anchor>
        </Stack>
      )}
      {(canRun || canDisable) && (
        <Group gap="xs" wrap="wrap">
          {canRun && (
            <Button
              variant="light"
              leftSection={<IconPlayerPlay size={16} />}
              loading={run.isPending}
              onClick={() => {
                setOutcome(null);
                run.mutate();
              }}
            >
              {t("runNow")}
            </Button>
          )}
          {canDisable && (
            <Button
              variant="light"
              color="red"
              leftSection={<IconUserOff size={16} />}
              loading={disable.isPending}
              onClick={confirmDisable}
            >
              {t("disableNow")}
            </Button>
          )}
        </Group>
      )}
      {canRun && (
        <Text size="xs" c="dimmed">
          {t("runNowExplained")}
        </Text>
      )}
      {outcome && (
        <Alert color={outcome.color} role={outcome.color === "teal" ? "status" : "alert"}>
          {outcome.text}
        </Alert>
      )}
    </FieldGroup>
  );
};
