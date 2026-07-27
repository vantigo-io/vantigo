using Projects;

using Scalar.Aspire;

var builder = DistributedApplication.CreateBuilder(args);

// Services
var postgres = builder.AddPostgres("postgres")
    .WithDataVolume()
    .WithPgAdmin();

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

// Scalar
var scalar = builder.AddScalarApiReference()
    .WithApiReference(customersApi);

builder.Build().Run();
