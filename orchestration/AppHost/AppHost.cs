using Projects;

using Scalar.Aspire;

var builder = DistributedApplication.CreateBuilder(args);

// Services
var postgres = builder.AddPostgres("postgres")
    .WithDataVolume()
    .WithPgAdmin();

// This stays server-side: each remote-application integration receives an
// explicit Enabled gate and the matching secret. Neither value reaches a Vite app.
var communicationsCustomersApiKey = builder.AddParameter("communications-customers-api-key", secret: true);

// Customers
var customersDb = postgres.AddDatabase("customers-db", "customers");

var customersApi = builder
    .AddProject<Customers_Api>("customers-api", "dev")
    .WithOtlpExporter()
    .WithReference(customersDb, connectionName: "Postgresql")
    .WaitFor(customersDb);

var customersFrontend = builder.AddViteApp("customers-frontend", "../../apps/customers/frontend")
    .WithBun()
    .WithReference(customersApi)
    .WaitFor(customersApi);

// Communications
var communicationsDb = postgres.AddDatabase("communications-db", "communications");

var communicationsApi = builder
    .AddProject<Communications_Api>("communications-api", "dev")
    .WithOtlpExporter()
    .WithEnvironment("Customers__Enabled", "true")
    .WithEnvironment("Customers__ApiKey", communicationsCustomersApiKey)
    .WithReference(communicationsDb, connectionName: "Postgresql")
    .WaitFor(communicationsDb);

var communicationsFrontend = builder.AddViteApp("communications-frontend", "../../apps/communications/frontend")
    .WithBun()
    .WithReference(communicationsApi)
    .WaitFor(communicationsApi);

// Prepare Customers' server-side configuration and service discovery for its
// future Communications client. Neither value is referenced by the Vite app.
customersApi
    .WithReference(communicationsApi)
    .WithEnvironment("Communications__Enabled", "true")
    .WithEnvironment("Communications__ApiKey", communicationsCustomersApiKey);

// Scalar
var scalar = builder.AddScalarApiReference()
    .WithApiReference(customersApi)
    .WithApiReference(communicationsApi);

builder.Build().Run();