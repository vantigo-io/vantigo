import { Alert, Button, Group, List, Stack, Text, TextInput } from "@mantine/core";
import { useMutation } from "@tanstack/react-query";
import { PageSection, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type BrandResolution, lookupBrandResolution } from "../../api/brands";
import "../../i18n";

/**
 * Who sees what (brands design B11, B21): name a person by e-mail address and see the brand they get and
 * why. It shows only the rule that matched; the person's groups and legal entities appear only when the
 * server sent them, which it does for a caller who may also read employment in Staff.
 */
export const BrandLookup = () => {
  const { t } = useI18n("host");
  const [email, setEmail] = useState("");
  const [notFound, setNotFound] = useState(false);
  const lookup = useMutation({
    mutationFn: () => lookupBrandResolution(email.trim()),
    onMutate: () => setNotFound(false),
    onError: (e) => setNotFound((e as { status?: number }).status === 404),
  });
  const result: BrandResolution | undefined = lookup.data;
  const none = t("brands.lookup.none");
  const kindOf = (r: BrandResolution) => (r.rule ? t(`brands.rules.kind.${r.rule.kind}`) : "");

  return (
    <PageSection title={t("brands.lookup.heading")}>
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          {t("brands.lookup.intro")}
        </Text>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (email.trim()) lookup.mutate();
          }}
        >
          <Group align="flex-end">
            <TextInput
              label={t("brands.lookup.email")}
              type="email"
              value={email}
              onChange={(e) => setEmail(e.currentTarget.value)}
              w={320}
            />
            <Button type="submit" variant="default" loading={lookup.isPending} disabled={!email.trim()}>
              {t("brands.lookup.submit")}
            </Button>
          </Group>
        </form>
        {notFound && <Alert color="gray">{t("brands.lookup.notFound")}</Alert>}
        {lookup.isError && !notFound && <Alert color="red">{t("brands.error.generic")}</Alert>}
        {result && (
          <Stack gap={4} data-testid="brand-lookup-result">
            <Text fw={500}>{t("brands.lookup.result", { name: result.displayName, brand: result.brand.name })}</Text>
            <Text size="sm">
              {result.source === "choice" && t("brands.lookup.reason.choice")}
              {result.source === "rule" &&
                result.rule &&
                t("brands.lookup.reason.rule", {
                  position: result.rule.position,
                  kind: kindOf(result),
                  target: result.rule.targetLabel,
                })}
              {result.source === "default" && t("brands.lookup.reason.default")}
            </Text>
            {result.source === "choice" && result.rule && (
              <Text size="sm" c="dimmed">
                {t("brands.lookup.rule", {
                  position: result.rule.position,
                  kind: kindOf(result),
                  target: result.rule.targetLabel,
                })}
              </Text>
            )}
            {result.candidates.length > 1 && (
              <Text size="sm" c="dimmed">
                {t("brands.lookup.candidates", { names: result.candidates.map((c) => c.name).join(", ") })}
              </Text>
            )}
            {result.choiceIgnored && (
              <Text size="sm" c="dimmed">
                {t("brands.lookup.ignored")}
              </Text>
            )}
            {result.details && (
              <Stack gap={2} mt="xs" data-testid="brand-lookup-details">
                <Text size="sm" fw={500}>
                  {t("brands.lookup.details")}
                </Text>
                <List size="sm" listStyleType="none" withPadding={false}>
                  <List.Item>{t("brands.lookup.domain", { domain: result.details.emailDomain || none })}</List.Item>
                  <List.Item>
                    {t("brands.lookup.groups", { names: result.details.accessGroups.join(", ") || none })}
                  </List.Item>
                  <List.Item>
                    {t("brands.lookup.entities", { names: result.details.legalEntities.join(", ") || none })}
                  </List.Item>
                </List>
              </Stack>
            )}
          </Stack>
        )}
      </Stack>
    </PageSection>
  );
};
