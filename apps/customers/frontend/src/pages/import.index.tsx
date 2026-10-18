import { ActionIcon, Alert, Anchor, Badge, Box, Button, Card, Group, Loader, Stack, Table, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import { type ChangeEvent, type DragEvent, useId, useState } from "react";
import { downloadImportTemplate, isSessionExpired, saveCsv } from "../api/import-export";
import {
  type CustomerImport,
  deleteImport,
  type ImportState,
  importsQueryKey,
  importsQueryOptions,
  isImportRunning,
  MAX_IMPORT_FILE_BYTES,
  uploadImport,
} from "../api/imports";
import { ApiValidationError } from "../api/request";
import "../i18n";

const stateColor: Record<ImportState, string> = {
  draft: "gray",
  checking: "blue",
  checked: "teal",
  importing: "blue",
  done: "green",
};

/**
 * The start of an import (customers import wizard design D8): a file dropped
 * or chosen, uploaded and parsed once, after which the page moves to the
 * import's own page; the template a click away, since a template still maps
 * itself; and the recent imports — a draft to pick up, a running one with
 * its progress, a finished one's result — each a link. Deleting one that is
 * not running takes its rows with it; the customers it made stay.
 */
export const ImportStartPage = () => {
  const { t, formatters } = useI18n("customers");
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const dropTextId = useId();
  const [over, setOver] = useState(false);
  const [problem, setProblem] = useState<string[] | null>(null);
  const imports = useQuery(importsQueryOptions(1, 10));

  const upload = useMutation({
    mutationFn: uploadImport,
    onSuccess: async (imp) => {
      await queryClient.invalidateQueries({ queryKey: importsQueryKey });
      void navigate({ to: `/customers/import/${imp.id}` as never });
    },
    onError: (error: Error) => {
      if (isSessionExpired(error)) return;
      if (error instanceof TypeError) setProblem([t("importUnreachable")]);
      else if (error instanceof ApiValidationError) setProblem(error.fields.file ?? Object.values(error.fields).flat());
      else setProblem([error.message]);
    },
  });
  const remove = useMutation({
    mutationFn: deleteImport,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: importsQueryKey }),
    onError: (error: Error) =>
      notifications.show({ color: "red", title: t("importCouldNotDelete"), message: error.message }),
  });

  const choose = (file: File | undefined) => {
    if (!file || upload.isPending) return;
    setProblem(null);
    if (file.size > MAX_IMPORT_FILE_BYTES) {
      setProblem([t("importFileTooLarge", { name: file.name })]);
      return;
    }
    upload.mutate(file);
  };
  const pick = (event: ChangeEvent<HTMLInputElement>) => {
    choose(event.target.files?.[0]);
    event.target.value = "";
  };
  const drop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setOver(false);
    choose(event.dataTransfer?.files?.[0]);
  };
  const template = async () => {
    try {
      saveCsv(await downloadImportTemplate());
    } catch (error) {
      if (!isSessionExpired(error)) setProblem([(error as Error).message]);
    }
  };
  const confirmDelete = (imp: CustomerImport) =>
    modals.openConfirmModal({
      title: t("importDeleteTitle", { name: imp.fileName }),
      children: <Text size="sm">{t("importDeleteWarning")}</Text>,
      labels: { confirm: t("importDelete"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => remove.mutate(imp.id),
    });

  const progressOf = (imp: CustomerImport) =>
    isImportRunning(imp.state)
      ? t("importListProgress", {
          done: formatters.formatNumber(imp.processed),
          total: formatters.formatNumber(imp.rows),
        })
      : imp.state === "draft"
        ? t("importFactRows", { count: formatters.formatNumber(imp.rows) })
        : t("importProgressCounts", {
            created: formatters.formatNumber(imp.created),
            updated: formatters.formatNumber(imp.updated),
            skipped: formatters.formatNumber(imp.skipped),
            failed: formatters.formatNumber(imp.failed),
          });

  return (
    <Stack gap="lg">
      <PageHeader
        breadcrumbs={[{ label: t("customers"), to: "/customers" }]}
        title={t("importTitle")}
        description={t("importPageIntro")}
      />
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Box
            p="lg"
            style={{
              border: "1px dashed var(--mantine-color-gray-4)",
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
            <Stack gap="xs" align="flex-start">
              <Text id={dropTextId} size="sm">
                {t("importDropFile")}
              </Text>
              <Group gap="sm">
                <input
                  type="file"
                  accept=".csv,.txt,text/csv,text/plain"
                  aria-label={t("importChooseFile")}
                  aria-describedby={dropTextId}
                  disabled={upload.isPending}
                  onChange={pick}
                />
                {upload.isPending && <Loader size="sm" aria-label={t("importUploading")} />}
              </Group>
              <Text size="xs" c="dimmed">
                {t("importFileHelp")}
              </Text>
            </Stack>
          </Box>
          <Anchor component="button" type="button" size="sm" onClick={() => void template()}>
            {t("importDownloadTemplate")}
          </Anchor>
          {problem && (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importCouldNotUpload")}>
              {problem.map((message, index) => (
                <Text key={index} size="sm">
                  {message}
                </Text>
              ))}
            </Alert>
          )}
        </Stack>
      </Card>

      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Text fw={600}>{t("importRecentTitle")}</Text>
          {imports.isPending ? (
            <ContentSkeleton rows={3} />
          ) : imports.isError ? (
            <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importsCouldNotLoad")}>
              <Text size="sm">{imports.error.message}</Text>
            </Alert>
          ) : imports.data.data.length === 0 ? (
            <EmptyState title={t("importNoneYet")} />
          ) : (
            <Table.ScrollContainer minWidth={700}>
              <Table highlightOnHover>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t("importFile")}</Table.Th>
                    <Table.Th>{t("importUploadedBy")}</Table.Th>
                    <Table.Th>{t("importUpdatedAt")}</Table.Th>
                    <Table.Th>{t("importState")}</Table.Th>
                    <Table.Th>{t("importResult")}</Table.Th>
                    <Table.Th>{t("actions")}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {imports.data.data.map((imp) => (
                    <Table.Tr key={imp.id}>
                      <Table.Td>
                        <Anchor component={Link} to={`/customers/import/${imp.id}` as never} size="sm">
                          {imp.fileName}
                        </Anchor>
                      </Table.Td>
                      <Table.Td>{imp.createdByName ?? "—"}</Table.Td>
                      <Table.Td>{formatters.formatDate(imp.finishedAt ?? imp.requestedAt ?? imp.createdAt)}</Table.Td>
                      <Table.Td>
                        <Badge variant="light" color={stateColor[imp.state]}>
                          {t(`importState_${imp.state}`)}
                        </Badge>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm">{progressOf(imp)}</Text>
                      </Table.Td>
                      <Table.Td>
                        <ActionIcon
                          variant="subtle"
                          color="red"
                          aria-label={t("importDeleteNamed", { name: imp.fileName })}
                          disabled={isImportRunning(imp.state)}
                          onClick={() => confirmDelete(imp)}
                        >
                          <IconTrash size={16} />
                        </ActionIcon>
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          )}
          <Group>
            <Button component={Link} to="/customers" variant="subtle" size="compact-sm">
              {t("importBackToCustomers")}
            </Button>
          </Group>
        </Stack>
      </Card>
    </Stack>
  );
};
