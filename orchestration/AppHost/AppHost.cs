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

var minioRootUser = builder.AddParameter("minio-root-user", "minioadmin", secret: true);
var minioRootPassword = builder.AddParameter("minio-root-password", "minioadmin", secret: true);
var minio = builder.AddContainer("minio", "minio/minio")
    .WithArgs("server", "/data", "--console-address", ":9001")
    .WithEnvironment("MINIO_ROOT_USER", minioRootUser)
    .WithEnvironment("MINIO_ROOT_PASSWORD", minioRootPassword)
    .WithVolume("minio-data", "/data")
    .WithHttpEndpoint(targetPort: 9000, name: "api")
    .WithHttpEndpoint(targetPort: 9001, name: "console");

// The debian variant publishes multi-arch images (amd64/arm64); the alpine-based
// clamav/clamav:stable tag has no arm64 manifest and never starts on Apple Silicon.
var clamav = builder.AddContainer("clamav", "clamav/clamav-debian:stable")
    .WithEndpoint(targetPort: 3310, name: "clamav");

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
    .WithEnvironment("Storage__Provider", "s3")
    .WithEnvironment("Storage__Authentication", "access-key")
    .WithEnvironment("Storage__S3__BUCKET_NAME", "vantigo-objects")
    .WithEnvironment("Storage__S3__SERVICE_URL", minio.GetEndpoint("api"))
    .WithEnvironment("Storage__S3__ACCESS_KEY", minioRootUser)
    .WithEnvironment("Storage__S3__SECRET_KEY", minioRootPassword)
    .WithEnvironment("Storage__S3__FORCE_PATH_STYLE", "true")
    .WithEnvironment("Communications__Scanner__Host", clamav.GetEndpoint("clamav"))
    .WithEnvironment("Communications__Scanner__Port", "3310")
    .WaitFor(minio)
    .WaitFor(clamav)
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