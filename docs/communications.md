# Communications deployment and integration

Communications can be deployed independently of the Aspire AppHost. Run its API
against a PostgreSQL database dedicated to Communications and deploy its Vite
frontend through the hosting path used for that deployment. Aspire is the local
composition that wires the `communications` logical PostgreSQL database, API, and
frontend together; it is not a requirement for a standalone deployment.

## Customers service integration

Configure the Customers integration in Communications as `Customers:Enabled` and
`Customers:ApiKey` (or `Customers__Enabled` and `Customers__ApiKey`). The key is
ignored unless `Enabled` is true, and the service-key authentication path fails
closed when the integration is disabled or the key is absent. Customers should
receive its future client configuration as `Communications:Enabled` and
`Communications:ApiKey` (or `Communications__Enabled` and `Communications__ApiKey`).
These settings group integrations by remote application; they do not create a
runtime dependency between standalone deployments. The future Customers service
client sends the key to Communications only as the `X-Vantigo-Api-Key` request
header. This key is a server-side secret: do not place it in frontend code, browser
storage, URLs, logs, or client-visible configuration, and do not commit it to source
control.

### Customer and Contact links

Customer and Contact links use an opaque link protocol. Treat each link value as an
uninterpreted value supplied by the Customers integration: preserve it exactly, do
not derive or expose meaning from it, and do not substitute a locally invented
identifier. Any lookup or display of Customer or Contact details must use the
authorized service integration rather than relying on the opaque value alone.

## SMTP

Provide the SMTP host, port, sender, and any required username/password through the
Communications deployment's supported configuration mechanism. Keep SMTP credentials
in a secret store and use the transport-security settings required by the provider.
SMTP is for outbound mail; this integration does not document inbound mail handling,
delivery guarantees, provider-specific retry behavior, or mailbox synchronization.
IMAP support is planned, but is not implemented.

## Retention and service security

Use a conservative 12-month retention period for Communications data unless a
shorter period is required by the operator's policy. Retention and deletion jobs
must be configured and operated without weakening access controls or audit needs.

Keep service-to-service traffic on private or otherwise authenticated paths, send
the Customers API key only over protected transport, scope credentials to the
minimum required service access, and rotate/revoke them through the deployment's
secret-management process. Do not treat the opaque Customer or Contact link as an
authorization credential; authorize every service operation independently.
