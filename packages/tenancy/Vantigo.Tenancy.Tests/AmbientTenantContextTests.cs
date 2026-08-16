using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Tenancy.Tests;

public sealed class AmbientTenantContextTests
{
    [Fact]
    public void Enter_and_dispose_restore_the_previous_tenant()
    {
        var first = TenantId.New();
        var second = TenantId.New();
        var context = new AmbientTenantContext();

        Assert.False(context.IsResolved);
        using (AmbientTenantContext.Enter(first))
        {
            Assert.Equal(first, context.Current);

            using (AmbientTenantContext.Enter(second))
                Assert.Equal(second, context.Current);

            Assert.Equal(first, context.Current);
        }

        Assert.False(context.IsResolved);
    }
}