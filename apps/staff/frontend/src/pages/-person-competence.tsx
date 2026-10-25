import {
  Alert,
  Badge,
  Box,
  Button,
  Card,
  Checkbox,
  FileButton,
  Group,
  Modal,
  Radio,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconCamera, IconDownload, IconFileUpload, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useRef, useState } from "react";
import {
  changeCompetence,
  competenceCategoriesQueryOptions,
  competencesQueryOptions,
  declareCompetence,
  downloadEvidence,
  personCompetencesQueryOptions,
  removeCompetence,
  renewCompetence,
  type StaffCompetence,
  type StaffCompetenceCategory,
  type StaffPersonCompetence,
  uploadEvidence,
  verifyCompetence,
} from "../api/competences";
import { saveFile } from "../api/documents";
import type { StaffPerson } from "../api/people";
import { ApiValidationError, STAFF_QUERY_KEY } from "../api/request";
import { FieldGroup, Fields } from "../components/field-group";
import "../i18n";
import { refusalText } from "../lib/absence";
import {
  basisLabel,
  competenceBases,
  competenceName,
  daysUntil,
  levelLabel,
  levelOptions,
  offeredCompetences,
  proposedExpiry,
  sourceLabel,
} from "../lib/competences";
import { DOCUMENT_ACCEPT, fileRefusal, placeDocumentRefusal } from "../lib/documents";
import { fieldRefusals, isRevisionConflict } from "../lib/errors";
import { useStaffFormat } from "../lib/format";
import { PersonProfileGaps } from "./-person-profile-gaps";
import { PersonReviews, PersonWishes } from "./-person-wishes";

type Translate = (key: string, values?: Record<string, unknown>) => string;

/** The field of a form each coded refusal is about. */
const refusalFields: Record<string, string> = {
  competence_held: "competenceId",
  competence_unavailable: "competenceId",
  level_required: "level",
  level_not_allowed: "level",
};

/**
 * A refused write placed on the form: a field error on the field the form
 * shows — in the catalog's words when it is coded — and the rest said out loud.
 */
const placeRefusal = (
  error: Error,
  shown: readonly string[],
  t: Translate,
): { fields: Record<string, string>; rest: string[] } => {
  const code = (error as { code?: string }).code;
  const { fields, rest } = fieldRefusals(error, shown);
  const field = code ? refusalFields[code] : undefined;
  if (field && shown.includes(field)) return { fields: { ...fields, [field]: refusalText(error, t) }, rest: [] };
  if (!(error instanceof ApiValidationError) || code) return { fields, rest: [refusalText(error, t)] };
  return { fields, rest };
};

type Dialog =
  | { kind: "add" }
  | { kind: "edit"; row: StaffPersonCompetence }
  | { kind: "verify"; row: StaffPersonCompetence }
  | { kind: "renew"; row: StaffPersonCompetence }
  | { kind: "evidence"; row: StaffPersonCompetence };

/**
 * The Competence tab (staff competence design §7), on My page and on the
 * person page alike: the person's competences grouped by category, each
 * with its level, whether it is declared or verified, its validity and its
 * evidence, and — as the person's capabilities allow — Add competence,
 * Edit, Verify, Renewed, Add evidence and Remove. What a row carries is what
 * the server let this reader see: a colleague reads no detail and does
 * nothing. History is behind a switch. Laid out for a phone first.
 */
