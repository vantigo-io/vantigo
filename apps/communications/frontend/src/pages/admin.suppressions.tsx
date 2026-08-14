import { Alert, Button, Card, Group, Loader, Modal, Stack, Table, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { ApiError } from "../api/request";
import { createSuppression, deleteSuppression, suppressionsQueryOptions } from "../api/suppressions";
import "../i18n";

export function SuppressionsPage() {
  const { t, formatters } = useI18n("communications");
  const client = useQueryClient();
  const query = useQuery(suppressionsQueryOptions());
  const form = useForm({
    initialValues: { emailAddress: "", reason: "" },
    validate: {
      emailAddress: (value) => (/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value) ? null : t("validEmailAddress")),
    },
  });
  const [search, setSearch] = useState("");
  const [deleteId, setDeleteId] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: createSuppression,
    onSuccess: () => {
      form.reset();
      void client.invalidateQueries({ queryKey: ["suppressions"] });
      notifications.show({ title: t("suppressionAdded"), message: t("addressWillNotReceive") });
    },
    onError: (error: ApiError) =>
      notifications.show({ color: "red", title: t("suppressionNotAdded"), message: error.message }),
  });
  const remove = useMutation({
    mutationFn: deleteSuppression,
    onSuccess: () => {
      setDeleteId(null);
      void client.invalidateQueries({ queryKey: ["suppressions"] });
      notifications.show({ title: t("suppressionDeleted"), message: t("addressCanReceive") });
    },
    onError: (error: ApiError) =>
      notifications.show({ color: "red", title: t("suppressionNotDeleted"), message: error.message }),
  });
  if (query.isPending) return <Loader />;
  if (query.isError) return <Alert color="red">{query.error.message}</Alert>;
  const visible = query.data.filter((item) =>
    `${item.emailAddress} ${item.reason || ""}`.toLowerCase().includes(search.toLowerCase()),
  );
  return (
    <Stack gap="xl">
      <PageHeader eyebrow={t("communications")} title={t("suppressions")} description={t("suppressionsDescription")} />
      <Card withBorder radius="lg">
        <form
          onSubmit={form.onSubmit((values) =>
            create.mutate({ emailAddress: values.emailAddress.trim(), reason: values.reason || undefined }),
          )}
        >
          <Group align="end">
            <TextInput label={t("emailAddress")} required {...form.getInputProps("emailAddress")} />
            <TextInput label={t("reasonOptional")} {...form.getInputProps("reason")} />
            <Button type="submit" loading={create.isPending}>
              {t("addSuppression")}
            </Button>
          </Group>
        </form>
      </Card>
      <Card withBorder radius="lg">
        <TextInput
          label={t("search")}
          placeholder={t("searchEmailOrReason")}
          value={search}
          onChange={(event) => setSearch(event.currentTarget.value)}
          mb="lg"
        />
        <Table.ScrollContainer minWidth={620}>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t("email")}</Table.Th>
                <Table.Th>{t("reason")}</Table.Th>
                <Table.Th>{t("created")}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {visible.map((item) => (
                <Table.Tr key={item.id}>
                  <Table.Td>{item.emailAddress}</Table.Td>
                  <Table.Td>{item.reason || t("noReason")}</Table.Td>
                  <Table.Td>
                    {formatters.formatDate(item.createdAt, { dateStyle: "medium", timeStyle: "short" })}
                  </Table.Td>
                  <Table.Td>
                    <Button color="red" variant="subtle" size="xs" onClick={() => setDeleteId(item.id)}>
                      {t("delete")}
                    </Button>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
        {visible.length === 0 && (
          <Text c="dimmed" ta="center" py="lg">
            {t("noSuppressionsFound")}
          </Text>
        )}
      </Card>
      <Modal opened={deleteId !== null} onClose={() => setDeleteId(null)} title={t("deleteSuppression")} centered>
        <Stack>
          <Text>{t("allowAddressAgain")}</Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setDeleteId(null)}>
              {t("cancel")}
            </Button>
            <Button color="red" loading={remove.isPending} onClick={() => deleteId && remove.mutate(deleteId)}>
              {t("deleteSuppression")}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
}
