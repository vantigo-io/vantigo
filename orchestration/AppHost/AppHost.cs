using Projects;

using Scalar.Aspire;

var builder = DistributedApplication.CreateBuilder(args);

var rootInstaller = builder.AddExecutable("bun-install", "bun", "../../", "install", "--frozen-lockfile");

// Services
var postgres = builder.AddPostgres("postgres")
    .WithDataVolume()
    .WithPgAdmin();

// This stays server-side: each remote-application integration receives an
// explicit Enabled gate and the matching secret. Neither value reaches a Vite app.
var communicationsCustomersApiKey = builder.AddParameter("communications-customers-api-key", secret: true);

// Customers
var customersDb = postgres.AddDatabase("customers-db", "customers");

// Customers lifecycle: database → migrate → seed → API.
var customersMigrate = builder
    .AddProject<Customers_Api>("customers-migrate", "migrate")
    .WithReference(customersDb, connectionName: "Postgresql")
    .WaitFor(customersDb);

var customersSeed = builder
    .AddProject<Customers_Api>("customers-seed", "seed")
    .WithReference(customersDb, connectionName: "Postgresql")
    .WaitForCompletion(customersMigrate);

var customersApi = builder
    .AddProject<Customers_Api>("customers-api", "api")
    .WithOtlpExporter()
    .WithReference(customersDb, connectionName: "Postgresql")
    .WaitForCompletion(customersSeed);

var customersFrontend = builder.AddViteApp("customers-frontend", "../../apps/customers/frontend")
    .WithBun(install: false)
    .WithReference(customersApi)
    .WaitFor(customersApi)
    .WaitForCompletion(rootInstaller);

// Communications
var communicationsDb = postgres.AddDatabase("communications-db", "communications");

// Communications lifecycle: database → migrate → seed → API.
var communicationsMigrate = builder
    .AddProject<Communications_Api>("communications-migrate", "migrate")
    .WithReference(communicationsDb, connectionName: "Postgresql")
    .WaitFor(communicationsDb);

var communicationsSeed = builder
    .AddProject<Communications_Api>("communications-seed", "seed")
    .WithReference(communicationsDb, connectionName: "Postgresql")
    .WaitForCompletion(communicationsMigrate);

var communicationsApi = builder
    .AddProject<Communications_Api>("communications-api", "api")
    .WithOtlpExporter()
    .WithEnvironment("Customers__Enabled", "true")
    .WithEnvironment("Customers__ApiKey", communicationsCustomersApiKey)
    .WithReference(communicationsDb, connectionName: "Postgresql")
    .WaitForCompletion(communicationsSeed);

var communicationsFrontend = builder.AddViteApp("communications-frontend", "../../apps/communications/frontend")
    .WithBun(install: false)
    .WithReference(communicationsApi)
    .WaitFor(communicationsApi)
    .WaitForCompletion(rootInstaller);

// Products
var productsDb = postgres.AddDatabase("products-db", "products");

// Products lifecycle: database → migrate → seed → API.
var productsMigrate = builder
    .AddProject<Products_Api>("products-migrate", "migrate")
    .WithReference(productsDb, connectionName: "Postgresql")
    .WaitFor(productsDb);

var productsSeed = builder
    .AddProject<Products_Api>("products-seed", "seed")
    .WithReference(productsDb, connectionName: "Postgresql")
    .WaitForCompletion(productsMigrate);

var productsApi = builder
    .AddProject<Products_Api>("products-api", "api")
    .WithOtlpExporter()
    .WithReference(productsDb, connectionName: "Postgresql")
    .WaitForCompletion(productsSeed);

var productsFrontend = builder.AddViteApp("products-frontend", "../../apps/products/frontend")
    .WithBun(install: false)
    .WithReference(productsApi)
    .WaitFor(productsApi)
    .WaitForCompletion(rootInstaller);

// Prepare Customers' server-side configuration and service discovery for its
// future Communications client. Neither value is referenced by the Vite app.
customersApi
    .WithReference(communicationsApi)
    .WithEnvironment("Communications__Enabled", "true")
    .WithEnvironment("Communications__ApiKey", communicationsCustomersApiKey);

// Scalar
var scalar = builder.AddScalarApiReference()
    .WithApiReference(customersApi)
    .WithApiReference(communicationsApi)
    .WithApiReference(productsApi);

builder.Build().Run();