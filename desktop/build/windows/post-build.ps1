[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$ApplicationBinary
)

$ErrorActionPreference = "Stop"
$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$desktopProject = Get-Content -Raw -LiteralPath (Join-Path $projectRoot "desktop\wails.json") | ConvertFrom-Json
$version = $desktopProject.info.productVersion
$releasePublicKey = $env:BERESTA_UPDATE_PUBLIC_KEY_BASE64
if ($null -eq $releasePublicKey) {
    $releasePublicKey = ""
}

$outputDirectory = Split-Path -Parent $ApplicationBinary
$updaterBinary = Join-Path $outputDirectory "beresta-updater.exe"
$ldflags = "-X=main.releasePublicKeyBase64=$releasePublicKey -X=main.currentVersion=$version -w -s -H windowsgui"

Push-Location $projectRoot
try {
    & go.exe build -trimpath "-ldflags=$ldflags" -o $updaterBinary ./cmd/beresta-updater
    if ($LASTEXITCODE -ne 0) {
        throw "Building beresta-updater.exe failed with exit code $LASTEXITCODE."
    }
} finally {
    Pop-Location
}

& (Join-Path $PSScriptRoot "sign-artifact.ps1") -Artifact $ApplicationBinary
& (Join-Path $PSScriptRoot "sign-artifact.ps1") -Artifact $updaterBinary
