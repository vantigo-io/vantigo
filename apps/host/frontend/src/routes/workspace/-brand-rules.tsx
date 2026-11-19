import { ActionIcon, Alert, Badge, Box, Button, Group, Select, Stack, Text, TextInput, Tooltip } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconArrowDown, IconArrowUp, IconGripVertical, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageSection, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type BrandRule,
  type BrandRuleInput,
  type BrandRuleKind,
  type BrandRulesResponse,
  type BrandRuleTargets,
  type BrandSummary,
  brandRulesQueryKey,
  brandRuleTargetsQueryKey,
  getBrandRules,
  getBrandRuleTargets,
  saveBrandRules,
} from "../../api/brands";
import "../../i18n";
import { insertIndexFor, moveItem } from "./-brand-rules-order";

/** A rule as it is edited: the server's, or one just added (no id yet). */
export interface RuleDraft {
  key: string;
  brandId: string;
  kind: BrandRuleKind;
  targetRef: string;
  label: string;
  stale: boolean;
  staleReason?: BrandRule["staleReason"];
}

const draftOf = (r: BrandRule): RuleDraft => ({
  key: r.id,
  brandId: r.brandId,
  kind: r.kind,
  targetRef: r.targetRef,
  label: r.targetLabel,
  stale: r.stale,
  staleReason: r.staleReason,
});

const inputsOf = (items: readonly RuleDraft[]): BrandRuleInput[] =>
  items.map((i) => ({ brandId: i.brandId, kind: i.kind, targetRef: i.targetRef }));

type ErrorBody = { status?: number; code?: string; message?: string };

interface Props {
  brands: readonly BrandSummary[];
}

/**
 * The rule list (brands design B8, B21): ordered, the first match wins. Rules are dragged, or moved with
 * the arrows, and added by kind with a picker for the target; the whole list is saved at once at the
 * revision it was read at.
 */
export const BrandRules = ({ brands }: Props) => {
  const { t } = useI18n("host");
  const qc = useQueryClient();
  const rules = useQuery({ queryKey: brandRulesQueryKey, queryFn: getBrandRules });
  const targets = useQuery({ queryKey: brandRuleTargetsQueryKey, queryFn: getBrandRuleTargets });
  const usable = brands.filter((b) => b.state !== "archived");

  if (rules.isError || targets.isError) return <Alert color="red">{t("brands.loadFailed")}</Alert>;
  if (!rules.data || !targets.data) return null;
  return (
    <RuleEditor
      // A fresh list from the server (a save, a reload) restarts the editing from it.
      key={rules.data.revision}
      revision={rules.data.revision}
      initial={rules.data.items.map(draftOf)}
      staffEnabled={rules.data.staffEnabled}
      targets={targets.data}
      brands={usable}
      onSaved={(next) => qc.setQueryData(brandRulesQueryKey, next)}
    />
  );
};

interface EditorProps {
  revision: number;
  initial: RuleDraft[];
  staffEnabled: boolean;
  targets: BrandRuleTargets;
  brands: readonly BrandSummary[];
  onSaved: (next: BrandRulesResponse) => void;
}