export const PersonCompetence = ({
  person,
  self,
  today,
  timeZone,
  addOnArrival = false,
  onArrivalDone,
}: {
  person: StaffPerson;
  /** The caller is the person — My page, or their own person page. */
  self: boolean;
  /** The business zone's day a validity is judged on. */
  today: string;
  /** The business time zone, the day a verification fell on is told in. */
  timeZone?: string;
  /** Opens Add competence on arrival — the host spotlight's Add competence. */
  addOnArrival?: boolean;
  /** Called when the form opened on arrival closes, so the URL forgets it. */
  onArrivalDone?: () => void;
}) => {
  const { t, locale } = useI18n("staff");
  const queryClient = useQueryClient();
  const [history, setHistory] = useState(false);
  const [dialog, setDialog] = useState<Dialog | null>(addOnArrival ? { kind: "add" } : null);
  const rows = useQuery(personCompetencesQueryOptions(person.id, history));
  const catalogue = useQuery(competencesQueryOptions());
  const categories = useQuery(competenceCategoriesQueryOptions());
  const phone = useMediaQuery("(max-width: 48em)");

  const caps = person.capabilities;
  const verifier = caps.canVerifyCompetence;
  const hr = caps.canEdit;
  const canAdd = self || verifier || hr;
  // Self first, as the server judges: on their own record HR and a verifier
  // are the person, who changes and removes their own declarations alone.
  const writesAny = !self && (verifier || hr);
  // Who files evidence, as the upload is judged: the person, HR and
  // staff:personal-data — never a manager or an Approver as such.
  const filesEvidence = self || hr || caps.canSeeHealth;
  const nameOf = (row: StaffPersonCompetence) => {
    const name = competenceName(row.competence, locale);
    return row.competence.archived ? t("competenceArchivedName", { name }) : name;
  };

  const remove = useMutation({
    mutationFn: ({ row, chain }: { row: StaffPersonCompetence; chain: boolean }) => removeCompetence(row.id, chain),
    onSuccess: async (_, { row }) => {
      notifications.show({ color: "teal", title: t("competenceRemoved", { name: nameOf(row) }), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: async (error, { row }) => {
      notifications.show({
        color: "red",
        title: t("couldNotRemoveCompetence", { name: nameOf(row) }),
        message: refusalText(error, t),
      });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
  });

  const confirmRemove = (row: StaffPersonCompetence) => {
    let chain = false;
    modals.openConfirmModal({
      title: t("removeCompetenceTitle", { name: nameOf(row) }),
      children: (
        <Stack gap="sm">
          <Text size="sm">{t("removeCompetenceExplained")}</Text>
          <Text size="sm">{t("removeCompetenceRenewed")}</Text>
          {hr && !self && (
            <Checkbox
              label={t("removeCompetenceChain")}
              description={t("removeCompetenceChainExplained")}
              onChange={(event) => {
                chain = event.currentTarget.checked;
              }}
            />
          )}
        </Stack>
      ),
      labels: { confirm: t("remove"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate({ row, chain }),
    });
  };

  // The groups in the categories' own order; a category the list does not name last.
  const order = new Map((categories.data ?? []).map((category, index) => [category.id, index]));
  const groups = new Map<number, StaffPersonCompetence[]>();
  for (const row of rows.data ?? [])
    groups.set(row.competence.categoryId, [...(groups.get(row.competence.categoryId) ?? []), row]);
  const grouped = [...groups.entries()].sort(
    ([a], [b]) => (order.get(a) ?? Number.MAX_SAFE_INTEGER) - (order.get(b) ?? Number.MAX_SAFE_INTEGER),
  );
  const categoryName = (id: number) => {
    const category = categories.data?.find((one) => one.id === id);
    return category ? competenceName(category, locale) : t("competenceCategoryUnknown");
  };

  const close = () => {
    const arrival = dialog?.kind === "add" && addOnArrival;
    setDialog(null);
    if (arrival) onArrivalDone?.();
  };
  const titleOf = (open: Dialog) => {
    if (open.kind === "add") return t("addCompetence");
    const name = nameOf(open.row);
    if (open.kind === "edit") return t("editRow", { name });
    if (open.kind === "verify") return t("verifyCompetenceTitle", { name });
    if (open.kind === "renew") return t("renewCompetenceTitle", { name });
    return t("addEvidenceTitle", { name });
  };
  const competenceOf = (row: StaffPersonCompetence) => catalogue.data?.find((one) => one.id === row.competence.id);

  return (
    <Stack gap="md">
      {/* What the person's profiles ask (delivery C) heads the tab, for the Profiles group's readers. */}
      <PersonProfileGaps person={person} self={self} />
      <FieldGroup
        title={t("competences")}
        description={t("competencesDescription")}
        actions={
          canAdd && (
            <Button leftSection={<IconPlus size={16} />} onClick={() => setDialog({ kind: "add" })}>
              {t("addCompetence")}
            </Button>
          )
        }
      >
        <Switch
          label={t("competenceShowHistory")}
          checked={history}
          onChange={(event) => setHistory(event.currentTarget.checked)}
        />
        {rows.isError && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadCompetences")}>
            {rows.error.message}
          </Alert>
        )}
        {rows.isPending && <ContentSkeleton rows={3} rowHeight={44} />}
        {rows.data?.length === 0 && <EmptyState title={t("noCompetences")} />}
        {grouped.map(([categoryId, members]) => (
          <Stack key={categoryId} gap="xs" component="section" aria-label={categoryName(categoryId)}>
            <Title order={4} size="h5">
              {categoryName(categoryId)}
            </Title>
            {members.map((row) => {
              const current = !row.superseded;
              const own = self && !row.verified;
              // A skill has no expiry: it is edited, never renewed.
              const renewable = row.competence.kind !== "skill";
              return (
                <CompetenceRow
                  key={row.id}
                  row={row}
                  name={nameOf(row)}
                  today={today}
                  timeZone={timeZone}
                  actions={
                    current && (
                      <>
                        {(own || writesAny) && (
                          <Button size="compact-sm" variant="light" onClick={() => setDialog({ kind: "edit", row })}>
                            {t("competenceEdit")}
                          </Button>
                        )}
                        {verifier && (
                          <Button size="compact-sm" variant="light" onClick={() => setDialog({ kind: "verify", row })}>
                            {t("competenceVerify")}
                          </Button>
                        )}
                        {renewable && (self || verifier || hr) && (
                          <Button size="compact-sm" variant="light" onClick={() => setDialog({ kind: "renew", row })}>
                            {t("competenceRenewed")}
                          </Button>
                        )}
                        {!row.evidence && filesEvidence && (
                          <Button
                            size="compact-sm"
                            variant="light"
                            onClick={() => setDialog({ kind: "evidence", row })}
                          >
                            {t("competenceAddEvidence")}
                          </Button>
                        )}
                        {(own || writesAny) && (
                          <Button size="compact-sm" variant="subtle" color="red" onClick={() => confirmRemove(row)}>
                            {t("remove")}
                          </Button>
                        )}
                      </>
                    )
                  }
                />
              );
            })}
          </Stack>
        ))}
      </FieldGroup>
      {/* What the person wants and the reviews (delivery B), each as the capabilities allow. */}
      {caps.canReadWishes && <PersonWishes person={person} self={self} timeZone={timeZone} />}
      {(verifier || self || person.lastCompetenceReview) && <PersonReviews person={person} />}
      <Modal
        opened={dialog !== null}
        onClose={close}
        title={dialog ? titleOf(dialog) : ""}
        size="lg"
        fullScreen={phone}
      >
        {dialog?.kind === "add" && (
          <AddForm
            personId={person.id}
            catalogue={catalogue.data}
            categories={categories.data}
            filesEvidence={filesEvidence}
            onDone={close}
          />
        )}
        {dialog?.kind === "edit" && (
          <EditForm row={dialog.row} competence={competenceOf(dialog.row)} name={nameOf(dialog.row)} onDone={close} />
        )}
        {dialog?.kind === "verify" && <VerifyForm row={dialog.row} name={nameOf(dialog.row)} onDone={close} />}
        {dialog?.kind === "renew" && (
          <RenewForm
            row={dialog.row}
            competence={competenceOf(dialog.row)}
            name={nameOf(dialog.row)}
            filesEvidence={filesEvidence}
            onDone={close}
          />
        )}
        {dialog?.kind === "evidence" && <EvidenceForm row={dialog.row} onDone={close} />}
      </Modal>
    </Stack>
  );
};

/** One row as the server shaped it: what it carries and nothing more. */
const CompetenceRow = ({
  row,
  name,
  today,
  timeZone,
  actions,
}: {
  row: StaffPersonCompetence;
  name: string;
  today: string;
  timeZone?: string;
  actions: ReactNode;
}) => {
  const { t, date, day } = useStaffFormat();
  const download = useMutation({
    mutationFn: (fileName: string) => downloadEvidence(row.id, fileName),
    onSuccess: saveFile,
    onError: (error, fileName) =>
      notifications.show({
        color: "red",
        title: t("couldNotDownloadDocument", { name: fileName }),
        message: error.message,
      }),
  });
  const left = row.validTo && !row.expired && !row.superseded ? daysUntil(today, row.validTo) : undefined;
  const validity = row.validTo ? t("competenceValidUntil", { date: date(row.validTo) }) : t("competenceNoExpiry");
  const daysLeft =
    left === undefined
      ? undefined
      : left === 0
        ? t("competenceExpiresToday")
        : left === 1
          ? t("competenceOneDayLeft")
          : t("competenceDaysLeft", { count: left });

  return (
    <Card
      withBorder
      radius="md"
      padding="sm"
      component="article"
      aria-label={name}
      style={row.superseded ? { opacity: 0.6 } : undefined}
    >
      <Stack gap={6}>
        <Group justify="space-between" wrap="wrap" gap="xs">
          <Text fw={500}>{name}</Text>
          <Group gap={4} wrap="wrap">
            {row.level !== undefined && <Badge variant="outline">{levelLabel(t, row.level)}</Badge>}
            {row.verified ? (
              <Badge variant="light" color="teal">
                {t("competenceVerified")}
              </Badge>
            ) : (
              <Badge variant="light" color="yellow">
                {t("competenceDeclared")}
              </Badge>
            )}
            {row.expiring && (
              <Badge variant="light" color="orange">
                {t("competenceExpiring")}
              </Badge>
            )}
            {row.expired && (
              <Badge variant="light" color="red">
                {t("competenceExpired")}
              </Badge>
            )}
            {row.superseded && (
              <Badge variant="light" color="gray">
                {t("competenceSuperseded")}
              </Badge>
            )}
          </Group>
        </Group>
        <Text size="sm">{daysLeft ? `${validity} · ${daysLeft}` : validity}</Text>
        {row.verifiedAt && (
          <Text size="sm" c="dimmed">
            {row.verifiedBy
              ? t("competenceVerifiedBy", { name: row.verifiedBy, date: day(row.verifiedAt, timeZone) })
              : t("competenceVerifiedOn", { date: day(row.verifiedAt, timeZone) })}
            {row.verifiedBasis ? ` · ${basisLabel(t, row.verifiedBasis)}` : ""}
          </Text>
        )}
        {row.verifiedNote && (
          <Text size="sm" c="dimmed" style={{ overflowWrap: "anywhere" }}>
            {row.verifiedNote}
          </Text>
        )}
        <Fields
          items={[
            [t("competenceValidFrom"), date(row.validFrom)],
            [t("competenceIssuedOn"), row.issuedOn ? date(row.issuedOn) : undefined],
            [t("competenceIssuer"), row.issuer],
            [t("competenceReference"), row.reference],
            [t("competenceSource"), row.source ? sourceLabel(t, row.source) : undefined],
            [t("competenceSeen"), row.seenAt ? day(row.seenAt, timeZone) : undefined],
            [
              t("competenceEvidence"),
              row.evidence && (
                <Button
                  size="compact-sm"
                  variant="subtle"
                  px={0}
                  leftSection={<IconDownload size={14} />}
                  aria-label={t("downloadDocument", { name: row.evidence.fileName })}
                  loading={download.isPending}
                  onClick={() => row.evidence && download.mutate(row.evidence.fileName)}
                  style={{ overflowWrap: "anywhere" }}
                >
                  {row.evidence.fileName}
                </Button>
              ),
            ],
          ]}
        />
        {actions && (
          <Group gap="xs" wrap="wrap">
            {actions}
          </Group>
        )}
      </Stack>
    </Card>
  );
};

/** The evidence file: a picker, and the camera where there is one to hand — a touch screen. */
const EvidencePicker = ({
  file,
  error,
  disabled,
  onPick,
}: {
  file: File | null;
  error?: ReactNode;
  disabled: boolean;
  onPick: (file: File) => void;
}) => {
  const { t } = useI18n("staff");
  const touch = useMediaQuery("(pointer: coarse)");
  // Cleared after each pick, so picking the same file again is a pick too.
  const resetPicker = useRef<() => void>(null);
  const resetCamera = useRef<() => void>(null);
  const take = (picked: File | null | undefined) => {
    if (!picked) return;
    onPick(picked);
    resetPicker.current?.();
    resetCamera.current?.();
  };
  return (
    <Box
      role="group"
      aria-label={t("competenceEvidenceFile")}
      p="sm"
      style={{
        border: `1px dashed var(--mantine-color-${error ? "red-6" : "gray-4"})`,
        borderRadius: "var(--mantine-radius-sm)",
      }}
    >
      <Stack gap="xs">
        <Text size="sm" fw={500}>
          {t("competenceEvidenceFile")}
        </Text>
        <Text size="sm" c="dimmed">
          {t("competenceEvidenceExplained")}
        </Text>
        <Group gap="sm" wrap="wrap">
          <FileButton
            onChange={take}
            accept={DOCUMENT_ACCEPT}
            disabled={disabled}
            resetRef={resetPicker}
            inputProps={{ "aria-label": t("chooseFile") }}
          >
            {(props) => (
              <Button {...props} variant="light" leftSection={<IconFileUpload size={16} />} disabled={disabled}>
                {t("chooseFile")}
              </Button>
            )}
          </FileButton>
          {touch && (
            <FileButton
              onChange={take}
              accept="image/*"
              capture="environment"
              disabled={disabled}
              resetRef={resetCamera}
              inputProps={{ "aria-label": t("takePhoto") }}
            >
              {(props) => (
                <Button {...props} variant="light" leftSection={<IconCamera size={16} />} disabled={disabled}>
                  {t("takePhoto")}
                </Button>
              )}
            </FileButton>
          )}
        </Group>
        {file && (
          <Text size="sm" fw={500} style={{ overflowWrap: "anywhere" }}>
            {file.name}
          </Text>
        )}
        {error && (
          <Text size="sm" c="red">
            {error}
          </Text>
        )}
      </Stack>
    </Box>
  );
};

/** Saved; the evidence, when chosen, filed as a second request — a failure leaves the row and says so. */
const useFileEvidence = () => {
  const { t } = useI18n("staff");
  return async (row: StaffPersonCompetence, file: File | null, name: string) => {
    if (!file) return;
    try {
      await uploadEvidence(row.id, file);
    } catch (error) {
      notifications.show({
        color: "red",
        title: t("evidenceNotFiled", { name }),
        message: refusalText(error, t),
      });
    }
  };
};

const optional = (value: string | null | undefined) => (value?.trim() ? value.trim() : undefined);

interface AddValues {
  competenceId: string | null;
  level: string | null;
  issuedOn: string | null;
  validTo: string | null;
  issuer: string;
  reference: string;
  file: File | null;
}

const addShown = ["competenceId", "level", "issuedOn", "validFrom", "validTo", "issuer", "reference"];

/**
 * Add competence: a searchable pick over the visible catalogue in its
 * categories, the level when it has one, the dates with the expiry proposed
 * from the validity, the issuer, the reference and the evidence.
 */
const AddForm = ({
  personId,
  catalogue,
  categories,
  filesEvidence,
  onDone,
}: {
  personId: number;
  catalogue?: StaffCompetence[];
  categories?: StaffCompetenceCategory[];
  /** The evidence picker is offered to whoever may file it alone. */
  filesEvidence: boolean;
  onDone: () => void;
}) => {
  const { t, locale } = useI18n("staff");
  const queryClient = useQueryClient();
  const fileEvidence = useFileEvidence();
  const [banner, setBanner] = useState<string | null>(null);
  // The expiry follows the issue date until the person sets it themselves.
  const [expiryTyped, setExpiryTyped] = useState(false);
  const form = useForm<AddValues>({
    initialValues: {
      competenceId: null,
      level: null,
      issuedOn: null,
      validTo: null,
      issuer: "",
      reference: "",
      file: null,
    },
    validate: {
      competenceId: (value) => (value ? null : t("competenceRequired")),
      issuer: (value) => (value.trim().length > 200 ? t("textTooLong", { max: 200 }) : null),
      reference: (value) => (value.trim().length > 100 ? t("textTooLong", { max: 100 }) : null),
      file: (file) => (file ? fileRefusal(file, t) : null),
    },
  });
  const offered = offeredCompetences(catalogue ?? []);
  const chosen = offered.find((one) => String(one.id) === form.values.competenceId);
  const data = (categories ?? [])
    .map((category) => ({
      group: competenceName(category, locale),
      items: offered
        .filter((one) => one.categoryId === category.id)
        .map((one) => ({ value: String(one.id), label: competenceName(one, locale) })),
    }))
    .filter((group) => group.items.length > 0);

  const propose = (issuedOn: string | null, competence: StaffCompetence | undefined) => {
    if (expiryTyped || competence?.kind === "skill") return;
    form.setFieldValue("validTo", proposedExpiry(issuedOn, competence?.validityMonths) ?? null);
  };

  const save = useMutation({
    mutationFn: async (values: AddValues) => {
      const created = await declareCompetence(personId, {
        competenceId: Number(values.competenceId),
        ...(values.level !== null ? { level: Number(values.level) } : {}),
        ...(values.issuedOn ? { issuedOn: values.issuedOn } : {}),
        ...(values.validTo && chosen?.kind !== "skill" ? { validTo: values.validTo } : {}),
        ...(optional(values.issuer) ? { issuer: optional(values.issuer) } : {}),
        ...(optional(values.reference) ? { reference: optional(values.reference) } : {}),
      });
      await fileEvidence(created, values.file, competenceName(created.competence, locale));
      return created;
    },
    onSuccess: async (created) => {
      notifications.show({
        color: "teal",
        title: t("competenceSaved", { name: competenceName(created.competence, locale) }),
        message: "",
      });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error) => {
      const { fields, rest } = placeRefusal(error, addShown, t);
      form.setErrors(fields);
      if (rest.length > 0) setBanner(rest.join(" "));
    },
  });

  return (
    <form
      onSubmit={form.onSubmit((values) => {
        setBanner(null);
        save.mutate(values);
      })}
    >
      <Stack>
        {banner && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {banner}
          </Alert>
        )}
        <Select
          label={t("competencePick")}
          placeholder={t("competencePickPlaceholder")}
          nothingFoundMessage={t("competenceNothingFound")}
          withAsterisk
          searchable
          allowDeselect={false}
          data={data}
          {...form.getInputProps("competenceId")}
          onChange={(value) => {
            form.setFieldValue("competenceId", value);
            const next = offered.find((one) => String(one.id) === value);
            if (!next?.hasLevel) form.setFieldValue("level", null);
            if (next?.kind === "skill") form.setFieldValue("validTo", null);
            else propose(form.values.issuedOn, next);
          }}
        />
        {chosen?.hasLevel && (
          <Select
            label={t("competenceLevel")}
            withAsterisk
            allowDeselect={false}
            data={levelOptions(t)}
            {...form.getInputProps("level")}
          />
        )}
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <DateInput
            label={t("competenceIssuedOn")}
            valueFormat={t("dateInputFormat")}
            clearable
            {...form.getInputProps("issuedOn")}
            onChange={(value) => {
              form.setFieldValue("issuedOn", value);
              propose(value, chosen);
            }}
          />
          {/* A skill has no expiry (`skill_no_validity`). */}
          {chosen?.kind !== "skill" && (
            <DateInput
              label={t("competenceValidTo")}
              description={
                chosen?.validityMonths ? t("competenceExpiryProposed", { months: chosen.validityMonths }) : undefined
              }
              valueFormat={t("dateInputFormat")}
              clearable
              {...form.getInputProps("validTo")}
              onChange={(value) => {
                setExpiryTyped(true);
                form.setFieldValue("validTo", value);
              }}
            />
          )}
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput label={t("competenceIssuer")} description={chosen?.issuerHint} {...form.getInputProps("issuer")} />
          <TextInput
            label={t("competenceReference")}
            description={t("competenceReferenceDescription")}
            {...form.getInputProps("reference")}
          />
        </SimpleGrid>
        {filesEvidence && (
          <EvidencePicker
            file={form.values.file}
            error={form.errors.file}
            disabled={save.isPending}
            onPick={(file) => {
              form.setFieldValue("file", file);
              form.setFieldError("file", fileRefusal(file, t));
            }}
          />
        )}
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone} disabled={save.isPending}>
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

interface EditValues {
  level: string | null;
  issuedOn: string | null;
  validTo: string | null;
  issuer: string;
  reference: string;
}

/** Edit: the row's level, dates, issuer and reference, replaced as a whole under the revision it was read at. */
const EditForm = ({
  row,
  competence,
  name,
  onDone,
}: {
  row: StaffPersonCompetence;
  competence?: StaffCompetence;
  name: string;
  onDone: () => void;
}) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const [banner, setBanner] = useState<string | null>(null);
  // A skill has no expiry (`skill_no_validity`): none is shown or sent.
  const skill = row.competence.kind === "skill";
  const form = useForm<EditValues>({
    initialValues: {
      level: row.level === undefined ? null : String(row.level),
      issuedOn: row.issuedOn ?? null,
      validTo: skill ? null : (row.validTo ?? null),
      issuer: row.issuer ?? "",
      reference: row.reference ?? "",
    },
    validate: {
      issuer: (value) => (value.trim().length > 200 ? t("textTooLong", { max: 200 }) : null),
      reference: (value) => (value.trim().length > 100 ? t("textTooLong", { max: 100 }) : null),
    },
  });
  const save = useMutation({
    mutationFn: (values: EditValues) =>
      changeCompetence(
        row.id,
        {
          ...(values.level !== null ? { level: Number(values.level) } : {}),
          ...(values.issuedOn ? { issuedOn: values.issuedOn } : {}),
          ...(values.validTo ? { validTo: values.validTo } : {}),
          ...(optional(values.issuer) ? { issuer: optional(values.issuer) } : {}),
          ...(optional(values.reference) ? { reference: optional(values.reference) } : {}),
        },
        row.revision,
      ),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("competenceSaved", { name }), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        onDone();
        return;
      }
      const { fields, rest } = placeRefusal(
        error,
        ["level", "issuedOn", "validFrom", "validTo", "issuer", "reference"],
        t,
      );
      form.setErrors(fields);
      if (rest.length > 0) setBanner(rest.join(" "));
    },
  });
  return (
    <form
      onSubmit={form.onSubmit((values) => {
        setBanner(null);
        save.mutate(values);
      })}
    >
      <Stack>
        {banner && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {banner}
          </Alert>
        )}
        {(row.competence.hasLevel || row.level !== undefined) && (
          <Select
            label={t("competenceLevel")}
            withAsterisk
            allowDeselect={false}
            data={levelOptions(t)}
            {...form.getInputProps("level")}
          />
        )}
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <DateInput
            label={t("competenceIssuedOn")}
            valueFormat={t("dateInputFormat")}
            clearable
            {...form.getInputProps("issuedOn")}
          />
          {!skill && (
            <DateInput
              label={t("competenceValidTo")}
              valueFormat={t("dateInputFormat")}
              clearable
              {...form.getInputProps("validTo")}
            />
          )}
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t("competenceIssuer")}
            description={competence?.issuerHint}
            {...form.getInputProps("issuer")}
          />
          <TextInput label={t("competenceReference")} {...form.getInputProps("reference")} />
        </SimpleGrid>
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone} disabled={save.isPending}>
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

interface VerifyValues {
  level: string | null;
  basis: string;
  note: string;
}

/** Verify: the level the verifier agrees to, what it rests on and a short note. */
const VerifyForm = ({ row, name, onDone }: { row: StaffPersonCompetence; name: string; onDone: () => void }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const [banner, setBanner] = useState<string | null>(null);
  const form = useForm<VerifyValues>({
    initialValues: {
      level: row.level === undefined ? null : String(row.level),
      basis: row.verifiedBasis ?? (row.evidence ? "evidence" : "observed"),
      note: row.verifiedNote ?? "",
    },
    validate: {
      note: (value) => (value.trim().length > 200 ? t("textTooLong", { max: 200 }) : null),
    },
  });
  const save = useMutation({
    mutationFn: (values: VerifyValues) =>
      verifyCompetence(row.id, {
        basis: values.basis as "evidence" | "observed",
        ...(values.level !== null ? { level: Number(values.level) } : {}),
        ...(optional(values.note) ? { note: optional(values.note) } : {}),
      }),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("competenceVerifiedSaved", { name }), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error) => {
      const { fields, rest } = placeRefusal(error, ["level", "basis", "note"], t);
      form.setErrors(fields);
      if (rest.length > 0) setBanner(rest.join(" "));
    },
  });
  return (
    <form
      onSubmit={form.onSubmit((values) => {
        setBanner(null);
        save.mutate(values);
      })}
    >
      <Stack>
        {banner && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {banner}
          </Alert>
        )}
        {row.competence.hasLevel && (
          <Select
            label={t("competenceLevel")}
            withAsterisk
            allowDeselect={false}
            data={levelOptions(t)}
            {...form.getInputProps("level")}
          />
        )}
        <Radio.Group label={t("competenceBasis")} withAsterisk {...form.getInputProps("basis")}>
          <Stack gap="xs" mt="xs">
            {competenceBases.map((basis) => (
              <Radio key={basis} value={basis} label={basisLabel(t, basis)} />
            ))}
          </Stack>
        </Radio.Group>
        <Textarea
          label={t("competenceNote")}
          description={t("competenceNoteDescription")}
          rows={3}
          maxLength={200}
          {...form.getInputProps("note")}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone} disabled={save.isPending}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("competenceVerify")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

