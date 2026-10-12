import { Alert, Box, Button, Group, Modal, Select, Stack, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { type UseFormReturnType, useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type ChangeEvent, type DragEvent, useState } from "react";
import { type StaffDocumentCategory, uploadDocument } from "../api/documents";
import { STAFF_QUERY_KEY } from "../api/request";
import "../i18n";
import { DOCUMENT_ACCEPT, fileableCategories, fileRefusal, placeDocumentRefusal } from "../lib/documents";

/** The fields a document's category and dates take, on the upload and on a change alike. */
export interface DocumentFieldValues {
  categoryId: string | null;
  validTo: string | null;
  signedAt: string | null;
}

/**
 * The category and the two dates. A contract asks for the signing date
 * first and says what it sets: the employment's signing date, when exactly
 * one employment covers that day and has none.
 */
export const DocumentFields = <V extends DocumentFieldValues>({
  form,
  categories,
}: {
  form: UseFormReturnType<V>;
  categories: StaffDocumentCategory[];
}) => {
  const { t, locale } = useI18n("staff");
  const chosen = categories.find((category) => String(category.id) === form.values.categoryId);
  const contract = chosen?.key === "contract";
  const signedAt = (
    <DateInput
      key="signedAt"
      label={t("documentSignedAt")}
      description={contract ? t("contractSignedAtHint") : t("documentSignedAtDescription")}
      valueFormat={t("dateInputFormat")}
      clearable
      {...form.getInputProps("signedAt")}
    />
  );
  const validTo = (
    <DateInput
      key="validTo"
      label={t("documentValidTo")}
      description={t("documentValidToDescription")}
      valueFormat={t("dateInputFormat")}
      clearable
      {...form.getInputProps("validTo")}
    />
  );
  return (
    <>
      <Select
        label={t("documentCategory")}
        description={chosen?.restricted ? t("documentHealthNote") : undefined}
        withAsterisk
        allowDeselect={false}
        data={categories.map((category) => ({
          value: String(category.id),
          label: category.names[locale] ?? category.names.en,
        }))}
        {...form.getInputProps("categoryId")}
      />
      {contract ? [signedAt, validTo] : [validTo, signedAt]}
    </>
  );
};

interface UploadValues extends DocumentFieldValues {
  file: File | null;
}

export interface DocumentUploadProps {
  personId: number;
  opened: boolean;
  onClose: () => void;
  /** Every category; the upload offers those this reader may file under. */
  categories: StaffDocumentCategory[];
  /** `staff:personal-data` — the restricted (health) categories are offered. */
  canSeeHealth: boolean;
}

/** *Add document*: one file, its category and its dates. On a phone the form is a sheet that takes the whole screen. */
export const DocumentUpload = ({ opened, onClose, ...props }: DocumentUploadProps) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  return (
    <Modal opened={opened} onClose={onClose} title={t("addDocument")} size="lg" fullScreen={phone}>
      {opened && <UploadForm {...props} onDone={onClose} />}
    </Modal>
  );
};

const UploadForm = ({
  personId,
  categories,
  canSeeHealth,
  onDone,
}: Omit<DocumentUploadProps, "opened" | "onClose"> & { onDone: () => void }) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  // A camera is offered where there is one to hand: a touch screen.
  const touch = useMediaQuery("(pointer: coarse)");
  const [over, setOver] = useState(false);
  const [banner, setBanner] = useState<string | null>(null);
  const offered = fileableCategories(categories, canSeeHealth);
  const form = useForm<UploadValues>({
    initialValues: { file: null, categoryId: null, validTo: null, signedAt: null },
    validate: {
      file: (file) => (file ? fileRefusal(file, t) : t("documentFileRequired")),
      categoryId: (value) => (value ? null : t("documentCategoryRequired")),
    },
  });

  const upload = useMutation({
    mutationFn: (values: UploadValues) =>
      uploadDocument(personId, {
        file: values.file as File,
        categoryId: Number(values.categoryId),
        ...(values.validTo ? { validTo: values.validTo } : {}),
        ...(values.signedAt ? { signedAt: values.signedAt } : {}),
      }),
    onSuccess: async (document) => {
      notifications.show({ color: "teal", title: t("documentUploaded", { name: document.fileName }), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      onDone();
    },
    onError: (error) => placeDocumentRefusal(error, t, form.setErrors, setBanner),
  });

  const take = (file: File | undefined) => {
    if (!file) return;
    form.setFieldValue("file", file);
    // Said at once, before anything is sent.
    form.setFieldError("file", fileRefusal(file, t));
  };
  const pick = (event: ChangeEvent<HTMLInputElement>) => {
    take(event.target.files?.[0]);
    event.target.value = "";
  };
  const drop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setOver(false);
    take(event.dataTransfer?.files?.[0]);
  };

  return (
    <form
      onSubmit={form.onSubmit((values) => {
        setBanner(null);
        upload.mutate(values);
      })}
    >
      <Stack>
        {banner && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("couldNotUploadDocument")}>
            {banner}
          </Alert>
        )}
        <Box
          role="group"
          aria-label={t("documentFile")}
          p="sm"
          style={{
            border: `1px dashed var(--mantine-color-${form.errors.file ? "red-6" : "gray-4"})`,
            borderRadius: "var(--mantine-radius-sm)",
            background: over ? "var(--mantine-color-gray-0)" : undefined,
          }}
          onDragOver={(event) => {
            event.preventDefault();
            setOver(true);
          }}
          onDragLeave={() => setOver(false)}
          onDrop={drop}
        >
          <Stack gap="xs">
            <Text size="sm" c="dimmed">
              {t("dropDocumentHere")}
            </Text>
            <Group gap="sm" wrap="wrap">
              <input
                type="file"
                accept={DOCUMENT_ACCEPT}
                aria-label={t("chooseFile")}
                disabled={upload.isPending}
                onChange={pick}
              />
              {touch && (
                <input
                  type="file"
                  accept="image/*"
                  capture="environment"
                  aria-label={t("takePhoto")}
                  disabled={upload.isPending}
                  onChange={pick}
                />
              )}
            </Group>
            {form.values.file && (
              <Text size="sm" fw={500} style={{ overflowWrap: "anywhere" }}>
                {form.values.file.name}
              </Text>
            )}
            {form.errors.file && (
              <Text size="sm" c="red">
                {form.errors.file}
              </Text>
            )}
          </Stack>
        </Box>
        <DocumentFields form={form} categories={offered} />
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
