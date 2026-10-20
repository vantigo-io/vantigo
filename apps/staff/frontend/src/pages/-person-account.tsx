import {
  Alert,
  Anchor,
  Badge,
  Button,
  Card,
  CopyButton,
  Group,
  Modal,
  Radio,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconAlertTriangle, IconKey, IconPlayerPlay, IconUserOff } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { appUrl, ContentSkeleton, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import {
  disablePersonAccount,
  personAccountQueryOptions,
  revealAccountPass,
  runPersonAccount,
  type StaffAccountEmailKind,
  type StaffAccountKind,
  type StaffAccountLink,
  type StaffAccountPass,
  type StaffDirectoryField,
  type StaffPersonAccount,
  type StaffProviderLink,
  updatePersonAccount,
} from "../api/accounts";
import type { StaffPersonGroup } from "../api/groups";
import { type StaffMeta, staffMetaQueryOptions } from "../api/meta";
import type { StaffPerson } from "../api/people";
import { ApiConflictError, ApiValidationError } from "../api/request";
import { FieldGroup, Fields } from "../components/field-group";
import { staffCatalog } from "../i18n";
import { refusalText } from "../lib/absence";
import { accountKindsOffered, isProviderKind, useAccountKindText, WORKSPACE_USERS_PATH } from "../lib/accounts";
import { isRevisionConflict } from "../lib/errors";
import { useStaffFormat } from "../lib/format";
import { groupLinkOptions } from "../lib/routes";

/** The reasons for which only an Owner, adopting the user for Staff, lets Staff manage them. */
const adoptionReasons = ["account_not_managed", "employee_number_taken"];

const codeOf = (error: unknown): string | undefined => {
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

type Translate = (key: string, values?: Record<string, unknown>) => string;

/**
 * A code in words: a provider link's own wording first (`providerReason_`),
 * then the accounts' (`accountReason_`); a licence's code carries its SKU
 * after a colon, said after the words. A code a later server writes is
 * still said, by its code, rather than as a raw key.
 */
const reasonWords = (t: Translate, code: string, provider = false) => {
  const [bare, detail] = code.split(/:(.*)/s);
  const key =
    provider && `providerReason_${bare}` in staffCatalog.en
      ? `providerReason_${bare}`
      : `accountReason_${bare}` in staffCatalog.en
        ? `accountReason_${bare}`
        : undefined;
  if (!key) return t("accountReasonUnknown", { code });
  // A leaver's step that failed names the step in words, or by its code
  // when a later server names one this catalogue does not know.
  if (bare === "leaver_step_failed" && detail)
    return t("accountReason_leaver_step_failed_step", {
      step: `leaverStep_${detail}` in staffCatalog.en ? t(`leaverStep_${detail}`) : detail,
    });
  return detail ? `${t(key)} (${detail})` : t(key);
};

/**
 * A refusal in words; the provider's refusal (`provider_refused`) with the
 * provider's code, which the problem's detail carries, in words after it.
 */
const accountRefusalText = (error: unknown, t: Translate) => {
  const words = refusalText(error, t);
  if (codeOf(error) !== "provider_refused" || !(error instanceof ApiConflictError)) return words;
  const code = [...(error.detail ?? "").matchAll(/[a-z]+(?:_[a-z]+)+/g)]
    .map((match) => match[0])
    .find((one) => `accountReason_${one}` in staffCatalog.en || `providerReason_${one}` in staffCatalog.en);
  return code ? `${words} ${reasonWords(t, code, true)}` : words;
};

/** The provider's name as `/meta` answers it, else the kind's own name, else its key. */
const providerName = (t: Translate, key: string, providers: StaffMeta["providers"]) =>
  providers?.find((provider) => provider.key === key)?.name ??
  (`accountKindChoice_${key}` in staffCatalog.en ? t(`accountKindChoice_${key}`) : key);

/**
 * The person's Account tab (delivery C, design S22, S27; Entra B, D5, D7,
 * D8): the account kind in words, the account flag with *Account from* —
 * the day derived from the employments explained when HR set none — the
 * address it uses, warned when the person does not have it, and the kind,
 * `entra` offered only while `/meta` lists its provider; the links side by
 * side — Vantigo's, the accounts worker's link with identity, its state,
 * reason and invitation in words, and the provider's for a provider kind,
 * its state, UPN, reason and reported codes in words, with *Show the pass*
 * opening the pass once; *Run now* and *Disable now* (which also turns the
 * flag off — the owner's answer 2) for `staff:manage`; for an existing user
 * Staff does not manage, the Owner's adoption step on identity's users
 * page; and the directory profile, read-only.
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
  // The page has read it already; its providers are staff:manage's alone.
  const providers = useQuery(staffMetaQueryOptions()).data?.providers;
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
          <AccountFlagForm key={data.revision} person={person} account={data} providers={providers} />
        ) : (
          <AccountFlagFields person={person} account={data} />
        )}
      </FieldGroup>
      <AccountLinkGroup
        person={person}
        account={data}
        providers={providers}
        timeZone={timeZone}
        canManage={canManage}
        erased={erased}
      />
      {data.groups && <PersonGroups groups={data.groups} canOpen={canManage} />}
      {data.directoryProfile && <DirectoryProfileTable fields={data.directoryProfile} />}
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
          [t("accountKindChoice"), kindChoice(t, account.accountKind)],
        ]}
      />
      {account.provisionAccount && !account.address.present && (
        <AddressWarning person={person} kind={account.accountEmailKind} />
      )}
    </Stack>
  );
};

/** A kind's short name, as the select offers it. */
const kindChoice = (t: Translate, kind: StaffAccountKind) =>
  `accountKindChoice_${kind}` in staffCatalog.en ? t(`accountKindChoice_${kind}`) : kind;

interface FlagValues {
  provisionAccount: boolean;
  provisionFrom: string | null;
  accountEmailKind: StaffAccountEmailKind;
  accountKind: StaffAccountKind;
}

/**
 * HR's full replace of the flag's fields, guarded by the flag's own
 * revision. The kind is sent only when HR changed it: left out, the server
 * keeps it, so a provider gone from `/meta` never refuses a change of date.
 */
const AccountFlagForm = ({
  person,
  account,
  providers,
}: {
  person: StaffPerson;
  account: StaffPersonAccount;
  providers: StaffMeta["providers"];
}) => {
  const { t, date } = useStaffFormat();
  const queryClient = useQueryClient();
  const queryKey = personAccountQueryOptions(person.id).queryKey;
  const form = useForm<FlagValues>({
    initialValues: {
      provisionAccount: account.provisionAccount,
      provisionFrom: account.provisionFrom ?? null,
      accountEmailKind: account.accountEmailKind,
      accountKind: account.accountKind,
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
          ...(values.accountKind === account.accountKind ? {} : { accountKind: values.accountKind }),
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
      // The kind's two refusals are said on the select: leaving entra while
      // its link holds an account, and a provider no enabled module offers
      // (a field error, whose code the client does not keep).
      if (codeOf(error) === "account_kind_locked") {
        form.setFieldError("accountKind", t("refusal.account_kind_locked"));
        return;
      }
      if (error instanceof ApiValidationError && error.fieldErrors.accountKind) {
        form.setFieldError("accountKind", t("refusal.provider_missing"));
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
        <Select
          label={t("accountKindChoice")}
          description={t("accountKindChoiceDescription")}
          allowDeselect={false}
          data={accountKindsOffered(providers, account.accountKind).map((kind) => ({
            value: kind,
            label: kindChoice(t, kind),
          }))}
          {...form.getInputProps("accountKind")}
        />
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

/** A small titled card inside the User account group: one link of the person's. */
const LinkCard = ({ title, children }: { title: string; children: ReactNode }) => (
  <Card withBorder radius="md" padding="md" component="section" aria-label={title}>
    <Stack gap="sm">
      <Title order={4} size="h5">
        {title}
      </Title>
      {children}
    </Stack>
  </Card>
);

const AccountLinkGroup = ({
  person,
  account,
  providers,
  timeZone,
  canManage,
  erased,
}: {
  person: StaffPerson;
  account: StaffPersonAccount;
  providers: StaffMeta["providers"];
  timeZone?: string;
  canManage: boolean;
  erased: boolean;
}) => {
  const { t, formatters } = useI18n("staff");
  const kindText = useAccountKindText();
  const queryClient = useQueryClient();
  const queryKey = personAccountQueryOptions(person.id).queryKey;
  const [outcome, setOutcome] = useState<{ color: string; text: string } | null>(null);
  const link = account.link;
  const providerLink = account.providerLink;
  const providerKind = isProviderKind(account.accountKind);
  const at = (instant: string) =>
    formatters.formatDate(new Date(instant), { dateStyle: "medium", timeStyle: "short", timeZone });
  const reasonText = (code: string) => reasonWords(t, code);

  const run = useMutation({
    mutationFn: () => runPersonAccount(person.id),
    onSuccess: (answer) => {
      queryClient.setQueryData(queryKey, answer.account);
      setOutcome({
        color: "teal",
        text: t(answer.invitationSent ? "runNowSent" : providerKind ? "runNowNewPassDone" : "runNowNotSent"),
      });
    },
    onError: async (error) => {
      setOutcome({
        color: codeOf(error) === "accounts_busy" ? "yellow" : "red",
        text: accountRefusalText(error, t),
      });
      await queryClient.invalidateQueries({ queryKey });
    },
  });

  const disable = useMutation({
    mutationFn: () => disablePersonAccount(person.id),
    onSuccess: (answer) => {
      queryClient.setQueryData(queryKey, answer);
      const userDisabled = Boolean(answer.link?.user);
      const directoryDisabled = answer.providerLink?.state === "disabled" || providerLink?.state === "enabled";
      const done = userDisabled
        ? directoryDisabled
          ? "disableNowBothDone"
          : "disableNowDone"
        : directoryDisabled
          ? "disableNowDirectoryDone"
          : "disableNowNoAccount";
      setOutcome({ color: "teal", text: t(done) });
    },
    onError: async (error) => {
      const code = codeOf(error);
      setOutcome({
        color: code === "accounts_busy" ? "yellow" : "red",
        text: code === "accounts_unavailable" ? t("disableUnavailable") : accountRefusalText(error, t),
      });
      // The flag is off whatever identity answered, unless the worker held
      // the link — and even then it may have been written: read it again.
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

  // An Owner's adoption is the way for Staff to manage an existing user:
  // offered for HR's hand link once HR wants an account, and for a reason
  // only adoption clears; never on an erased record, whose access only ends.
  const adopt =
    canManage &&
    !erased &&
    link !== undefined &&
    ((link.state === "linked" && !link.managed && link.user !== undefined && account.provisionAccount) ||
      (link.reason !== undefined && adoptionReasons.includes(link.reason)));
  const canRun = canManage && !erased && account.provisionAccount;
  // Never on HR's self-service link, which is not Staff's to disable.
  // Disable now acts on both links: also for a provider's account still enabled.
  const canDisable =
    canManage &&
    link?.state !== "linked" &&
    (account.provisionAccount ||
      (link?.managed === true && link.user !== undefined && link.state !== "disabled") ||
      providerLink?.state === "enabled");
  const invitation = link?.invitation;

  const vantigo = (
    <>
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
    </>
  );

  return (
    <FieldGroup title={t("userAccount")} description={kindText(account.accountKind)}>
      {providerKind || providerLink ? (
        <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
          <LinkCard title={t("vantigoAccount")}>{vantigo}</LinkCard>
          <LinkCard title={providerName(t, providerLink?.provider ?? account.accountKind, providers)}>
            <ProviderLinkFields
              person={person}
              link={providerLink}
              timeZone={timeZone}
              canReveal={canManage && !erased}
            />
          </LinkCard>
        </SimpleGrid>
      ) : (
        vantigo
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
              {t(providerKind ? "runNowNewPass" : "runNow")}
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
          {t(providerKind ? "runNowNewPassExplained" : "runNowExplained")}
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

/**
 * The provider link (Entra design D7): its state, the UPN the directory
 * answered, since when it is enabled, the pass not yet shown and the
 * announcement, the reason and the reported codes in words — and *Show the
 * pass*, which asks the server for it once and shows it in a dialog that
 * never asks again: closed, the pass is gone from the page.
 */
const ProviderLinkFields = ({
  person,
  link,
  timeZone,
  canReveal,
}: {
  person: StaffPerson;
  link: StaffProviderLink | undefined;
  timeZone?: string;
  canReveal: boolean;
}) => {
  const { t, formatters } = useI18n("staff");
  const queryClient = useQueryClient();
  const queryKey = personAccountQueryOptions(person.id).queryKey;
  const [pass, setPass] = useState<StaffAccountPass | null>(null);
  const [refusal, setRefusal] = useState<string | null>(null);
  const at = (instant: string) =>
    formatters.formatDate(new Date(instant), { dateStyle: "medium", timeStyle: "short", timeZone });

  // The pass is never cached: it lives in this component's state until the
  // dialog closes, and the account is marked as holding none, without a refetch.
  const reveal = useMutation({
    mutationFn: () => revealAccountPass(person.id),
    onSuccess: (answer) => {
      setPass(answer);
      queryClient.setQueryData<StaffPersonAccount>(queryKey, (current) => {
        if (!current?.providerLink) return current;
        const shown: StaffProviderLink = { ...current.providerLink, hasPass: false };
        delete shown.passIssuedAt;
        return { ...current, providerLink: shown };
      });
    },
    onError: async (error) => {
      setRefusal(refusalText(error, t));
      await queryClient.invalidateQueries({ queryKey });
    },
  });

  if (!link)
    return (
      <Text size="sm" c="dimmed">
        {t("providerNoLink")}
      </Text>
    );
  const stateKey = `providerLinkState_${link.state}`;
  return (
    <>
      <Fields
        items={[
          [t("accountState"), stateKey in staffCatalog.en ? t(stateKey) : link.state],
          [t("userPrincipalName"), link.externalUpn],
          [t("enabledSince"), link.state === "enabled" && link.lastSyncedAt && at(link.lastSyncedAt)],
          [t("lastSynced"), link.state !== "enabled" && link.lastSyncedAt && at(link.lastSyncedAt)],
          [t("passIssued"), link.hasPass && link.passIssuedAt && at(link.passIssuedAt)],
          [t("accountAnnounced"), link.announcedAt && at(link.announcedAt)],
          [t("nextAttempt"), link.nextAttemptAt && at(link.nextAttemptAt)],
        ]}
      />
      {link.reason && <Text size="sm">{reasonWords(t, link.reason, true)}</Text>}
      {link.reportedCodes.map((code) => (
        <Text size="sm" key={code}>
          {reasonWords(t, code, true)}
        </Text>
      ))}
      {link.state === "failed" && link.attempts > 0 && (
        <Text size="sm" c="red">
          {t("providerFailing", { count: link.attempts })}
        </Text>
      )}
      {canReveal && link.hasPass && (
        <Group>
          <Button
            variant="light"
            leftSection={<IconKey size={16} />}
            loading={reveal.isPending}
            onClick={() => {
              setRefusal(null);
              reveal.mutate();
            }}
          >
            {t("showPass")}
          </Button>
        </Group>
      )}
      {refusal && (
        <Alert color="red" role="alert">
          {refusal}
        </Alert>
      )}
      <Modal opened={pass !== null} onClose={() => setPass(null)} title={t("passTitle")} centered>
        {pass && (
          <Stack gap="sm">
            <Text size="sm">{t("passExplained", { name: person.displayName })}</Text>
            <Text ff="monospace" size="xl" fw={600} style={{ overflowWrap: "anywhere" }}>
              {pass.pass}
            </Text>
            <Text size="sm">{t("passExpires", { date: at(pass.expiresAt) })}</Text>
            <Text size="sm" fw={500}>
              {t("passShownOnce")}
            </Text>
            <Group justify="flex-end">
              <CopyButton value={pass.pass}>
                {({ copied, copy }) => (
                  <Button variant="light" onClick={copy}>
                    {t(copied ? "copied" : "copy")}
                  </Button>
                )}
              </CopyButton>
              <Button onClick={() => setPass(null)}>{t("close")}</Button>
            </Group>
          </Stack>
        )}
      </Modal>
    </>
  );
};

/**
 * The directory profile (Entra design D8), read-only: every field the
 * provider writes on the person's external account, in the server's order,
 * with the value it carries today. A field that is always written is greyed;
 * a field the installation switched off is marked, since the provider clears it.
 */
const DirectoryProfileTable = ({ fields }: { fields: StaffDirectoryField[] }) => {
  const { t } = useI18n("staff");
  const label = (key: string) => (`directoryField_${key}` in staffCatalog.en ? t(`directoryField_${key}`) : key);
  return (
    <FieldGroup title={t("directoryProfile")} description={t("directoryProfilePersonDescription")}>
      <Table.ScrollContainer minWidth={480}>
        <Table aria-label={t("directoryProfile")}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("directoryField")}</Table.Th>
              <Table.Th>{t("directoryWritten")}</Table.Th>
              <Table.Th>{t("directoryValueToday")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {fields.map((field) => (
              <Table.Tr key={field.key} c={field.switchable ? undefined : "dimmed"}>
                <Table.Td>{label(field.key)}</Table.Td>
                <Table.Td>
                  {!field.switchable ? (
                    t("directoryAlways")
                  ) : field.on ? (
                    t("directoryOn")
                  ) : (
                    <Badge color="gray" variant="light">
                      {t("directoryOff")}
                    </Badge>
                  )}
                </Table.Td>
                <Table.Td style={{ overflowWrap: "anywhere" }}>{field.value ?? "—"}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
    </FieldGroup>
  );
};

/**
 * The groups the person is in (Entra design D9), archived groups aside: how
 * each came in and since when — the groups a provider makes them a member
 * of, and whose licences they get. HR opens a group from here.
 */
const PersonGroups = ({ groups, canOpen }: { groups: StaffPersonGroup[]; canOpen: boolean }) => {
  const { t, date } = useStaffFormat();
  return (
    <FieldGroup title={t("personGroups")} description={t("personGroupsDescription")}>
      {groups.length === 0 ? (
        <Text size="sm" c="dimmed">
          {t("personNoGroups")}
        </Text>
      ) : (
        <Table.ScrollContainer minWidth={420}>
          <Table aria-label={t("personGroups")}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("name")}</Table.Th>
                <Table.Th>{t("groupSource")}</Table.Th>
                <Table.Th>{t("groupSince")}</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {groups.map((group) => (
                <Table.Tr key={group.id}>
                  <Table.Td>
                    {canOpen ? (
                      <Anchor size="sm" renderRoot={(props) => <Link {...groupLinkOptions(group.id)} {...props} />}>
                        {group.name}
                      </Anchor>
                    ) : (
                      group.name
                    )}
                  </Table.Td>
                  <Table.Td>{group.source ? t(`groupSource_${group.source}`) : "—"}</Table.Td>
                  <Table.Td>{group.since ? date(group.since) : "—"}</Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      )}
    </FieldGroup>
  );
};
