import { Alert, Button, Group, Modal, Stack } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconInfoCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { addTerms, type StaffEmployment, type StaffTerms, updateTerms } from "../api/employments";
import type { StaffStructure } from "../api/meta";
import type { StaffPersonSummary } from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { conflictField, isRevisionConflict, mappedRefusals, termsConflictFields } from "../lib/errors";
import { useStaffFormat } from "../lib/format";
import { defaultFullTimeHours } from "../lib/terms";
import {
  type TermsFormValues,
  termsFields,
  termsFormPath,
  termsFormValuesOf,
  termsRowInputOf,
  termsValidation,
} from "../lib/terms-form";
import { TermsFields } from "./-terms-fields";

/** New terms from the row they start from, or a correction of one row. */
export type TermsModalMode = { kind: "new"; from: StaffTerms } | { kind: "correct"; row: StaffTerms };

export interface TermsFormModalProps {
  employment: StaffEmployment;
  /** The chain, oldest first — which row is the first and which the latest. */
  terms: StaffTerms[];
  /** Open with this mode; undefined is closed. */
  mode?: TermsModalMode;
  structure?: StaffStructure;
  canSeePay: boolean;
  /** The person a row names as manager, when the page knows them, so the picker can say who it is. */
  manager: (id: number) => StaffPersonSummary | undefined;
  onClose: () => void;
}

/**
 * A terms row's form (design §7): *New terms* starts from the row in force
 * and takes effect on a day HR gives; *Correct* rewrites a row as it was
 * agreed — a past row with `correction: true`, the first one on the
 * employment's start. New Person's fields are reused whole, with the pieces
 * a first row seldom needs (the pay components, the seniority date and the
 * grade, the probation's extension) under "More terms", open here since the
 * row arrives filled in.
 */
export const TermsFormModal = ({ mode, ...props }: TermsFormModalProps) => {
  const format = useStaffFormat();
  const phone = useMediaQuery("(max-width: 48em)");
  const title =
    mode?.kind === "correct"
      ? format.t("correctTermsTitle", { date: format.date(mode.row.validFrom) })
      : format.t("newTerms");
  return (
    <Modal opened={mode !== undefined} onClose={props.onClose} title={title} size="xl" fullScreen={phone}>
      {mode && <TermsForm mode={mode} {...props} />}
    </Modal>
  );
};

interface TermsRowFormValues {
  validFrom: string | null;
  terms: TermsFormValues;
}

const TermsForm = ({
  mode,
  employment,
  terms,
  structure,
  canSeePay,
  manager,
  onClose,
}: Omit<TermsFormModalProps, "mode"> & { mode: TermsModalMode }) => {
  const format = useStaffFormat();
  const t = format.t;
  const queryClient = useQueryClient();
  const source = mode.kind === "new" ? mode.from : mode.row;
  const first = mode.kind === "correct" && terms[0]?.id === mode.row.id;
  const latest = mode.kind === "correct" && terms[terms.length - 1]?.id === mode.row.id;
  const initial = termsFormValuesOf(source);
  const [moreOpen, setMoreOpen] = useState(true);
  // Hours the row states apart from its scheme's own were typed by hand, and stay when the scheme changes.
  const [hoursTouched, setHoursTouched] = useState(
    initial.fullTimeHoursPerWeek !== defaultFullTimeHours[initial.workingHoursScheme],
  );

  const form = useForm<TermsRowFormValues>({
    initialValues: { validFrom: mode.kind === "correct" ? mode.row.validFrom : null, terms: initial },
    validate: {
      validFrom: (value) => (value ? null : t("validFromRequired")),
      terms: termsValidation(t, format.number),
    },
  });

  const shown = (path: string) => {
    if (path === "validFrom") return true;
    const root = path.replace(/^terms\./, "").split(".")[0];
    return root === "payComponents" || (termsFields as readonly string[]).includes(root);
  };

  const save = useMutation({
    mutationFn: (values: TermsRowFormValues) => {
      const input = termsRowInputOf(values.terms, values.validFrom as string);
      if (mode.kind === "new") return addTerms(employment.id, input);
      // The latest row is replaced as it is; any other is HR's correction of the past.
      return updateTerms(mode.row.id, latest ? input : { ...input, correction: true }, mode.row.revision);
    },
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("termsSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onClose();
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        onClose();
        return;
      }
      // A rule about the row among the others (a day taken, a manager graph
      // that would loop) is a 409 with no field; it still lands on its field.
      const placed = conflictField(error, termsConflictFields);
      if (placed) {
        form.setErrors(placed);
        return;
      }
      const { fields, rest } = mappedRefusals(error, (field) => {
        const path = termsFormPath(field);
        return shown(path) ? path : undefined;
      });
      form.setErrors(fields);
      if (Object.keys(fields).some((path) => path !== "validFrom")) setMoreOpen(true);
      if (rest.length > 0) notifications.show({ color: "red", title: t("couldNotSaveTerms"), message: rest.join(" ") });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <Alert variant="light" color="blue" icon={<IconInfoCircle size={16} />}>
          {mode.kind === "new" ? t("newTermsExplained") : t("correctionExplained")}
        </Alert>
        <DateInput
          label={t("validFrom")}
          withAsterisk
          valueFormat={t("dateInputFormat")}
          disabled={first}
          description={first ? t("firstRowFixed") : undefined}
          minDate={employment.startDate}
          maxDate={employment.endDate}
          {...form.getInputProps("validFrom")}
        />
        <TermsFields
          form={form}
          structure={structure}
          canSeePay={canSeePay}
          moreOpen={moreOpen}
          onToggleMore={() => setMoreOpen((open) => !open)}
          hoursTouched={hoursTouched}
          onHoursTouched={() => setHoursTouched(true)}
          managerSelected={source.managerPersonId ? manager(source.managerPersonId) : undefined}
        />
        <Group justify="flex-end">
          <Button type="button" variant="default" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={save.isPending}>
            {t("saveTerms")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};
