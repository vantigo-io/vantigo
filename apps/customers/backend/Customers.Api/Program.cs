var builder = WebApplication.CreateBuilder(args);

builder.Services.AddOpenApi();

var app = builder.Build();

app.MapOpenApi();

app.MapGet("/api/test", () => new []{"Hei", "På", "Deg", "!"});

app.Run();