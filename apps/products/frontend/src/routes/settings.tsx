import { Alert, Badge, Button, Card, Group, Select, Stack, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, redirect } from "@tanstack/react-router";
import { useState } from "react";
import {
  createInvitation,
  disableMfa,
  enableMfa,
  initializeMfa,
  invitationAction,
  listInvitations,
  mfaSetup,
  mfaStatus,
} from "../api/account-lifecycle";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { showLifecycleFormError } from "../lib/lifecycle-form-errors";

const SettingsPage = () => {
  const qc = useQueryClient();
  const session = qc.getQueryData<Awaited<ReturnType<typeof fetchSession>>>(sessionQueryKey);
  const invitations = useQuery({ queryKey: ["invitations"], queryFn: listInvitations });
  const mfa = useQuery({ queryKey: ["mfa"], queryFn: mfaStatus });
  const setup = useQuery({ queryKey: ["mfa-setup"], queryFn: mfaSetup, enabled: false });
  const [recovery, setRecovery] = useState<string[]>([]);
  const invite = useForm({ initialValues: { email: "", displayName: "", role: "User" as "User" | "Owner" } });
  const create = useMutation({
    mutationFn: createInvitation,
    onSuccess: () => {
      invite.reset();
      void invitations.refetch();
    },
    onError: (error) => showLifecycleFormError(error, invite, "Invitation could not be sent"),
  });
  const action = useMutation({
    mutationFn: ({ id, kind }: { id: string; kind: "revoke" | "resend" }) => invitationAction(id, kind),
    onSuccess: () => void invitations.refetch(),
  });
  const code = useForm({ initialValues: { value: "" } });
  const enable = useMutation({
    mutationFn: () => enableMfa(code.values.value),
    onSuccess: (result) => {
      setRecovery(result.recoveryCodes);
      void mfa.refetch();
    },
  });
  return (
    <Stack maw={920} mx="auto">
      <Title order={2}>Account settings</Title>
      <Card withBorder>
        <Stack>
          <Title order={4}>Invite teammates</Title>
          <form onSubmit={invite.onSubmit((values) => create.mutate(values))}>
            <Group align="end">
              <TextInput label="Email" required {...invite.getInputProps("email")} />
              <TextInput label="Display name" {...invite.getInputProps("displayName")} />
              <Select label="Role" data={["User", "Owner"]} {...invite.getInputProps("role")} />
              <Button type="submit" loading={create.isPending}>
                Send invite
              </Button>
            </Group>
          </form>
          {invitations.data?.map((item) => (
            <Group key={item.id} justify="space-between">
              <Text>{item.displayName || item.email}</Text>
              <Badge>{item.role}</Badge>
              <Text size="sm" c="dimmed">
                {item.acceptedAt ? "Accepted" : item.revokedAt ? "Revoked" : "Pending"}
              </Text>
              {!item.acceptedAt && !item.revokedAt && (
                <>
                  <Button size="xs" variant="subtle" onClick={() => action.mutate({ id: item.id, kind: "resend" })}>
                    Resend
                  </Button>
                  <Button
                    size="xs"
                    color="red"
                    variant="subtle"
                    onClick={() => action.mutate({ id: item.id, kind: "revoke" })}
                  >
                    Revoke
                  </Button>
                </>
              )}
            </Group>
          ))}
        </Stack>
      </Card>
      <Card withBorder>
        <Stack>
          <Title order={4}>Two-factor authentication</Title>
          <Text c="dimmed">Protect your account with an authenticator app.</Text>
          <Text>{mfa.data?.twoFactorEnabled ? "Enabled" : "Not enabled"}</Text>
          {!mfa.data?.twoFactorEnabled && (
            <Button
              onClick={() => {
                void initializeMfa().then(() => setup.refetch());
              }}
            >
              Start setup
            </Button>
          )}
          {setup.data?.authenticatorUri && (
            <Text size="sm" style={{ wordBreak: "break-all" }}>
              Manual setup URI: {setup.data.authenticatorUri}
            </Text>
          )}
          {setup.data?.initialized && (
            <form
              onSubmit={(event) => {
                event.preventDefault();
                enable.mutate();
              }}
            >
              <Group>
                <TextInput label="Authenticator code" {...code.getInputProps("value")} />
                <Button type="submit">Enable MFA</Button>
              </Group>
            </form>
          )}
          {recovery.length > 0 && <Alert title="Save these recovery codes now">{recovery.join(" · ")}</Alert>}
          {session?.user.roles.includes("Owner") && mfa.data?.twoFactorEnabled && (
            <Button
              color="red"
              variant="light"
              onClick={() => {
                if (window.confirm("Disable MFA for this account?"))
                  void disableMfa(window.prompt("Enter your current password") ?? "");
              }}
            >
              Disable MFA
            </Button>
          )}
        </Stack>
      </Card>
    </Stack>
  );
};
export const Route = createFileRoute("/settings")({
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.fetchQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
    if (!session?.user.roles.includes("Owner")) throw redirect({ to: "/" });
  },
  component: SettingsPage,
});
