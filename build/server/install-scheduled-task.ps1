[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$ApplicationPath,

    [string]$DataDirectory = "$env:ProgramData\Beresta\Server"
)

$ErrorActionPreference = "Stop"
$resolvedApplication = (Resolve-Path -LiteralPath $ApplicationPath).Path
New-Item -ItemType Directory -Path $DataDirectory -Force | Out-Null
$action = New-ScheduledTaskAction -Execute $resolvedApplication -Argument "--data `"$DataDirectory`""
$trigger = New-ScheduledTaskTrigger -AtStartup
$principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)
Register-ScheduledTask -TaskName "Beresta Server" -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force
