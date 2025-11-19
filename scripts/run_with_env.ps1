param(
    [string]$EnvFile = ".env",
    [string[]]$Command = @("go", "run", "./cmd/server")
)

if (-not (Test-Path $EnvFile)) {
    Write-Error "Environment file '$EnvFile' not found."
    exit 1
}

Get-Content $EnvFile | ForEach-Object {
    $line = $_.Trim()
    if (-not $line -or $line.StartsWith("#")) {
        return
    }
    $parts = $line.Split("=", 2)
    if ($parts.Count -ne 2) {
        Write-Warning "Skipping invalid line: $line"
        return
    }
    $name = $parts[0].Trim()
    $value = $parts[1]
    $value = $value.Trim('"')
    Set-Item -Path "Env:$name" -Value $value
}

Write-Host "Loaded environment from $EnvFile" -ForegroundColor Cyan
Write-Host "Running command: $($Command -join ' ')" -ForegroundColor Green

& $Command[0] @($Command[1..($Command.Length - 1)])
$exitCode = $LASTEXITCODE
exit $exitCode
