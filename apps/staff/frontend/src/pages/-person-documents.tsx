import { Alert, Button, Group, Modal, Select, Stack, Text } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMediaQuery } from "@mantine/hooks";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { personCasesQueryOptions } from "../api/cases";
import {
  deleteDocument,
  documentCategoriesQueryOptions,
  documentsQueryOptions,
  type StaffDocument,
  type StaffDocumentCategory,
  updateDocument,
} from "../api/documents";
import type { StaffPerson } from "../api/people";
import { type ApiError, STAFF_QUERY_KEY } from "../api/request";
import { DocumentList } from "../components/document-list";
import { DocumentFields, type DocumentFieldValues, DocumentUpload } from "../components/document-upload";
import "../i18n";
import { fileableCategories, placeDocumentRefusal } from "../lib/documents";
import { isRevisionConflict, refusalMessage } from "../lib/errors";
import { useStaffFormat } from "../lib/format";

export interface PersonDocumentsProps {
  person: StaffPerson;
  /** The business zone's day a validity is judged on. */
  today: string;
  /** The caller's `staff:personal-data` (`/meta`'s `canSeeHealth`): health documents are theirs to file, change and remove. */
  canSeeHealth: boolean;
}

/**
 * The Documents tab (design §7, S14): the person's archive grouped by
 * category, and — for `staff:manage` and `staff:personal-data` — filing,
 * changing and removing a document. What is listed is what the server
 * answered this reader; a health document is not offered to change to a
 * reader without `staff:personal-data`, whose write the server would refuse.
 */
export const PersonDocuments = ({ person, today, canSeeHealth }: PersonDocumentsProps) => {
  const { t } = useI18n("staff");
  const queryClient = useQueryClient();
  // Filing is staff:manage's (canEdit) and staff:personal-data's (canHold).
  const writes = person.capabilities.canEdit || person.capabilities.canHold;
  const documents = useQuery(documentsQueryOptions(person.id));
  const categories = useQuery({ ...documentCategoriesQueryOptions(), enabled: writes });
  const [uploading, setUploading] = useState(false);
  const [editing, setEditing] = useState<StaffDocument | null>(null);

  const remove = useMutation({
    mutationFn: (document: StaffDocument) => deleteDocument(document.id),
    onSuccess: async (_none, document) => {
      notifications.show({ color: "teal", title: t("documentRemoved", { name: document.fileName }), message: "" });
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
    },
    onError: (error, document) =>
      notifications.show({
        color: "red",
        title: t("couldNotRemoveDocument", { name: document.fileName }),
        message: (error as ApiError).code === "category_restricted" ? t("documentRestricted") : refusalMessage(error),
      }),
  });
  const confirmRemove = (document: StaffDocument) =>
    modals.openConfirmModal({
      title: t("removeDocumentTitle", { name: document.fileName }),
      children: <Text size="sm">{t("removeDocumentExplained")}</Text>,
      labels: { confirm: t("remove"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(document),
    });

  const canChange = (document: StaffDocument) => writes && (canSeeHealth || !document.category.restricted);

  return (
    <Stack gap="md">
      <Group justify="space-between" align="flex-start" wrap="wrap" gap="xs">
        <Text size="sm" c="dimmed" maw={640}>
          {t("documentsDescription")}
        </Text>
        {writes && categories.data && (
          <Button leftSection={<IconPlus size={16} />} onClick={() => setUploading(true)}>
            {t("addDocument")}
          </Button>
        )}
      </Group>
      {documents.isError && (
        <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("failedToLoadDocuments")}>
          {documents.error.message}
        </Alert>
      )}
      {documents.isPending && <ContentSkeleton rows={3} rowHeight={44} />}
      {documents.data?.length === 0 && <EmptyState title={t("noDocuments")} />}
      {documents.data && documents.data.length > 0 && (
        <DocumentList
          documents={documents.data}
          today={today}
          canChange={canChange}
          onEdit={setEditing}
          onRemove={confirmRemove}
        />
      )}
      {categories.data && (
        <>
          <DocumentUpload
            personId={person.id}
            opened={uploading}
            onClose={() => setUploading(false)}
            categories={categories.data}
            canSeeHealth={canSeeHealth}
          />
          <DocumentEditModal
            document={editing}
            onClose={() => setEditing(null)}
            categories={categories.data}
            canSeeHealth={canSeeHealth}
          />
        </>
      )}
    </Stack>
  );
};

interface DocumentEditModalProps {
  document: StaffDocument | null;
  onClose: () => void;
  categories: StaffDocumentCategory[];
  canSeeHealth: boolean;
}

/** A document's category and dates; the file never changes. */
const DocumentEditModal = ({ document, onClose, ...props }: DocumentEditModalProps) => {
  const { t } = useI18n("staff");
  const phone = useMediaQuery("(max-width: 48em)");
  return (
    <Modal
      opened={document !== null}
      onClose={onClose}
      title={document ? t("changeDocument", { name: document.fileName }) : ""}
      fullScreen={phone}
    >
      {document && <DocumentEditForm key={document.id} document={document} onDone={onClose} {...props} />}
    </Modal>
  );
};

const DocumentEditForm = ({
  document,
  categories,
  canSeeHealth,
  onDone,
}: Omit<DocumentEditModalProps, "document" | "onClose"> & { document: StaffDocument; onDone: () => void }) => {
  const { t, date } = useStaffFormat();
  const queryClient = useQueryClient();
  const [banner, setBanner] = useState<string | null>(null);
  const offered = fileableCategories(categories, canSeeHealth);
  const form = useForm<DocumentFieldValues & { caseId: string | null }>({
    initialValues: {
      categoryId: String(document.category.id),
      validTo: document.validTo ?? null,
      signedAt: document.signedAt ?? null,
      caseId: document.caseId === undefined ? null : String(document.caseId),
    },
    validate: { categoryId: (value) => (value ? null : t("documentCategoryRequired")) },
  });
  // A health document — one of a restricted category — may be filed under one
  // of the person's sick-leave cases (S15), staff:personal-data's to do; the
  // server takes caseId for no other category, so none is sent for one.
  const restricted = categories.find((one) => String(one.id) === form.values.categoryId)?.restricted ?? false;
  const cases = useQuery({
    ...personCasesQueryOptions(document.personId, "all"),
    enabled: canSeeHealth && restricted,
  });

  const save = useMutation({
    // A replace: a date left out is cleared; the employment it is filed
    // against is left as the upload derived it.
    mutationFn: (values: DocumentFieldValues & { caseId: string | null }) =>
      updateDocument(
        document.id,
        {
          categoryId: Number(values.categoryId),
          ...(values.validTo ? { validTo: values.validTo } : {}),
          ...(values.signedAt ? { signedAt: values.signedAt } : {}),
          // Named only for a restricted category: a case chosen, or null to take it off one.
          ...(canSeeHealth && restricted
            ? values.caseId
              ? { caseId: Number(values.caseId) }
              : document.caseId !== undefined
                ? { caseId: null }
                : {}
            : {}),
        },
        document.revision,
      ),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("documentSaved"), message: "" });
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
      placeDocumentRefusal(error, t, form.setErrors, setBanner);
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
        <DocumentFields form={form} categories={offered} />
        {canSeeHealth && restricted && (
          <Select
            label={t("documentCase")}
            description={t("documentCaseDescription")}
            clearable
            data={(cases.data?.data ?? []).map((one) => ({
              value: String(one.id),
              label: t("caseFrom", { date: date(one.firstDay) }),
            }))}
            {...form.getInputProps("caseId")}
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
