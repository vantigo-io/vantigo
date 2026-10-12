import { Alert, Badge, Button, Card, Group, Modal, Stack, Text, Title } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import {
  IconAlertCircle,
  IconDoorExit,
  IconInfoCircle,
  IconPencil,
  IconPlus,
  IconRotateClockwise,
} from "@tabler/icons-react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  deleteTerms,
  personEmploymentsQueryOptions,
  reopenEmployment,
  type StaffEmployment,
  type StaffTerms,
  termsQueryOptions,
} from "../api/employments";
import type { StaffStructure } from "../api/meta";
import { orgQueryOptions, personQueryOptions, type StaffPerson, type StaffPersonSummary } from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import { ContractCheck } from "../components/contract-check";
import { Fields } from "../components/field-group";
import { TermsTimeline } from "../components/terms-timeline";
import "../i18n";
import { useStaffFormat } from "../lib/format";
import { inForceOn } from "../lib/terms";
import { EmploymentEditModal } from "./-employment-edit-modal";
import { EndEmploymentModal } from "./-end-employment-modal";
import { EmploymentForm } from "./-new-person-modal";
import { TermsFormModal, type TermsModalMode } from "./-terms-form-modal";

export interface PersonEmploymentsProps {
  person: StaffPerson;
  /** The business zone's day, from `/meta`. */
  today: string;
  structure?: StaffStructure;
  /** My page: the person reads their own terms, without HR's note. */
  asSelf?: boolean;
  /**
   * The person is erased and the caller is HR: their record is read-only
   * (the person comes without `canEdit`), but an employment may still be
   * added — the rehire, which opens the record again (design S15).
   */
  rehire?: boolean;
}

/**
 * The Employments tab (design §7): one card per employment, oldest first as
 * the server lists them, each with its terms as a timeline and — for a
 * reader of the employment group — its § 14-6 check. HR's actions are on
 * the card and the rows, and New employment above them — a second
 * position, a leaver hired again, or the first employment New person left
 * for later; on an erased record it is Rehire, the one change it takes.
 * Everyone else reads.
 */
export const PersonEmployments = ({ person, today, structure, asSelf, rehire = false }: PersonEmploymentsProps) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  const [adding, setAdding] = useState(false);
  const { data, isPending, isError, error } = useQuery(personEmploymentsQueryOptions(person.id));
  // People a terms row names as manager are named from what this page
  // already knows — the person's manager today and the org view — before a
  // read of their own record.
  const { data: org } = useQuery(orgQueryOptions(person.id));
  const known = new Map<number, StaffPersonSummary>();
  for (const one of [person.manager, org?.person, ...(org?.chain ?? []), ...(org?.reports ?? [])])
    if (one) known.set(one.id, one);

  if (isError)
    return (
      <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadEmployments")}>
        {error.message}
      </Alert>
    );
  if (isPending) return null;

  const canAdd = person.capabilities.canEdit || rehire;
  // The number the server generates: the employee number and the first free
  // -n, a number taken however it is cased.
  const taken = new Set(data.map((one) => one.employmentNumber?.toLowerCase()));
  let next = 1;
  while (taken.has(`${person.employeeNumber}-${next}`.toLowerCase())) next += 1;
  const add = canAdd && (
    <Button leftSection={<IconPlus size={16} />} onClick={() => setAdding(true)}>
      {t(rehire ? "rehire" : "newEmployment")}
    </Button>
  );
  const close = () => setAdding(false);
  const dialog = canAdd && (
    <Modal
      opened={adding}
      onClose={close}
      title={t(rehire ? "rehireTitle" : "newEmploymentTitle", { name: person.displayName })}
      size="xl"
      fullScreen={phone}
    >
      {adding && (
        <EmploymentForm
          person={person}
          intro={
            rehire && (
              <Alert color="blue" icon={<IconInfoCircle size={16} />}>
                {t("rehireExplained")}
              </Alert>
            )
          }
          generatedNumber={`${person.employeeNumber}-${next}`}
          cancelLabel={t("cancel")}
          onCancel={close}
          onSaved={close}
          savedTitle={t("employmentAdded")}
        />
      )}
    </Modal>
  );

  if (data.length === 0)
    return (
      <>
        <EmptyState title={t("noEmployments")} action={add || undefined} />
        {dialog}
      </>
    );
  return (
    <Stack gap="lg">
      {add && <Group justify="flex-end">{add}</Group>}
      {dialog}
      {data.map((employment) => (
        <EmploymentCard
          key={employment.id}
          person={person}
          employment={employment}
          today={today}
          structure={structure}
          known={known}
          asSelf={asSelf}
        />
      ))}
    </Stack>
  );
};

