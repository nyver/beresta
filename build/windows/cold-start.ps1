[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$ApplicationPath,

    [ValidateRange(1, 60000)]
    [int]$BudgetMilliseconds = 5000,

    [ValidateRange(1, 100)]
    [int]$SampleCount = 10,

    [ValidateRange(1000, 120000)]
    [int]$SampleTimeoutMilliseconds = 30000
)

$ErrorActionPreference = "Stop"
$workspaceRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$application = (Resolve-Path -LiteralPath $ApplicationPath).Path
$coldStartBase = Join-Path $workspaceRoot "build\output\cold-start"
New-Item -ItemType Directory -Force -Path $coldStartBase | Out-Null

function Remove-IsolatedProfile {
    param([Parameter(Mandatory)][string]$Path)

    $resolvedPath = [IO.Path]::GetFullPath($Path)
    $allowedPrefix = [IO.Path]::GetFullPath($coldStartBase) + [IO.Path]::DirectorySeparatorChar
    if (-not $resolvedPath.StartsWith($allowedPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to remove cold-start profile outside $coldStartBase."
    }

    for ($attempt = 1; $attempt -le 20; $attempt++) {
        Remove-Item -LiteralPath $resolvedPath -Recurse -Force -ErrorAction SilentlyContinue
        if (-not (Test-Path -LiteralPath $resolvedPath)) {
            return
        }
        Start-Sleep -Milliseconds 100
    }
    Write-Warning "Could not remove isolated cold-start profile: $resolvedPath"
}

$measurements = [Collections.Generic.List[long]]::new()
for ($sample = 1; $sample -le $SampleCount; $sample++) {
    $isolatedAppData = Join-Path $coldStartBase ([IO.Path]::GetRandomFileName())
    New-Item -ItemType Directory -Force -Path $isolatedAppData | Out-Null

    $process = $null
    $stopwatch = [Diagnostics.Stopwatch]::new()
    try {
        $start = [Diagnostics.ProcessStartInfo]::new()
        $start.FileName = $application
        $start.WorkingDirectory = Split-Path -Parent $application
        $start.UseShellExecute = $false
        $start.EnvironmentVariables["APPDATA"] = $isolatedAppData
        $start.EnvironmentVariables["BERESTA_COLD_START_TEST"] = "1"

        $stopwatch.Start()
        $process = [Diagnostics.Process]::Start($start)
        if ($null -eq $process) {
            throw "Failed to start Beresta."
        }
        while ($true) {
            $process.Refresh()
            if ($process.HasExited) {
                throw "Beresta exited with code $($process.ExitCode) before creating its main window."
            }
            if ($process.MainWindowHandle -ne [IntPtr]::Zero -and $process.Responding) {
                break
            }
            if ($stopwatch.ElapsedMilliseconds -gt $SampleTimeoutMilliseconds) {
                throw "Cold-start sample $sample did not expose a responsive main window within ${SampleTimeoutMilliseconds} ms."
            }
            Start-Sleep -Milliseconds 10
        }
        $stopwatch.Stop()
        $measurements.Add($stopwatch.ElapsedMilliseconds)
        Write-Host "Beresta cold start sample $sample/$SampleCount`: $($stopwatch.ElapsedMilliseconds) ms."
    } finally {
        $stopwatch.Stop()
        if ($null -ne $process -and -not $process.HasExited) {
            $process.Kill()
            $process.WaitForExit()
        }
        Remove-IsolatedProfile -Path $isolatedAppData
    }
}

$sorted = @($measurements | Sort-Object)
$nearestRank = [Math]::Ceiling(0.95 * $sorted.Count)
$p95Milliseconds = $sorted[$nearestRank - 1]
$passed = $p95Milliseconds -le $BudgetMilliseconds
$resultPath = Join-Path $coldStartBase "last-run.json"
[ordered]@{
    schema_version = 1
    measured_at_utc = [DateTime]::UtcNow.ToString("o")
    application = $application
    sample_count = $SampleCount
    percentile = 95
    percentile_method = "nearest-rank"
    samples_milliseconds = @($measurements)
    sorted_samples_milliseconds = $sorted
    p95_milliseconds = $p95Milliseconds
    budget_milliseconds = $BudgetMilliseconds
    passed = $passed
} | ConvertTo-Json -Depth 3 | Set-Content -LiteralPath $resultPath -Encoding UTF8

Write-Host "Beresta cold-start p95: $p95Milliseconds ms across $SampleCount fresh-profile launches (budget: $BudgetMilliseconds ms)."
Write-Host "Cold-start measurements: $resultPath"
if (-not $passed) {
    throw "Cold-start p95 of $p95Milliseconds ms exceeded the ${BudgetMilliseconds} ms budget."
}
