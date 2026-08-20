[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$InstallerPath,

    [ValidateSet("Current", "Windows10", "Windows11")]
    [string]$ExpectedOS = "Current"
)

$ErrorActionPreference = "Stop"
if ($env:BERESTA_INSTALLER_SMOKE_DISPOSABLE_PROFILE -ne "1") {
    throw "Installer smoke tests may remove %APPDATA%\Beresta and require a disposable Windows profile. Set BERESTA_INSTALLER_SMOKE_DISPOSABLE_PROFILE=1 only in an isolated VM or CI runner."
}
$workspaceRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$installer = (Resolve-Path -LiteralPath $InstallerPath).Path
$osBuild = [System.Environment]::OSVersion.Version.Build
if ($ExpectedOS -eq "Windows10" -and $osBuild -ge 22000) {
    throw "Windows 10 smoke test requested, but the current build is $osBuild."
}
if ($ExpectedOS -eq "Windows11" -and $osBuild -lt 22000) {
    throw "Windows 11 smoke test requested, but the current build is $osBuild."
}

$smokeRoot = Join-Path $workspaceRoot ("build\output\installer-smoke\" + [IO.Path]::GetRandomFileName())
$installDirectory = Join-Path $smokeRoot "installed"
New-Item -ItemType Directory -Force -Path $installDirectory | Out-Null

function Invoke-IsolatedProcess {
    param(
        [Parameter(Mandatory)]
        [string]$FilePath,
        [string[]]$Arguments
    )
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $FilePath
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.Arguments = (($Arguments | ForEach-Object { '"' + $_.Replace('"', '\"') + '"' }) -join ' ')
    $process = [Diagnostics.Process]::Start($start)
    $process.WaitForExit()
    if ($process.ExitCode -ne 0) {
        throw "$FilePath exited with code $($process.ExitCode)."
    }
}

try {
    Invoke-IsolatedProcess -FilePath $installer -Arguments @("/S", "/D=$installDirectory")
    $application = Join-Path $installDirectory "beresta.exe"
    $updater = Join-Path $installDirectory "beresta-updater.exe"
    $uninstaller = Join-Path $installDirectory "uninstall.exe"
    foreach ($required in $application, $updater, $uninstaller) {
        if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
            throw "Installer did not create $required."
        }
    }

    $installedHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $application).Hash
    Invoke-IsolatedProcess -FilePath $installer -Arguments @("/S", "/UPDATE", "/D=$installDirectory")
    $rollbackApplication = "$application.previous"
    if (-not (Test-Path -LiteralPath $rollbackApplication -PathType Leaf)) {
        throw "Update install did not preserve the prior executable."
    }
    $rollbackHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $rollbackApplication).Hash
    if ($rollbackHash -ne $installedHash) {
        throw "Update install did not preserve the exact prior executable."
    }
    foreach ($required in $application, $updater, $uninstaller) {
        if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
            throw "Update install did not retain $required."
        }
    }

    $dataDirectory = Join-Path ([Environment]::GetFolderPath([Environment+SpecialFolder]::ApplicationData)) "Beresta"
    New-Item -ItemType Directory -Force -Path $dataDirectory | Out-Null
    Set-Content -LiteralPath (Join-Path $dataDirectory "retention-sentinel") -Value "preserve"
    Invoke-IsolatedProcess -FilePath $uninstaller -Arguments @("/S", "/PURGEUSERDATA=0")
    if (-not (Test-Path -LiteralPath (Join-Path $dataDirectory "retention-sentinel") -PathType Leaf)) {
        throw "Default uninstall removed local user data."
    }

    Invoke-IsolatedProcess -FilePath $installer -Arguments @("/S", "/D=$installDirectory")
    $uninstaller = Join-Path $installDirectory "uninstall.exe"
    Invoke-IsolatedProcess -FilePath $uninstaller -Arguments @("/S", "/PURGEUSERDATA=1")
    if (Test-Path -LiteralPath $dataDirectory) {
        throw "Purge uninstall retained local user data."
    }
} finally {
    $resolvedSmokeRoot = [IO.Path]::GetFullPath($smokeRoot)
    $allowedPrefix = [IO.Path]::GetFullPath((Join-Path $workspaceRoot "build\output\installer-smoke")) + [IO.Path]::DirectorySeparatorChar
    if ($resolvedSmokeRoot.StartsWith($allowedPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        Remove-Item -LiteralPath $resolvedSmokeRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
