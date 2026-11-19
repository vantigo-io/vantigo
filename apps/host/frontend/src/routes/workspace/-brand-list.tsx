import {
  ActionIcon,
  Badge,
  Box,
  Button,
  ColorInput,
  FileButton,
  Group,
  Menu,
  Modal,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconDots } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { BUILTIN_PRIMARY, PageSection, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  archiveBrand,
  type Brand,
  type BrandInputs,
  type BrandSummary,
  brandQueryKey,
  brandRulesQueryKey,
  brandsQueryKey,
  createBrand,
  deleteBrand,
  duplicateBrand,
  exportInputsOf,
  fetchBrand,
  makeDefaultBrand,
  restoreBrand,
} from "../../api/brands";
import "../../i18n";

type ErrorBody = { status?: number; code?: string; message?: string };

interface Props {
  items: readonly BrandSummary[];
  ruleCounts: ReadonlyMap<string, number>;
  selectedId: string | undefined;
  onSelect: (id: string) => void;
}

/** The inputs a file exported from here carries, or null when it is something else. */
export function inputsFromFile(text: string): BrandInputs | null {
  try {
    const v = JSON.parse(text) as Partial<BrandInputs> | null;
    if (!v || typeof v !== "object" || typeof v.name !== "string" || typeof v.primary !== "string") return null;
    return {
      name: v.name,
      title: typeof v.title === "string" ? v.title : undefined,
      primary: v.primary,
      radius: (v.radius ?? "md") as BrandInputs["radius"],
      support: typeof v.support === "object" && v.support ? v.support : undefined,
    };
  } catch {
    return null;
  }
}

/**
 * The brands list (brands design B21): each with its swatch, state and number of rules, and the actions
 * create, duplicate, make default, archive, restore and delete, and export and import of a brand's
 * inputs as a JSON file. Every action takes the revision the list was read at; a stale one is refused.
 */
