import {
  Alert,
  Badge,
  Button,
  Card,
  Grid,
  Group,
  NumberInput,
  Stack,
  Switch,
  Table,
  Text,
  Textarea,
  TextInput,
  Title,
  UnstyledButton,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconDeviceFloppy, IconPlus, IconTemplate } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ContentSkeleton, EmptyState, PageHeader } from "@vantigo/frontend-shell";
import { useState } from "react";
import { isVersionConflict, ORDERS_QUERY_KEY } from "../api/request";
import {
  createTemplate,
  deleteTemplate,
  templateListQueryOptions,
  templateQueryOptions,
  updateTemplate,
} from "../api/templates";
import type { Template } from "../api/types";
import { ConfirmDialog } from "../components/confirm-dialog";
import { ConflictAlert } from "../components/conflict-alert";
import { LineGrid } from "../components/line-grid";
import "../i18n";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import { refusalMessage } from "../lib/errors";
import { useOrdersFormat } from "../lib/format";
import { type EditableLine, fromTemplateLine, incompleteLines, toTemplateLineInput } from "../lib/lines";

/**
 * The quote templates (O23): every template by name, the archived ones on
 * request, and the chosen one's editor beside the list — its name, texts,
 * validity, whether the discount prints, and its lines: products with their
 * quantity, optional flag and an optional price override, sections and notes.
 * Editing takes orders:settings.
 */