const RuleEditor = ({ revision, initial, staffEnabled, targets, brands, onSaved }: EditorProps) => {
  const { t } = useI18n("host");
  const [items, setItems] = useState<RuleDraft[]>(initial);
  const [dragFrom, setDragFrom] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [kind, setKind] = useState<BrandRuleKind>("domain");
  const [domain, setDomain] = useState("");
  const [picked, setPicked] = useState<string | null>(null);
  const [brandId, setBrandId] = useState<string | null>(brands[0]?.id ?? null);

  const dirty = JSON.stringify(inputsOf(items)) !== JSON.stringify(inputsOf(initial));
  const brandName = (id: string) => brands.find((b) => b.id === id)?.name ?? id;
  const kinds = (["domain", ...(staffEnabled ? ["legal_entity"] : []), "access_group"] as BrandRuleKind[]).map((k) => ({
    value: k,
    label: t(`brands.rules.kind.${k}`),
  }));

  const save = useMutation({
    mutationFn: () => saveBrandRules(revision, inputsOf(items)),
    onSuccess: (next) => {
      setError(null);
      onSaved(next);
      notifications.show({ color: "green", message: t("brands.rules.saved") });
    },
    onError: (e) => {
      const code = (e as ErrorBody).code ?? "";
      const key = `brands.rules.error.${code}`;
      const text = t(key);
      setError(text === key ? ((e as ErrorBody).message ?? t("brands.error.generic")) : text);
    },
  });

  const move = (from: number, to: number) => setItems((prev) => moveItem(prev, from, to));
  const canAdd = brandId !== null && (kind === "domain" ? domain.trim() !== "" : picked !== null);
  const add = () => {
    if (!brandId) return;
    const entry: RuleDraft =
      kind === "domain"
        ? {
            key: `new-${crypto.randomUUID()}`,
            brandId,
            kind,
            targetRef: domain.trim(),
            label: domain.trim(),
            stale: false,
          }
        : {
            key: `new-${crypto.randomUUID()}`,
            brandId,
            kind,
            targetRef: picked as string,
            label:
              (kind === "access_group" ? targets.accessGroups : targets.legalEntities).find((x) => x.id === picked)
                ?.name ?? (picked as string),
            stale: false,
          };
    setItems((prev) => {
      const next = [...prev];
      next.splice(insertIndexFor(prev, kind), 0, entry);
      return next;
    });
    setDomain("");
    setPicked(null);
  };

  const options = kind === "access_group" ? targets.accessGroups : targets.legalEntities;

  return (
    <PageSection title={t("brands.rules.heading")}>
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          {t("brands.rules.intro")}
        </Text>
        {!staffEnabled && items.some((i) => i.kind === "legal_entity") && (
          <Alert color="gray">{t("brands.rules.staffOff")}</Alert>
        )}
        {items.length === 0 ? (
          <Text c="dimmed">{t("brands.rules.empty")}</Text>
        ) : (
          <Stack gap="xs" component="ol" m={0} p={0} style={{ listStyle: "none" }} data-testid="brand-rules">
            {items.map((item, i) => (
              <Box
                component="li"
                key={item.key}
                draggable
                onDragStart={(e: React.DragEvent) => {
                  setDragFrom(i);
                  e.dataTransfer?.setData("text/plain", String(i));
                }}
                onDragOver={(e: React.DragEvent) => e.preventDefault()}
                onDrop={(e: React.DragEvent) => {
                  e.preventDefault();
                  if (dragFrom !== null) move(dragFrom, i);
                  setDragFrom(null);
                }}
                onDragEnd={() => setDragFrom(null)}
                data-testid="brand-rule"
                data-target={item.targetRef}
                style={{
                  border: "1px solid var(--mantine-color-default-border)",
                  borderRadius: "var(--mantine-radius-default)",
                  padding: "var(--mantine-spacing-xs)",
                  opacity: dragFrom === i ? 0.5 : 1,
                }}
              >
                <Group gap="sm" wrap="nowrap">
                  <Tooltip label={t("brands.rules.drag", { target: item.label })}>
                    <IconGripVertical size={16} style={{ cursor: "grab", flexShrink: 0 }} aria-hidden />
                  </Tooltip>
                  <Text w={24} ta="right" c="dimmed" size="sm">
                    {i + 1}
                  </Text>
                  <Badge variant="light" color="gray" style={{ flexShrink: 0 }}>
                    {t(`brands.rules.kind.${item.kind}`)}
                  </Badge>
                  <Box style={{ flex: 1, minWidth: 0 }}>
                    <Text truncate fw={500}>
                      {item.label}
                    </Text>
                    {item.stale && item.staleReason && (
                      <Text size="xs" c="yellow.8" role="note">
                        {t(`brands.rules.stale.${item.staleReason}`)}
                      </Text>
                    )}
                  </Box>
                  <Select
                    aria-label={t("brands.rules.brandOf", { target: item.label })}
                    w={200}
                    allowDeselect={false}
                    data={brands.map((b) => ({ value: b.id, label: b.name }))}
                    value={item.brandId}
                    onChange={(v) => v && setItems((prev) => prev.map((x, j) => (j === i ? { ...x, brandId: v } : x)))}
                    placeholder={brandName(item.brandId)}
                  />
                  <ActionIcon
                    variant="subtle"
                    aria-label={t("brands.rules.moveUp", { target: item.label })}
                    disabled={i === 0}
                    onClick={() => move(i, i - 1)}
                  >
                    <IconArrowUp size={16} />
                  </ActionIcon>
                  <ActionIcon
                    variant="subtle"
                    aria-label={t("brands.rules.moveDown", { target: item.label })}
                    disabled={i === items.length - 1}
                    onClick={() => move(i, i + 1)}
                  >
                    <IconArrowDown size={16} />
                  </ActionIcon>
                  <ActionIcon
                    variant="subtle"
                    color="red"
                    aria-label={t("brands.rules.remove", { target: item.label })}
                    onClick={() => setItems((prev) => prev.filter((_, j) => j !== i))}
                  >
                    <IconTrash size={16} />
                  </ActionIcon>
                </Group>
              </Box>
            ))}
          </Stack>
        )}

        <Group align="flex-end" gap="sm" wrap="wrap">
          <Select
            label={t("brands.rules.kind")}
            w={180}
            allowDeselect={false}
            data={kinds}
            value={kind}
            onChange={(v) => {
              setKind((v as BrandRuleKind) ?? "domain");
              setPicked(null);
            }}
          />
          {kind === "domain" ? (
            <TextInput
              label={t("brands.rules.target")}
              description={t("brands.rules.domainHelp")}
              placeholder={t("brands.rules.domainPlaceholder")}
              value={domain}
              onChange={(e) => setDomain(e.currentTarget.value)}
            />
          ) : (
            <Select
              label={t("brands.rules.target")}
              w={260}
              searchable
              placeholder={t(kind === "access_group" ? "brands.rules.pickGroup" : "brands.rules.pickEntity")}
              data={options.filter((o) => o.available).map((o) => ({ value: o.id, label: o.name }))}
              value={picked}
              onChange={setPicked}
            />
          )}
          <Select
            label={t("brands.rules.brand")}
            w={200}
            allowDeselect={false}
            data={brands.map((b) => ({ value: b.id, label: b.name }))}
            value={brandId}
            onChange={setBrandId}
          />
          <Button variant="default" onClick={add} disabled={!canAdd}>
            {t("brands.rules.add")}
          </Button>
        </Group>

        {error && (
          <Alert color="red" role="alert">
            {error}
          </Alert>
        )}
        <Group>
          <Button onClick={() => save.mutate()} loading={save.isPending} disabled={!dirty}>
            {t("brands.rules.save")}
          </Button>
          <Button
            variant="default"
            onClick={() => {
              setItems(initial);
              setError(null);
            }}
            disabled={!dirty || save.isPending}
          >
            {t("brands.rules.revert")}
          </Button>
        </Group>
      </Stack>
    </PageSection>
  );
};
