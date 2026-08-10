using Projects;

using Scalar.Aspire;

var builder = DistributedApplication.CreateBuilder(args);

var rootInstaller = builder.AddExecutable("bun-install", "bun", "../../", "install", "--frozen-lockfile");

// Services
var postgres = builder.AddPostgres("postgres")
    .WithDataVolume()
    .WithPgAdmin();

// One shared PostgreSQL database is partitioned by module schema.
var vantigoDb = postgres.AddDatabase("vantigo-db", "vantigo");

// Host lifecycle: database → migrate → seed → API.
var vantigoMigrate = builder
    .AddProject<Vantigo_Host>("vantigo-migrate", "migrate")
    .WithReference(vantigoDb, connectionName: "vantigo")
    .WaitFor(vantigoDb);

var vantigoSeed = builder
    .AddProject<Vantigo_Host>("vantigo-seed", "seed")
    .WithReference(vantigoDb, connectionName: "vantigo")
    .WaitForCompletion(vantigoMigrate);

var vantigoApi = builder
    .AddProject<Vantigo_Host>("vantigo-api", "api")
    .WithOtlpExporter()
    .WithReference(vantigoDb, connectionName: "vantigo")
    .WaitForCompletion(vantigoSeed);

var hostFrontend = builder.AddViteApp("vantigo-frontend", "../../apps/host/frontend")
    .WithBun(install: false)
    .WithReference(vantigoApi)
    .WaitFor(vantigoApi)
    .WaitForCompletion(rootInstaller);

// Scalar
var scalar = builder.AddScalarApiReference()
    .WithApiReference(vantigoApi)
    ;

builder.Build().Run();