interface RenewValues {
  issuedOn: string | null;
  validTo: string | null;
  issuer: string;
  reference: string;
  file: File | null;
}

/** Renewed: a new row with the new dates, the expiry proposed; the old row stays as history. */
const RenewForm = ({
  row,
  competence,
  name,
  filesEvidence,
  onDone,
}: {
  row: StaffPersonCompetence;
  competence?: StaffCompetence;
  name: string;
  /** The evidence picker is offered to whoever may file it alone. */
  filesEvidence: boolean;
  onDone: () => void;
}) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const fileEvidence = useFileEvidence();
  const [banner, setBanner] = useState<string | null>(null);
  const [expiryTyped, setExpiryTyped] = useState(false);
  const form = useForm<RenewValues>({
    initialValues: { issuedOn: null, validTo: null, issuer: row.issuer ?? "", reference: "", file: null },
    validate: {
      issuer: (value) => (value.trim().length > 200 ? t("textTooLong", { max: 200 }) : null),
      reference: (value) => (value.trim().length > 100 ? t("textTooLong", { max: 100 }) : null),
      file: (file) => (file ? fileRefusal(file, t) : null),
    },
  });
  const save = useMutation({
    mutationFn: async (values: RenewValues) => {
      const renewed = await renewCompetence(row.id, {
        ...(values.issuedOn ? { issuedOn: values.issuedOn } : {}),
        ...(values.validTo ? { validTo: values.validTo } : {}),
        ...(optional(values.issuer) ? { issuer: optional(values.issuer) } : {}),
        ...(optional(values.reference) ? { reference: optional(values.reference) } : {}),
      });
      await fileEvidence(renewed, values.file, name);
      return renewed;
    },
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("competenceSaved", { name }), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error) => {
      const { fields, rest } = placeRefusal(error, ["issuedOn", "validFrom", "validTo", "issuer", "reference"], t);
      form.setErrors(fields);
      if (rest.length > 0) setBanner(rest.join(" "));
    },
  });
  return (
    <form
      onSubmit={form.onSubmit((values) => {
        setBanner(null);
        save.mutate(values);
      })}
    >
      <Stack>
        {banner && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotSave")}>
            {banner}
          </Alert>
        )}
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <DateInput
            label={t("competenceIssuedOn")}
            valueFormat={t("dateInputFormat")}
            clearable
            {...form.getInputProps("issuedOn")}
            onChange={(value) => {
              form.setFieldValue("issuedOn", value);
              if (!expiryTyped)
                form.setFieldValue("validTo", proposedExpiry(value, competence?.validityMonths) ?? null);
            }}
          />
          <DateInput
            label={t("competenceValidTo")}
            description={
              competence?.validityMonths
                ? t("competenceExpiryProposed", { months: competence.validityMonths })
                : undefined
            }
            valueFormat={t("dateInputFormat")}
            clearable
            {...form.getInputProps("validTo")}
            onChange={(value) => {
              setExpiryTyped(true);
              form.setFieldValue("validTo", value);
            }}
          />
        </SimpleGrid>
        <SimpleGrid cols={{ base: 1, sm: 2 }}>
          <TextInput
            label={t("competenceIssuer")}
            description={competence?.issuerHint}
            {...form.getInputProps("issuer")}
          />
          <TextInput
            label={t("competenceReference")}
            description={t("competenceReferenceDescription")}
            {...form.getInputProps("reference")}
          />
        </SimpleGrid>
        {filesEvidence && (
          <EvidencePicker
            file={form.values.file}
            error={form.errors.file}
            disabled={save.isPending}
            onPick={(file) => {
              form.setFieldValue("file", file);
              form.setFieldError("file", fileRefusal(file, t));
            }}
          />
        )}
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone} disabled={save.isPending}>
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

