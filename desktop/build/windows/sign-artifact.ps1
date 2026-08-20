[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$Artifact
)

$ErrorActionPreference = "Stop"
$thumbprint = $env:BERESTA_SIGN_CERT_SHA1
$requireSigning = $env:BERESTA_REQUIRE_SIGNING -eq "1"

if ([string]::IsNullOrWhiteSpace($thumbprint)) {
    if ($requireSigning) {
        throw "BERESTA_SIGN_CERT_SHA1 is required for a signed release build."
    }
    Write-Host "Development build: Authenticode signing skipped for $Artifact"
    exit 0
}

$thumbprint = ($thumbprint -replace '\s', '').ToUpperInvariant()
if ($thumbprint -notmatch '^[0-9A-F]{40}$') {
    throw "BERESTA_SIGN_CERT_SHA1 must be a 40-character SHA-1 certificate thumbprint."
}

$signTool = if ([string]::IsNullOrWhiteSpace($env:BERESTA_SIGNTOOL)) {
    (Get-Command signtool.exe -ErrorAction Stop).Source
} else {
    $env:BERESTA_SIGNTOOL
}

if (-not (Test-Path -LiteralPath $Artifact -PathType Leaf)) {
    throw "Signing target does not exist: $Artifact"
}

& $signTool sign /sha1 $thumbprint /fd SHA256 /td SHA256 /tr "http://timestamp.digicert.com" $Artifact
if ($LASTEXITCODE -ne 0) {
    throw "signtool failed with exit code $LASTEXITCODE for $Artifact"
}
