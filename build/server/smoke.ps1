[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$ApplicationPath
)

$ErrorActionPreference = "Stop"
$root = Join-Path ([IO.Path]::GetTempPath()) ("beresta-server-smoke-" + [guid]::NewGuid().ToString("N"))
$serverProcess = $null
try {
    New-Item -ItemType Directory -Path $root | Out-Null
    @"
server:
  listen: "127.0.0.1:18444"
backups:
  enabled: false
"@ | Set-Content -LiteralPath (Join-Path $root "config.yaml") -Encoding UTF8
    & $ApplicationPath --data $root --init-only
    if ($LASTEXITCODE -ne 0) {
        throw "Server first-start smoke failed with exit code $LASTEXITCODE."
    }
    foreach ($relative in @("beresta.db", "blobs", "backups", "tls\server.crt", "tls\server.key")) {
        if (-not (Test-Path -LiteralPath (Join-Path $root $relative))) {
            throw "Server first-start smoke did not create $relative."
        }
    }
    $serverProcess = Start-Process -FilePath $ApplicationPath -ArgumentList "--data `"$root`"" -PassThru -WindowStyle Hidden
    $healthy = $false
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        if ($serverProcess.HasExited) {
            throw "Server exited before becoming healthy with code $($serverProcess.ExitCode)."
        }
        & curl.exe --silent --fail --insecure https://127.0.0.1:18444/health | Out-Null
        if ($LASTEXITCODE -eq 0) {
            $healthy = $true
            break
        }
        Start-Sleep -Milliseconds 250
        $serverProcess.Refresh()
    }
    if (-not $healthy) {
        throw "Server did not become healthy during the smoke-test deadline."
    }
    Stop-Process -Id $serverProcess.Id
    Wait-Process -Id $serverProcess.Id
    $serverProcess = $null
    & $ApplicationPath --data $root verify
    if ($LASTEXITCODE -ne 0) {
        throw "Server live-state verification failed with exit code $LASTEXITCODE."
    }
}
finally {
    if ($serverProcess -and -not $serverProcess.HasExited) {
        Stop-Process -Id $serverProcess.Id -Force -ErrorAction SilentlyContinue
        Wait-Process -Id $serverProcess.Id -ErrorAction SilentlyContinue
    }
    Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
}