type Dialog = { kind: "terms"; mode: TermsModalMode } | { kind: "end" } | { kind: "edit" } | undefined;

const EmploymentCard = ({
  person,
  employment,
  today,
  structure,
  known,
  asSelf,
}: {
  person: StaffPerson;
  employment: StaffEmployment;
  today: string;
  structure?: StaffStructure;
  known: Map<number, StaffPersonSummary>;
  asSelf?: boolean;
}) => {
  const format = useStaffFormat();
  const t = format.t;
  const queryClient = useQueryClient();
  const canEdit = person.capabilities.canEdit;
  const { data: terms = [] } = useQuery(termsQueryOptions(employment.id));
  const [dialog, setDialog] = useState<Dialog>();

  // The managers the rows name that the page does not know yet, one read each.
  const unknown = [
    ...new Set(
      terms.flatMap((row) => (row.managerPersonId && !known.has(row.managerPersonId) ? [row.managerPersonId] : [])),
    ),
  ];
  const managers = useQueries({ queries: unknown.map((id) => ({ ...personQueryOptions(id), retry: false })) });
  const personName = (id: number) => {
    const found = known.get(id) ?? managers[unknown.indexOf(id)]?.data;
    if (found) return found.displayName;
    return managers[unknown.indexOf(id)]?.isError ? t("personById", { id }) : t("loadingName");
  };

  const refresh = () => queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });

  const reopen = useMutation({
    mutationFn: () => reopenEmployment(employment.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("employmentReopened"), message: "" });
      await refresh();
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotReopen"), message: error.message }),
  });
  const remove = useMutation({
    mutationFn: (row: StaffTerms) => deleteTerms(row.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("termsDeleted"), message: "" });
      await refresh();
    },
    onError: (error) => notifications.show({ color: "red", title: t("couldNotDeleteTerms"), message: error.message }),
  });

  const ended = employment.endDate !== undefined && employment.endDate < today;
  const status = ended
    ? t("statusEndedOn", { date: format.date(employment.endDate as string) })
    : employment.startDate > today
      ? t("statusStartsOn", { date: format.date(employment.startDate) })
      : t("statusActive");
  const title = employment.employmentNumber
    ? t("employmentTitle", { number: employment.employmentNumber })
    : t("employmentFrom", { date: format.date(employment.startDate) });
  // Named from the structure when the reader holds it, else from the
  // person's place today, which every reader of the person reads.
  const unit =
    structure?.businessUnits.find((row) => row.id === employment.businessUnitId)?.name ??
    (person.businessUnit && person.businessUnit.id === employment.businessUnitId
      ? person.businessUnit.name
      : undefined);
  const canSeePay = person.capabilities.canSeePay;
  // New terms start from the row in force today; when none is — before the
  // start, the first row; after the end, the last.
  const inForce =
    [...terms].reverse().find((row) => inForceOn(row, today, employment)) ??
    (today < employment.startDate ? terms[0] : terms[terms.length - 1]);

  const confirmReopen = () =>
    modals.openConfirmModal({
      title: t("reopenTitle"),
      children: <Text size="sm">{t("reopenExplained")}</Text>,
      labels: { confirm: t("reopenEmployment"), cancel: t("cancel") },
      onConfirm: () => reopen.mutate(),
    });
  const confirmDelete = (row: StaffTerms) =>
    modals.openConfirmModal({
      title: t("deleteTermsTitle", { date: format.date(row.validFrom) }),
      children: <Text size="sm">{t("deleteTermsExplained")}</Text>,
      labels: { confirm: t("deleteTerms"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(row),
    });

  return (
    <Card withBorder radius="md" padding="lg" component="section" aria-label={title}>
      <Stack gap="md">
        <Group justify="space-between" align="flex-start" wrap="wrap" gap="xs">
          <Group gap="xs">
            <Title order={3} size="h4">
              {title}
            </Title>
            <Badge variant="light" color={ended ? "gray" : employment.startDate > today ? "blue" : "green"}>
              {status}
            </Badge>
          </Group>
          {canEdit && (
            <Group gap="xs" wrap="wrap">
              {inForce && (
                <Button
                  size="xs"
                  leftSection={<IconPlus size={14} />}
                  onClick={() => setDialog({ kind: "terms", mode: { kind: "new", from: inForce } })}
                >
                  {t("newTerms")}
                </Button>
              )}
              <Button
                size="xs"
                variant="light"
                leftSection={<IconPencil size={14} />}
                onClick={() => setDialog({ kind: "edit" })}
              >
                {t("editEmployment")}
              </Button>
              <Button
                size="xs"
                variant="light"
                color="orange"
                leftSection={<IconDoorExit size={14} />}
                onClick={() => setDialog({ kind: "end" })}
              >
                {t("endEmployment")}
              </Button>
              {employment.endDate && (
                <Button
                  size="xs"
                  variant="light"
                  leftSection={<IconRotateClockwise size={14} />}
                  loading={reopen.isPending}
                  onClick={confirmReopen}
                >
                  {t("reopenEmployment")}
                </Button>
              )}
            </Group>
          )}
        </Group>

        <Fields
          items={[
            [t("startDate"), format.date(employment.startDate)],
            [t("endDate"), employment.endDate && format.date(employment.endDate)],
            [t("endReason"), employment.endReason && t(`endReason_${employment.endReason}`)],
            [t("businessUnit"), unit],
            [
              t("relationshipType"),
              employment.relationshipType && t(`relationshipType_${employment.relationshipType}`),
            ],
            [t("contractSignedAt"), employment.contractSignedAt && format.date(employment.contractSignedAt)],
            [t("fteChangedAt"), employment.fteChangedAt && format.date(employment.fteChangedAt)],
            [t("salaryChangedAt"), canSeePay && employment.salaryChangedAt && format.date(employment.salaryChangedAt)],
          ]}
        />

        <Stack gap="xs">
          <Title order={4} size="h5">
            {t("terms")}
          </Title>
          <TermsTimeline
            employment={employment}
            terms={terms}
            asSelf={asSelf}
            today={today}
            structure={structure}
            personName={personName}
            departmentName={(id) => (person.department?.id === id ? person.department.name : undefined)}
            locationName={(id) => (person.location?.id === id ? person.location.name : undefined)}
            canSeePay={canSeePay}
            onCorrect={canEdit ? (row) => setDialog({ kind: "terms", mode: { kind: "correct", row } }) : undefined}
            onDelete={canEdit ? confirmDelete : undefined}
          />
        </Stack>

        {/* The check says something about the pay, so it is read by those who
            read the employment group — whose employment carries its number. */}
        {employment.employmentNumber !== undefined && <ContractCheck employmentId={employment.id} />}
      </Stack>

      {canEdit && (
        <>
          <TermsFormModal
            employment={employment}
            terms={terms}
            mode={dialog?.kind === "terms" ? dialog.mode : undefined}
            structure={structure}
            canSeePay={canSeePay}
            manager={(id) => known.get(id) ?? managers[unknown.indexOf(id)]?.data}
            onClose={() => setDialog(undefined)}
          />
          <EndEmploymentModal
            employment={employment}
            today={today}
            opened={dialog?.kind === "end"}
            onClose={() => setDialog(undefined)}
          />
          <EmploymentEditModal
            employment={employment}
            structure={structure}
            opened={dialog?.kind === "edit"}
            onClose={() => setDialog(undefined)}
          />
        </>
      )}
    </Card>
  );
};
