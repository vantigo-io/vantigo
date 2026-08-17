import { Alert, Button, Card, Checkbox, Group, Radio, Stack, Stepper, Text, TextInput, Title } from "@mantine/core";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { createSystemTenant, normalizeSlug, SYSTEM_MODULES, systemTenantError } from "../../../api/system-tenants";
import { translateSystemModule } from "../../../i18n";
import "../../../i18n";

export const Route = createFileRoute("/admin/tenants/new")({ component: NewTenant });

function NewTenant() {
  const { t } = useI18n("host");
  const qc = useQueryClient();
  const [step, setStep] = useState(0);
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [modules, setModules] = useState<string[]>(["customers"]);
  const [mode, setMode] = useState("invite");
  const [email, setEmail] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [result, setResult] = useState<{ token?: string; id?: string }>();
  const create = useMutation({
    mutationFn: () =>
      createSystemTenant({
        name,
        slug,
        enabledModules: modules,
        ...(mode === "invite" ? { adminEmail: email, adminDisplayName: displayName } : { adminUserId: "" }),
      }),
    onSuccess: (value) => {
      setResult({ token: value.seededAdminInvitationToken, id: value.tenant.id });
      void qc.invalidateQueries({ queryKey: ["system-tenants"] });
    },
  });
  const valid = name.trim() && normalizeSlug(slug) === slug && slug.length > 0 && modules.length > 0;
  if (result)
    return (
      <Card withBorder maw={700}>
        <Stack>
          <Title order={2}>{t("systemAdmin.tenantCreated")}</Title>
          {result.token && (
            <Alert color="yellow" title={t("systemAdmin.invitationTokenShownOnce")}>
              {t("systemAdmin.copyLinkNow")}{" "}
              <Text
                ff="monospace"
                style={{ wordBreak: "break-all" }}
              >{`${window.location.origin}/accept-invitation?token=${encodeURIComponent(result.token)}`}</Text>
            </Alert>
          )}
          <Button component={Link} to="/admin/tenants/$tenantId" params={{ tenantId: result.id ?? "" } as never}>
            {t("systemAdmin.openTenant")}
          </Button>
        </Stack>
      </Card>
    );
  return (
    <Stack maw={760}>
      <Title order={1}>{t("systemAdmin.newTenant")}</Title>
      <Stepper active={step} onStepClick={setStep}>
        <Stepper.Step label={t("systemAdmin.identity")}>
          <Stack>
            <TextInput
              label={t("systemAdmin.name")}
              value={name}
              onChange={(e) => {
                setName(e.currentTarget.value);
                setSlug(normalizeSlug(e.currentTarget.value));
              }}
            />
            <TextInput
              label={t("systemAdmin.slug")}
              value={slug}
              onChange={(e) => setSlug(normalizeSlug(e.currentTarget.value))}
              description={t("systemAdmin.lowercaseUrlSafeSlug")}
            />
          </Stack>
        </Stepper.Step>
        <Stepper.Step label={t("systemAdmin.moduleSelection")}>
          <Checkbox.Group value={modules} onChange={setModules}>
            <Stack>
              {SYSTEM_MODULES.map((module) => (
                <Checkbox key={module} value={module} label={translateSystemModule(module, t)} />
              ))}
            </Stack>
          </Checkbox.Group>
        </Stepper.Step>
        <Stepper.Step label={t("systemAdmin.administrator")}>
          <Radio.Group value={mode} onChange={setMode}>
            <Stack>
              <Radio value="invite" label={t("systemAdmin.inviteByEmail")} />
              <Radio value="existing" label={t("systemAdmin.existingUserId")} />
            </Stack>
          </Radio.Group>
          {mode === "invite" ? (
            <>
              <TextInput
                label={t("systemAdmin.email")}
                value={email}
                onChange={(e) => setEmail(e.currentTarget.value)}
              />
              <TextInput
                label={t("systemAdmin.displayName")}
                value={displayName}
                onChange={(e) => setDisplayName(e.currentTarget.value)}
              />
            </>
          ) : (
            <TextInput label={t("systemAdmin.userId")} />
          )}
        </Stepper.Step>
        <Stepper.Completed>
          <Text>{t("systemAdmin.reviewTenant", { name, slug, count: modules.length })}</Text>
        </Stepper.Completed>
      </Stepper>
      <Group>
        <Button variant="default" disabled={step === 0} onClick={() => setStep(step - 1)}>
          {t("systemAdmin.back")}
        </Button>
        {step < 3 ? (
          <Button disabled={step === 0 && !valid} onClick={() => setStep(step + 1)}>
            {t("systemAdmin.next")}
          </Button>
        ) : (
          <Button loading={create.isPending} onClick={() => create.mutate()}>
            {t("systemAdmin.createTenant")}
          </Button>
        )}
      </Group>
      {create.error && <Alert color="red">{systemTenantError(create.error)}</Alert>}
    </Stack>
  );
}
