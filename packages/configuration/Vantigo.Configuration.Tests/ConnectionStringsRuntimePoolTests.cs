using Npgsql;

namespace Vantigo.Configuration.Tests;

public sealed class ConnectionStringsRuntimePoolTests
{
    [Fact]
    public void Runtime_resolution_applies_the_default_pool_budget()
    {
        var options = new ConnectionStringsOptions { Vantigo = "Host=db;Database=vantigo;Username=app" };

        var builder = new NpgsqlConnectionStringBuilder(options.ResolveRuntime());

        Assert.Equal(ConnectionStringsOptions.DefaultMaxPoolSize, builder.MaxPoolSize);
    }

    [Fact]
    public void An_explicit_pool_size_in_the_connection_string_wins()
    {
        var options = new ConnectionStringsOptions
        {
            Vantigo = "Host=db;Database=vantigo;Username=app;Maximum Pool Size=60",
        };

        var builder = new NpgsqlConnectionStringBuilder(options.ResolveRuntime());

        Assert.Equal(60, builder.MaxPoolSize);
    }

    [Fact]
    public void Plain_resolution_is_left_untouched_for_migrations()
    {
        var options = new ConnectionStringsOptions { Vantigo = "Host=db;Database=vantigo;Username=app" };

        Assert.Equal("Host=db;Database=vantigo;Username=app", options.Resolve());
    }
}