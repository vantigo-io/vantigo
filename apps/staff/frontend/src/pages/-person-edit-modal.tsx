import { Button, Group, Modal, Stack, Textarea } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type StaffPerson, updatePerson } from "../api/people";
import { STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { fieldRefusals, isRevisionConflict } from "../lib/errors";
import {
  type PersonFormValues,
  personFields,
  personFormValuesOf,
  personInputOf,
  personValidation,
} from "../lib/person-form";
import { PersonFields } from "./-person-form";

export interface PersonEditModalProps {
  person: StaffPerson;
  opened: boolean;
  onClose: () => void;
}

/**
 * HR's edit of a person (`PUT /people/{id}`, `staff:manage`): the work
 * group, the private group and HR's notes, replaced whole with the revision
 * the form was opened at. The employee number is shown and never sent; the
 * sealed fields and the link have their own places on the page. On a phone
 * the form takes the whole screen.
 */
export const PersonEditModal = ({ person, opened, onClose }: PersonEditModalProps) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  return (
    <Modal opened={opened} onClose={onClose} title={t("editDetails")} size="xl" fullScreen={phone}>
      {opened && <PersonEditForm person={person} onDone={onClose} />}
    </Modal>
  );
};

const PersonEditForm = ({ person, onDone }: { person: StaffPerson; onDone: () => void }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  const [privateOpen, setPrivateOpen] = useState(true);
  const form = useForm<PersonFormValues>({ initialValues: personFormValuesOf(person), validate: personValidation(t) });

  const save = useMutation({
    mutationFn: (values: PersonFormValues) => updatePerson(person.id, personInputOf(values), person.revision),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("detailsSaved"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: async (error) => {
      if (isRevisionConflict(error)) {
        // Somebody else saved first: the form closes on the fresh record
        // rather than overwriting what they did.
        notifications.show({ color: "yellow", title: t("revisionConflict"), message: t("revisionConflictHint") });
        await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
        onDone();
        return;
      }
      const { fields, rest } = fieldRefusals(error, personFields);
      form.setErrors(fields);
      if (rest.length > 0)
        notifications.show({ color: "red", title: t("couldNotSavePerson"), message: rest.join(" ") });
    },
  });

  return (
    <form onSubmit={form.onSubmit((values) => save.mutate(values))}>
      <Stack>
        <PersonFields
          form={form}
          fixedNumber={person.employeeNumber}
          privateOpen={privateOpen}
          onTogglePrivate={() => setPrivateOpen((open) => !open)}
        />
        <Textarea
          label={t("hrNotes")}
          description={t("termsNoteDescription")}
          minRows={3}
          {...form.getInputProps("notes")}
        />
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