export const TemplatesPage = ({ access = noOrdersAccess }: { access?: OrdersAccess }) => {
  const { t, date } = useOrdersFormat();
  const [archived, setArchived] = useState(false);
  const [selected, setSelected] = useState<number | "new" | null>(null);
  const list = useQuery(templateListQueryOptions(archived));
  const manage = access.canManageSettings;
  return (
    <Stack gap="lg">
      <PageHeader
        title={t("templatesTitle")}
        description={t("templatesDescription")}
        actions={
          manage && (
            <Button
              leftSection={<IconPlus size={16} />}
              onClick={() => setSelected("new")}
              data-assist="orders.templates.new"
            >
              {t("newTemplate")}
            </Button>
          )
        }
      />
      <Grid gap="lg">
        <Grid.Col span={{ base: 12, md: 4 }}>
          <Card withBorder padding="sm">
            <Stack gap="sm">
              <Switch
                label={t("showArchived")}
                checked={archived}
                onChange={(e) => setArchived(e.currentTarget.checked)}
              />
              {list.isPending && <ContentSkeleton rows={4} rowHeight={32} />}
              {list.isError && <Alert color="red">{refusalMessage(list.error, t)}</Alert>}
              {list.data?.length === 0 && (
                <EmptyState
                  size="sm"
                  icon={IconTemplate}
                  title={t("noTemplates")}
                  description={t("noTemplatesDescription")}
                />
              )}
              {list.data && list.data.length > 0 && (
                <Table highlightOnHover verticalSpacing={6} data-testid="template-list">
                  <Table.Tbody>
                    {list.data.map((template) => (
                      <Table.Tr
                        key={template.id}
                        bg={selected === template.id ? "var(--mantine-primary-color-light)" : undefined}
                      >
                        <Table.Td>
                          <UnstyledButton onClick={() => setSelected(template.id)} w="100%">
                            <Group justify="space-between" wrap="nowrap">
                              <Stack gap={0}>
                                <Text size="sm" fw={600}>
                                  {template.name}
                                </Text>
                                <Text size="xs" c="dimmed">
                                  {t("templateMeta", {
                                    count: template.lineCount,
                                    date: date(template.updatedAt.slice(0, 10)),
                                  })}
                                </Text>
                              </Stack>
                              {template.archived && (
                                <Badge size="xs" variant="light" color="gray">
                                  {t("archived")}
                                </Badge>
                              )}
                            </Group>
                          </UnstyledButton>
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              )}
            </Stack>
          </Card>
        </Grid.Col>
        <Grid.Col span={{ base: 12, md: 8 }}>
          {selected === null ? (
            <EmptyState icon={IconTemplate} title={t("pickTemplate")} description={t("pickTemplateDescription")} />
          ) : selected === "new" ? (
            <TemplateEditor key="new" access={access} onSaved={setSelected} onDeleted={() => setSelected(null)} />
          ) : (
            <LoadedTemplate id={selected} access={access} onSaved={setSelected} onDeleted={() => setSelected(null)} />
          )}
        </Grid.Col>
      </Grid>
    </Stack>
  );
};

interface EditorCallbacks {
  access: OrdersAccess;
  onSaved: (id: number) => void;
  onDeleted: () => void;
}

const LoadedTemplate = ({ id, ...callbacks }: { id: number } & EditorCallbacks) => {
  const { t } = useOrdersFormat();
  const template = useQuery({
    ...templateQueryOptions(id),
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
  });
  if (template.isPending) return <ContentSkeleton rows={5} rowHeight={40} />;
  if (template.isError) return <Alert color="red">{refusalMessage(template.error, t)}</Alert>;
  return (
    <TemplateEditor key={`${template.data.id}:${template.data.revision}`} template={template.data} {...callbacks} />
  );
};

const TemplateEditor = ({ template, access, onSaved, onDeleted }: { template?: Template } & EditorCallbacks) => {
  const { t } = useOrdersFormat();
  const queryClient = useQueryClient();
  const manage = access.canManageSettings;
  const [name, setName] = useState(template?.name ?? "");
  const [validityDays, setValidityDays] = useState<number | "">(template?.validityDays ?? "");
  const [discountVisible, setDiscountVisible] = useState(template?.discountVisible ?? true);
  const [introText, setIntroText] = useState(template?.introText ?? "");
  const [closingText, setClosingText] = useState(template?.closingText ?? "");
  const [isArchived, setArchived] = useState(template?.archived ?? false);
  const [lines, setLines] = useState<EditableLine[]>(() => (template?.lines ?? []).map(fromTemplateLine));
  const [dirty, setDirty] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [refused, setRefused] = useState<ReadonlyMap<number, string>>(new Map());
  const [nameError, setNameError] = useState<string>();
  const [deleting, setDeleting] = useState(false);
  const touch =
    <T,>(setter: (value: T) => void) =>
    (value: T) => {
      setter(value);
      setDirty(true);
    };

  const save = useMutation({
    mutationFn: () => {
      const input = {
        name: name.trim(),
        validityDays: validityDays === "" ? null : validityDays,
        discountVisible,
        introText,
        closingText,
        archived: isArchived,
        lines: lines.map(toTemplateLineInput),
      };
      return template ? updateTemplate(template.id, { ...input, revision: template.revision }) : createTemplate(input);
    },
    onSuccess: async (saved) => {
      queryClient.setQueryData(templateQueryOptions(saved.id).queryKey, saved);
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY, "templates", "list"] });
      notifications.show({ color: "green", message: t("templateSaved") });
      onSaved(saved.id);
    },
    onError: (error) => {
      if (isVersionConflict(error)) return setConflict(true);
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(error, t) });
    },
  });
  const remove = useMutation({
    mutationFn: () => deleteTemplate(template?.id ?? 0),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: [ORDERS_QUERY_KEY, "templates"] });
      notifications.show({ color: "green", message: t("templateDeleted") });
      onDeleted();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotDelete"), message: refusalMessage(error, t) }),
  });
  const submit = () => {
    if (name.trim() === "") return setNameError(t("templateNameRequired"));
    const problems = incompleteLines(lines, { template: true });
    setRefused(new Map([...problems].map(([index, key]) => [index, t(key)])));
    if (problems.size === 0) save.mutate();
  };

  return (
    <Card withBorder padding="lg" data-testid="template-editor">
      <Stack gap="md">
        <Group justify="space-between">
          <Title order={4}>{template ? template.name : t("newTemplate")}</Title>
          {manage && (
            <Group gap="xs">
              {template && (
                <Button variant="subtle" color="red" onClick={() => setDeleting(true)}>
                  {t("deleteTemplate")}
                </Button>
              )}
              <Button
                leftSection={<IconDeviceFloppy size={16} />}
                disabled={!dirty && template !== undefined}
                loading={save.isPending}
                onClick={submit}
              >
                {t("save")}
              </Button>
            </Group>
          )}
        </Group>
        {conflict && (
          <ConflictAlert
            onReload={() =>
              template && void queryClient.refetchQueries({ queryKey: templateQueryOptions(template.id).queryKey })
            }
          />
        )}
        <Group align="flex-start" grow wrap="wrap">
          <TextInput
            label={t("templateName")}
            required
            maxLength={200}
            disabled={!manage}
            value={name}
            error={nameError}
            onChange={(e) => {
              setNameError(undefined);
              touch(setName)(e.currentTarget.value);
            }}
          />
          <NumberInput
            label={t("validityDays")}
            description={t("validityDaysHint")}
            min={1}
            max={365}
            allowDecimal={false}
            disabled={!manage}
            value={validityDays}
            onChange={(value) => touch(setValidityDays)(value === "" ? "" : Number(value))}
          />
        </Group>
        <Group gap="lg">
          <Switch
            label={t("discountVisible")}
            disabled={!manage}
            checked={discountVisible}
            onChange={(e) => touch(setDiscountVisible)(e.currentTarget.checked)}
          />
          <Switch
            label={t("archived")}
            disabled={!manage || !template}
            checked={isArchived}
            onChange={(e) => touch(setArchived)(e.currentTarget.checked)}
          />
        </Group>
        <Group grow align="flex-start">
          <Textarea
            label={t("introText")}
            autosize
            minRows={2}
            maxLength={4000}
            disabled={!manage}
            value={introText}
            onChange={(e) => touch(setIntroText)(e.currentTarget.value)}
          />
          <Textarea
            label={t("closingText")}
            autosize
            minRows={2}
            maxLength={4000}
            disabled={!manage}
            value={closingText}
            onChange={(e) => touch(setClosingText)(e.currentTarget.value)}
          />
        </Group>
        <Title order={5}>{t("lines")}</Title>
        <LineGrid
          lines={lines}
          onChange={(next) => {
            touch(setLines)(next);
            setRefused(new Map());
          }}
          mode="template"
          currency="NOK"
          editable={manage}
          access={access}
          refused={refused}
        />
      </Stack>
      {deleting && template && (
        <ConfirmDialog
          title={t("deleteTemplate")}
          confirmLabel={t("deleteTemplate")}
          danger
          loading={remove.isPending}
          onConfirm={() => remove.mutate()}
          onClose={() => setDeleting(false)}
        >
          {t("deleteTemplateHint", { name: template.name })}
        </ConfirmDialog>
      )}
    </Card>
  );
};
