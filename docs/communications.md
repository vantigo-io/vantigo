# Communications module deployment and integration

Communications is a module loaded by `Vantigo.Host`, not a separately deployed
service. It runs in the same process as Identity, Customers and Products and uses
the shared PostgreSQL database's `communications` schema. The production artifact
is the single `ghcr.io/vantigo-io/vantigo` image.

## Running the host

The host requires one command: `api`, `migrate`, or `seed`.

```bash
dotnet run --project apps/host/backend/Vantigo.Host --launch-profile migrate
dotnet run --project apps/host/backend/Vantigo.Host --launch-profile seed
dotnet run --project apps/host/backend/Vantigo.Host --launch-profile api
```

`migrate` applies all enabled module migrations and exits. `seed` is
Development-only. `api` hosts the application and does not migrate or seed.
Enable or disable the module with `Modules__Communications__Enabled`.

## In-process customer integration

Communications accesses customer data through the in-process
`Vantigo.Contracts.ICustomerDirectory` contract. There is no Customers service URL,
S2S API key or `X-Vantigo-Api-Key` environment variable. The host registers the
Customers module and its contract implementation when
`Modules__Customers__Enabled=true`.

Customer and Contact link values remain opaque domain values. Preserve them exactly;
do not derive meaning from them or use them as authorization credentials. The
in-process contract is responsible for authorized lookup and display of details.

## SMTP and Mailgun

The default Communications mailbox delivery uses the host's `Smtp:*` configuration:

```text
Smtp__Host=smtp.example.com
Smtp__Port=587
Smtp__Username=<smtp-user-from-secret-store>
Smtp__Password=<smtp-password-from-secret-store>
Smtp__UseSsl=false
Smtp__TimeoutSeconds=20
```

Mailgun is configured per mailbox through the Communications API with provider,
domain, region and API key fields. The API key is protected at rest; it is never
placed in frontend code, browser storage, URLs, logs or deployment environment
files. Mailgun credentials are not a service-to-service integration.

SMTP credentials must be kept in a secret store. SMTP is for outbound mail;
inbound mail synchronization and IMAP support are not implemented.

## Retention and module security

Use a conservative 12-month retention period unless the operator's policy requires
less. Configure `Communications__Retention__Days`, `BatchSize` and `PollMinutes`
as needed. Access controls and audit requirements must remain in force while data
is deleted.
