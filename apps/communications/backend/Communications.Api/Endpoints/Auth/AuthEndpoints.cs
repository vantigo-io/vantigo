using System.Security.Claims;
using System.Security.Cryptography;
using System.Text;

using Microsoft.AspNetCore.Antiforgery;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;

using Vantigo.Communications.Api.Database.Accounts;
using Vantigo.Communications.Api.Services;

namespace Vantigo.Communications.Api.Endpoints.Auth;

internal static class AuthEndpoints
{
    internal static IEndpointRouteBuilder MapAuthEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("/auth").WithTags("Authentication");
        group.MapGet("/antiforgery", (HttpContext context, IAntiforgery antiforgery) =>
        {
            var tokens = antiforgery.GetAndStoreTokens(context);
            return TypedResults.Ok(new AntiforgeryResponse(tokens.RequestToken!));
        });
        group.MapGet("/bootstrap-status", BootstrapStatus);
        group.MapPost("/bootstrap", Bootstrap).RequireAntiforgery();
        group.MapPost("/login", Login).RequireAntiforgery();
        group.MapPost("/logout", Logout).RequireAuthorization().RequireAntiforgery();
        group.MapGet("/session", Session).RequireAuthorization();
        return app;
    }

    private static async Task<IResult> BootstrapStatus(BootstrapSecretProvider secret, AccountsDbContext db, CancellationToken cancellationToken) =>
        TypedResults.Ok(new BootstrapStatusResponse(!await db.BootstrapStates.AnyAsync(cancellationToken)));

    private static async Task<IResult> Bootstrap(
        BootstrapRequest? request, BootstrapSecretProvider secret, AccountsDbContext db,
        UserManager<ApplicationUser> users, RoleManager<IdentityRole<Guid>> roles,
        SignInManager<ApplicationUser> signIn, CancellationToken cancellationToken)
    {
        if (request is null || string.IsNullOrWhiteSpace(request.Secret) || string.IsNullOrWhiteSpace(request.Email) ||
            !request.Email.Contains('@', StringComparison.Ordinal) || string.IsNullOrWhiteSpace(request.DisplayName) ||
            string.IsNullOrWhiteSpace(request.Password))
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The bootstrap request is invalid.");
        if (!FixedTimeEquals(secret.Secret, request.Secret)) return Error(StatusCodes.Status401Unauthorized, "invalid_secret", "The bootstrap secret is invalid.");

        await using var transaction = await db.Database.BeginTransactionAsync(System.Data.IsolationLevel.Serializable, cancellationToken);
        if (await db.BootstrapStates.AnyAsync(cancellationToken)) return Error(StatusCodes.Status409Conflict, "bootstrap_unavailable", "The Owner already exists.");
        var ownerRole = await roles.FindByNameAsync(AuthRoles.Owner) ?? new IdentityRole<Guid>(AuthRoles.Owner);
        if (ownerRole.Id == Guid.Empty) ownerRole.Id = Guid.NewGuid();
        if (ownerRole.Id != Guid.Empty && ownerRole.Id != default && await roles.FindByNameAsync(AuthRoles.Owner) is null)
        {
            var roleResult = await roles.CreateAsync(ownerRole);
            if (!roleResult.Succeeded) return IdentityError(roleResult);
        }
        var userRole = await roles.FindByNameAsync(AuthRoles.User) ?? new IdentityRole<Guid>(AuthRoles.User);
        if (await roles.FindByNameAsync(AuthRoles.User) is null)
        {
            var roleResult = await roles.CreateAsync(userRole);
            if (!roleResult.Succeeded) return IdentityError(roleResult);
        }
        var user = new ApplicationUser { UserName = request.Email.Trim(), Email = request.Email.Trim(), DisplayName = request.DisplayName.Trim() };
        var create = await users.CreateAsync(user, request.Password);
        if (!create.Succeeded) return IdentityError(create);
        var addRole = await users.AddToRoleAsync(user, AuthRoles.Owner);
        if (!addRole.Succeeded) return IdentityError(addRole);
        db.BootstrapStates.Add(new BootstrapState { Id = 1, CompletedAt = DateTimeOffset.UtcNow });
        await db.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        await signIn.SignInAsync(user, false);
        return TypedResults.Created("/auth/session", new AuthSuccessResponse(ToResponse(user, [AuthRoles.Owner])));
    }

    private static async Task<IResult> Login(LoginRequest? request, SignInManager<ApplicationUser> signIn, UserManager<ApplicationUser> users)
    {
        if (string.IsNullOrWhiteSpace(request?.Email) || string.IsNullOrWhiteSpace(request.Password))
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "Email and password are required.");
        var user = await users.FindByEmailAsync(request.Email.Trim());
        if (user is null || !(await signIn.CheckPasswordSignInAsync(user, request.Password, true)).Succeeded)
            return Error(StatusCodes.Status401Unauthorized, "invalid_credentials", "Invalid email or password.");
        await signIn.SignInAsync(user, false);
        return TypedResults.Ok(new AuthSuccessResponse(ToResponse(user, (await users.GetRolesAsync(user)).ToArray())));
    }

    private static async Task<IResult> Logout(SignInManager<ApplicationUser> signIn)
    {
        await signIn.SignOutAsync();
        return TypedResults.Ok(new LogoutResponse(true));
    }

    private static async Task<IResult> Session(ClaimsPrincipal principal, UserManager<ApplicationUser> users)
    {
        var user = await users.GetUserAsync(principal);
        return user is null ? Error(StatusCodes.Status401Unauthorized, "unauthenticated", "The session is not authenticated.") :
            TypedResults.Ok(new AuthSessionResponse(ToResponse(user, (await users.GetRolesAsync(user)).ToArray())));
    }

    private static AuthUserResponse ToResponse(ApplicationUser user, IReadOnlyCollection<string> roles) => new(user.Id, user.DisplayName, user.Email, roles);
    private static bool FixedTimeEquals(string configured, string supplied)
    {
        var left = Encoding.UTF8.GetBytes(configured);
        var right = Encoding.UTF8.GetBytes(supplied);
        return left.Length == right.Length && CryptographicOperations.FixedTimeEquals(left, right);
    }
    private static IResult IdentityError(IdentityResult result) => Error(StatusCodes.Status400BadRequest, "identity_validation_failed", string.Join(" ", result.Errors.Select(error => error.Description)));
    private static IResult Error(int status, string code, string message) => TypedResults.Json(new AuthErrorResponse(new AuthError(code, message)), statusCode: status);
}