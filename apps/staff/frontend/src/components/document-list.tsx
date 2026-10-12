import { ActionIcon, Badge, Card, Group, Stack, Table, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconDownload, IconPencil, IconTrash } from "@tabler/icons-react";
import { useMutation } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { downloadDocument, type StaffDocument, type StaffDocumentCategorySummary, saveFile } from "../api/documents";
import "../i18n";
import { useStaffFormat } from "../lib/format";
import { CategoryBadge } from "./category-badge";

const MIB = 1024 * 1024;

export interface DocumentListProps {
  /** As the server answered them for this reader, newest first. */
  documents: StaffDocument[];
  /** The business zone's day a validity is judged on. */
  today: string;
  /** Whether this reader may change and remove the document — HR's, health documents `staff:personal-data`'s. */
  canChange?: (document: StaffDocument) => boolean;
  onEdit?: (document: StaffDocument) => void;
  onRemove?: (document: StaffDocument) => void;
}

/**
 * A person's documents grouped by category (design §7), each group under the
 * category's name in the reader's language with its class, each row with the
 * file, its size, the validity — flagged once it has passed — the signing
 * date and when it was added. The bytes are fetched over the session and
 * handed to the browser under the name the server gives them: a bare link
 * would not carry the refusal back to the page.
 */
export const DocumentList = ({ documents, today, canChange = () => false, onEdit, onRemove }: DocumentListProps) => {
  const { t, locale, formatters } = useI18n("staff");
  const format = useStaffFormat();

  const download = useMutation({
    mutationFn: downloadDocument,
    onSuccess: saveFile,
    onError: (error, document) =>
      notifications.show({
        color: "red",
        title: t("couldNotDownloadDocument", { name: document.fileName }),
        message: error.message,
      }),
  });

  // The groups in the categories' own order: the seeds first, the company's after.
  const groups = new Map<number, { category: StaffDocumentCategorySummary; rows: StaffDocument[] }>();
  for (const document of [...documents].sort((a, b) => a.category.id - b.category.id)) {
    const group = groups.get(document.category.id) ?? { category: document.category, rows: [] };
    group.rows.push(document);
    groups.set(document.category.id, group);
  }

  const size = (bytes: number) =>
    bytes >= MIB
      ? `${formatters.formatNumber(bytes / MIB, { maximumFractionDigits: 1 })} MB`
      : `${formatters.formatNumber(Math.max(1, Math.round(bytes / 1024)))} KB`;

  return (
    <Stack gap="md">
      {[...groups.values()].map(({ category, rows }) => {
        const name = category.names[locale] ?? category.names.en;
        return (
          <Card key={category.id} withBorder radius="md" padding="lg" component="section" aria-label={name}>
            <Stack gap="sm">
              <Group gap="xs">
                <Title order={3} size="h4">
                  {name}
                </Title>
                <CategoryBadge documentClass={category.class} />
              </Group>
              <Table.ScrollContainer minWidth={560}>
                <Table>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("documentFile")}</Table.Th>
                      <Table.Th>{t("documentSize")}</Table.Th>
                      <Table.Th>{t("documentValidTo")}</Table.Th>
                      <Table.Th>{t("documentSignedAt")}</Table.Th>
                      <Table.Th>{t("documentUploadedAt")}</Table.Th>
                      <Table.Th>{t("documentActions")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {rows.map((document) => (
                      <Table.Tr key={document.id} aria-label={document.fileName}>
                        <Table.Td style={{ overflowWrap: "anywhere" }}>{document.fileName}</Table.Td>
                        <Table.Td>{size(document.sizeBytes)}</Table.Td>
                        <Table.Td>
                          {document.validTo ? (
                            <Group gap={6} wrap="nowrap">
                              <Text size="sm">{format.date(document.validTo)}</Text>
                              {document.validTo < today && (
                                <Badge size="sm" variant="light" color="red">
                                  {t("documentExpired")}
                                </Badge>
                              )}
                            </Group>
                          ) : (
                            t("notSet")
                          )}
                        </Table.Td>
                        <Table.Td>{document.signedAt ? format.date(document.signedAt) : t("notSet")}</Table.Td>
                        <Table.Td>
                          {formatters.formatDate(new Date(document.uploadedAt), { dateStyle: "medium" })}
                        </Table.Td>
                        <Table.Td>
                          <Group gap={4} wrap="nowrap">
                            <ActionIcon
                              variant="subtle"
                              aria-label={t("downloadDocument", { name: document.fileName })}
                              loading={download.isPending && download.variables?.id === document.id}
                              onClick={() => download.mutate(document)}
                            >
                              <IconDownload size={16} />
                            </ActionIcon>
                            {canChange(document) && onEdit && (
                              <ActionIcon
                                variant="subtle"
                                aria-label={t("changeDocument", { name: document.fileName })}
                                onClick={() => onEdit(document)}
                              >
                                <IconPencil size={16} />
                              </ActionIcon>
                            )}
                            {canChange(document) && onRemove && (
                              <ActionIcon
                                variant="subtle"
                                color="red"
                                aria-label={t("removeDocument", { name: document.fileName })}
                                onClick={() => onRemove(document)}
                              >
                                <IconTrash size={16} />
                              </ActionIcon>
                            )}
                          </Group>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            </Stack>
          </Card>
        );
      })}
    </Stack>
  );
};