export const BrandList = ({ items, ruleCounts, selectedId, onSelect }: Props) => {
  const { t } = useI18n("host");
  const qc = useQueryClient();
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [primary, setPrimary] = useState(BUILTIN_PRIMARY);
  const [nameError, setNameError] = useState<string | undefined>();

  const explain = (error: unknown): string => {
    const e = error as ErrorBody;
    if (e.status === 409 && e.code === "stale_revision") return t("brands.error.stale");
    if (e.code === "brand_name_taken") return t("brands.error.nameTaken");
    if (e.code === "brand_in_use") return t("brands.error.inUse");
    if (e.code === "brand_not_published") return t("brands.error.notPublished");
    if (e.code === "brand_primary_range") return t("brands.primaryRange");
    return e.message || t("brands.error.generic");
  };
  const failed = (error: unknown) => notifications.show({ color: "red", message: explain(error) });
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: brandsQueryKey });
    void qc.invalidateQueries({ queryKey: brandRulesQueryKey });
  };
  const done = (message: string, next?: Brand) => {
    if (next) qc.setQueryData(brandQueryKey(next.id), next);
    refresh();
    notifications.show({ color: "green", message });
  };

  const create = useMutation({
    mutationFn: ({ inputs }: { inputs: BrandInputs; imported: boolean }) => createBrand(inputs),
    onSuccess: (next, { imported }) => {
      done(t(imported ? "brands.done.imported" : "brands.done.created"), next);
      setCreating(false);
      setName("");
      setNameError(undefined);
      onSelect(next.id);
    },
    onError: (error) => {
      const e = error as ErrorBody;
      if (e.code === "brand_name_taken") setNameError(t("brands.error.nameTaken"));
      failed(error);
    },
  });
  const duplicate = useMutation({
    mutationFn: (b: BrandSummary) => duplicateBrand(b.id, b.revision),
    onSuccess: (next) => {
      done(t("brands.done.duplicated"), next);
      onSelect(next.id);
    },
    onError: failed,
  });
  const archive = useMutation({
    mutationFn: (b: BrandSummary) => archiveBrand(b.id, b.revision),
    onSuccess: (next) => done(t("brands.done.archived"), next),
    onError: failed,
  });
  const restore = useMutation({
    mutationFn: (b: BrandSummary) => restoreBrand(b.id, b.revision),
    onSuccess: (next) => done(t("brands.done.restored"), next),
    onError: failed,
  });
  const makeDefault = useMutation({
    mutationFn: (b: BrandSummary) => makeDefaultBrand(b.id, b.revision),
    onSuccess: (next) => done(t("brands.done.madeDefault"), next),
    onError: failed,
  });
  const remove = useMutation({
    mutationFn: (b: BrandSummary) => deleteBrand(b.id, b.revision),
    onSuccess: () => done(t("brands.done.deleted")),
    onError: failed,
  });

  const confirm = (kind: "archive" | "delete" | "makeDefault", b: BrandSummary, run: () => void) =>
    modals.openConfirmModal({
      title: t(`brands.confirm.${kind}.title`, { name: b.name }),
      children: <Text size="sm">{t(`brands.confirm.${kind}.body`)}</Text>,
      labels: { confirm: t(`brands.action.${kind}`), cancel: t("common.cancel") },
      confirmProps: kind === "makeDefault" ? undefined : { color: "red" },
      onConfirm: run,
    });

  const exportBrand = async (b: BrandSummary) => {
    try {
      const full = await fetchBrand(b.id);
      const blob = new Blob([`${JSON.stringify(exportInputsOf(full), null, 2)}\n`], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `${
        b.name
          .toLowerCase()
          .replace(/[^a-z0-9]+/g, "-")
          .replace(/^-|-$/g, "") || "brand"
      }.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (error) {
      failed(error);
    }
  };
  const importFile = async (file: File | null) => {
    if (!file) return;
    const inputs = inputsFromFile(await file.text());
    if (!inputs) {
      notifications.show({ color: "red", message: t("brands.import.invalid") });
      return;
    }
    create.mutate({ inputs, imported: true });
  };

  return (
    <PageSection title={t("brands.list.heading")}>
      <Stack gap="sm">
        <Group justify="flex-end" gap="xs">
          <FileButton accept="application/json,.json" onChange={importFile}>
            {(props) => (
              <Button variant="default" {...props}>
                {t("brands.list.import")}
              </Button>
            )}
          </FileButton>
          <Button onClick={() => setCreating(true)}>{t("brands.list.new")}</Button>
        </Group>
        <Table withTableBorder data-testid="brand-list">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("brands.name")}</Table.Th>
              <Table.Th>{t("brands.titleLabel")}</Table.Th>
              <Table.Th>{t("brands.list.state")}</Table.Th>
              <Table.Th>{t("brands.list.rules")}</Table.Th>
              <Table.Th />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {items.map((b) => {
              const archived = b.state === "archived";
              return (
                <Table.Tr
                  key={b.id}
                  data-testid="brand-row"
                  data-brand={b.name}
                  bg={b.id === selectedId ? "var(--mantine-color-vantigo-light)" : undefined}
                >
                  <Table.Td>
                    <Group gap="xs" wrap="nowrap">
                      <Box
                        aria-hidden
                        w={16}
                        h={16}
                        style={{
                          background: b.primary,
                          borderRadius: 4,
                          border: "1px solid var(--mantine-color-default-border)",
                        }}
                      />
                      <Button
                        variant="subtle"
                        size="compact-sm"
                        onClick={() => onSelect(b.id)}
                        c={archived ? "dimmed" : undefined}
                      >
                        {b.name}
                      </Button>
                      {b.isDefault && (
                        <Badge variant="light" size="sm">
                          {t("brands.default")}
                        </Badge>
                      )}
                    </Group>
                  </Table.Td>
                  <Table.Td>{b.title}</Table.Td>
                  <Table.Td>
                    <Group gap={4}>
                      <Badge
                        variant="light"
                        color={archived ? "gray" : b.state === "draft" ? "yellow" : "green"}
                        size="sm"
                      >
                        {archived
                          ? t("brands.state.archived")
                          : b.state === "draft"
                            ? t("brands.state.draft")
                            : t("brands.published")}
                      </Badge>
                      {b.hasDraft && b.state !== "draft" && (
                        <Badge variant="light" color="yellow" size="sm">
                          {t("brands.unpublishedChanges")}
                        </Badge>
                      )}
                    </Group>
                  </Table.Td>
                  <Table.Td>{ruleCounts.get(b.id) ?? 0}</Table.Td>
                  <Table.Td>
                    <Menu position="bottom-end" withinPortal>
                      <Menu.Target>
                        <ActionIcon variant="subtle" aria-label={t("brands.action.menu", { name: b.name })}>
                          <IconDots size={16} />
                        </ActionIcon>
                      </Menu.Target>
                      <Menu.Dropdown>
                        {!archived && <Menu.Item onClick={() => onSelect(b.id)}>{t("brands.action.edit")}</Menu.Item>}
                        {!archived && (
                          <Menu.Item onClick={() => duplicate.mutate(b)}>{t("brands.action.duplicate")}</Menu.Item>
                        )}
                        {b.state === "published" && !b.isDefault && (
                          <Menu.Item onClick={() => confirm("makeDefault", b, () => makeDefault.mutate(b))}>
                            {t("brands.action.makeDefault")}
                          </Menu.Item>
                        )}
                        <Menu.Item onClick={() => void exportBrand(b)}>{t("brands.action.export")}</Menu.Item>
                        {archived && (
                          <Menu.Item onClick={() => restore.mutate(b)}>{t("brands.action.restore")}</Menu.Item>
                        )}
                        {!archived && !b.isDefault && b.state !== "draft" && (
                          <Menu.Item color="red" onClick={() => confirm("archive", b, () => archive.mutate(b))}>
                            {t("brands.action.archive")}
                          </Menu.Item>
                        )}
                        {b.state === "draft" && !b.isDefault && b.publishedVersion === 0 && (
                          <Menu.Item color="red" onClick={() => confirm("delete", b, () => remove.mutate(b))}>
                            {t("brands.action.delete")}
                          </Menu.Item>
                        )}
                      </Menu.Dropdown>
                    </Menu>
                  </Table.Td>
                </Table.Tr>
              );
            })}
          </Table.Tbody>
        </Table>
      </Stack>

      <Modal opened={creating} onClose={() => setCreating(false)} title={t("brands.create.title")}>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate({ inputs: { name, title: name, primary, radius: "md" }, imported: false });
          }}
        >
          <Stack gap="sm">
            <TextInput
              label={t("brands.name")}
              description={t("brands.nameHelp")}
              value={name}
              maxLength={100}
              onChange={(e) => {
                setName(e.currentTarget.value);
                setNameError(undefined);
              }}
              error={nameError}
              data-autofocus
            />
            <ColorInput
              label={t("brands.primary")}
              description={t("brands.primaryHelp")}
              format="hex"
              value={primary}
              onChange={setPrimary}
              withEyeDropper={false}
            />
            <Group justify="flex-end">
              <Button
                type="submit"
                loading={create.isPending}
                disabled={!name.trim() || !/^#[0-9a-f]{6}$/i.test(primary)}
              >
                {t("brands.create.submit")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </PageSection>
  );
};