/** Add evidence: one file, filed under Certificate and named on the row. */
const EvidenceForm = ({ row, onDone }: { row: StaffPersonCompetence; onDone: () => void }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const [banner, setBanner] = useState<string | null>(null);
  const form = useForm<{ file: File | null }>({
    initialValues: { file: null },
    validate: { file: (file) => (file ? fileRefusal(file, t) : t("competenceEvidenceRequired")) },
  });
  const upload = useMutation({
    mutationFn: (file: File) => uploadEvidence(row.id, file),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("evidenceFiled"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error) =>
      placeDocumentRefusal(
        error,
        t,
        (errors) => form.setErrors(errors.file ? { file: errors.file } : {}),
        (message) => setBanner(message),
      ),
  });
  return (
    <form
      onSubmit={form.onSubmit((values) => {
        setBanner(null);
        upload.mutate(values.file as File);
      })}
    >
      <Stack>
        {banner && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotUploadDocument")}>
            {banner}
          </Alert>
        )}
        <EvidencePicker
          file={form.values.file}
          error={form.errors.file}
          disabled={upload.isPending}
          onPick={(file) => {
            form.setFieldValue("file", file);
            form.setFieldError("file", fileRefusal(file, t));
          }}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onDone} disabled={upload.isPending}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={upload.isPending}>
            {t("upload")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
