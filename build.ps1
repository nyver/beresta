[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet("bootstrap", "format", "format-check", "locale-check", "lint", "test", "coverage-gate", "security-scan", "build", "server-build", "server-cross-build", "server-smoke", "package", "cold-start", "installer-smoke", "mobile-check", "mobile-bind-android", "mobile-build-android", "mobile-package-android", "mobile-test-android", "verify")]
    [string]$Task = "verify"
)

$ErrorActionPreference = "Stop"
$projectRoot = $PSScriptRoot
$env:GOPATH = Join-Path $projectRoot "build\.go"
$env:GOMODCACHE = Join-Path $env:GOPATH "pkg\mod"
$env:GOCACHE = Join-Path $projectRoot "build\.go-cache"
$env:GOTMPDIR = Join-Path $projectRoot "build\.go-tmp"
$goBin = Join-Path $env:GOPATH "bin"
New-Item -ItemType Directory -Path $env:GOMODCACHE, $env:GOCACHE, $env:GOTMPDIR, $goBin -Force | Out-Null
if (($env:PATH -split [System.IO.Path]::PathSeparator) -notcontains $goBin) {
    $env:PATH = $goBin + [System.IO.Path]::PathSeparator + $env:PATH
}
$portableCompilerBin = Join-Path $projectRoot "build\tools\w64devkit\bin"
if ((Test-Path -LiteralPath $portableCompilerBin -PathType Container) -and
    (($env:PATH -split [System.IO.Path]::PathSeparator) -notcontains $portableCompilerBin)) {
    $env:PATH = $portableCompilerBin + [System.IO.Path]::PathSeparator + $env:PATH
}
# Phase-1 client storage is backed by the native SQLCipher amalgamation. Make
# CGo selection deterministic instead of allowing cached stub builds to pass.
$env:CGO_ENABLED = "1"
# The vendored SQLCipher amalgamation only compiles FTS5 support when this
# build tag is set (see third_party/go-sqlcipher/sqlite3_opt_fts5.go); the
# client schema's notes_fts table requires it everywhere the "sqlite3"
# driver is linked, including the Android AAR built through gomobile bind.
$env:GOFLAGS = "-tags=sqlite_fts5"

function Resolve-Executable {
    param(
        [Parameter(Mandatory)]
        [string]$Name,

        [string[]]$Fallbacks = @()
    )

    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($command) {
        return $command.Source
    }

    foreach ($fallback in $Fallbacks) {
        $candidate = if ([System.IO.Path]::IsPathRooted($fallback)) {
            $fallback
        }
        else {
            Join-Path $projectRoot $fallback
        }
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return $candidate
        }
    }

    throw "Required executable '$Name' was not found. See README.md for prerequisites."
}

function Invoke-Checked {
    param(
        [Parameter(Mandatory)]
        [string]$FilePath,

        [string[]]$Arguments = @(),

        [string]$WorkingDirectory = $projectRoot
    )

    Push-Location $WorkingDirectory
    try {
        & $FilePath @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "Command failed with exit code ${LASTEXITCODE}: $FilePath $($Arguments -join ' ')"
        }
    }
    finally {
        Pop-Location
    }
}

function Invoke-FlutterChecked {
    param(
        [Parameter(Mandatory)]
        [string]$FilePath,

        [string[]]$Arguments = @(),

        [string]$WorkingDirectory = $projectRoot
    )

    $savedAppData = $env:APPDATA
    $savedPubCache = $env:PUB_CACHE
    $savedAnalytics = $env:DASH__SUPPRESS_ANALYTICS
    $savedGradleHome = $env:GRADLE_USER_HOME
    try {
        $env:APPDATA = Join-Path $projectRoot "build\.flutter\appdata"
        $env:PUB_CACHE = Join-Path $projectRoot "build\.pub-cache"
        $env:GRADLE_USER_HOME = Join-Path $projectRoot "build\.gradle"
        $env:DASH__SUPPRESS_ANALYTICS = "true"
        New-Item -ItemType Directory -Path $env:APPDATA, $env:PUB_CACHE, $env:GRADLE_USER_HOME -Force | Out-Null
        Invoke-Checked -FilePath $FilePath -Arguments $Arguments -WorkingDirectory $WorkingDirectory
    }
    finally {
        $env:APPDATA = $savedAppData
        $env:PUB_CACHE = $savedPubCache
        $env:DASH__SUPPRESS_ANALYTICS = $savedAnalytics
        $env:GRADLE_USER_HOME = $savedGradleHome
    }
}

function Get-GoExecutable {
    return Resolve-Executable -Name "go.exe" -Fallbacks @("C:\Program Files\Go\bin\go.exe")
}

function Get-GoFmtExecutable {
    return Resolve-Executable -Name "gofmt.exe" -Fallbacks @("C:\Program Files\Go\bin\gofmt.exe")
}

function Get-FlutterExecutable {
    $projectFlutter = Join-Path $projectRoot "build\tools\flutter-sdk\bin\flutter.bat"
    if (Test-Path -LiteralPath $projectFlutter -PathType Leaf) {
        return $projectFlutter
    }
    return Resolve-Executable -Name "flutter.bat"
}

function Get-DartExecutable {
    $projectDart = Join-Path $projectRoot "build\tools\flutter-sdk\bin\dart.bat"
    if (Test-Path -LiteralPath $projectDart -PathType Leaf) {
        return $projectDart
    }
    return Resolve-Executable -Name "dart.bat"
}

function Get-WailsExecutable {
    return Resolve-Executable -Name "wails.exe" -Fallbacks @("build\tools\wails.exe")
}

function Get-GoMobileExecutable {
    return Resolve-Executable -Name "gomobile.exe" -Fallbacks @("build\.go\bin\gomobile.exe")
}

function Initialize-AndroidEnvironment {
    $androidSdk = $env:ANDROID_SDK_ROOT
    if (-not $androidSdk) {
        $androidSdk = $env:ANDROID_HOME
    }
    if (-not $androidSdk) {
        $flutterSettings = Join-Path $projectRoot "build\.flutter\appdata\.flutter_settings"
        if (Test-Path -LiteralPath $flutterSettings -PathType Leaf) {
            $settings = Get-Content -Raw -LiteralPath $flutterSettings | ConvertFrom-Json
            $androidSdk = $settings.'android-sdk'
        }
    }
    if (-not $androidSdk -or -not (Test-Path -LiteralPath $androidSdk -PathType Container)) {
        throw "Android SDK was not found. Set ANDROID_SDK_ROOT or configure Flutter with 'flutter config --android-sdk <path>'."
    }

    $env:ANDROID_SDK_ROOT = $androidSdk
    $env:ANDROID_HOME = $androidSdk
}

function Invoke-Bootstrap {
    Invoke-Checked -FilePath (Resolve-Executable -Name "npm.cmd") -Arguments @("ci") -WorkingDirectory (Join-Path $projectRoot "desktop\frontend")
    Invoke-FlutterChecked -FilePath (Get-FlutterExecutable) -Arguments @("pub", "get") -WorkingDirectory (Join-Path $projectRoot "mobile")
}

function Invoke-Format {
    $goFiles = Get-ChildItem -Path (Join-Path $projectRoot "cmd"), (Join-Path $projectRoot "core"), (Join-Path $projectRoot "desktop"), (Join-Path $projectRoot "internal"), (Join-Path $projectRoot "server") -Recurse -File -Filter "*.go"
    if ($goFiles.Count -gt 0) {
        Invoke-Checked -FilePath (Get-GoFmtExecutable) -Arguments (@("-w") + $goFiles.FullName)
    }

    Invoke-FlutterChecked -FilePath (Get-DartExecutable) -Arguments @("format", "lib", "test") -WorkingDirectory (Join-Path $projectRoot "mobile")
}

function Invoke-FormatCheck {
    $goFiles = Get-ChildItem -Path (Join-Path $projectRoot "cmd"), (Join-Path $projectRoot "core"), (Join-Path $projectRoot "desktop"), (Join-Path $projectRoot "internal"), (Join-Path $projectRoot "server") -Recurse -File -Filter "*.go"
    if ($goFiles.Count -gt 0) {
        $unformatted = & (Get-GoFmtExecutable) -l @($goFiles.FullName)
        if ($LASTEXITCODE -ne 0) {
            throw "gofmt failed with exit code $LASTEXITCODE."
        }
        if ($unformatted) {
            throw "Go files require formatting: $($unformatted -join ', ')"
        }
    }

    Invoke-FlutterChecked -FilePath (Get-DartExecutable) -Arguments @("format", "--output=none", "--set-exit-if-changed", "lib", "test") -WorkingDirectory (Join-Path $projectRoot "mobile")
}

function Invoke-Lint {
    Invoke-LocaleCheck
    Invoke-Checked -FilePath (Get-GoExecutable) -Arguments @("vet", "./...")
    Invoke-MobileCompileCheck
    Invoke-Checked -FilePath (Resolve-Executable -Name "npm.cmd") -Arguments @("run", "typecheck") -WorkingDirectory (Join-Path $projectRoot "desktop\frontend")
    Invoke-FlutterChecked -FilePath (Get-FlutterExecutable) -Arguments @("analyze") -WorkingDirectory (Join-Path $projectRoot "mobile")
}

function Invoke-LocaleCheck {
    Invoke-Checked -FilePath (Get-GoExecutable) -Arguments @("run", "./cmd/localecheck", "-en", "locales/en.json", "-ru", "locales/ru.json")
}

function Invoke-MobileCompileCheck {
    $bindingCheckDirectory = Join-Path $projectRoot "build\output\gobind-check"
    New-Item -ItemType Directory -Path $bindingCheckDirectory -Force | Out-Null
    Invoke-Checked -FilePath (Get-GoExecutable) -Arguments @("tool", "gobind", "-lang=java", "-outdir=$bindingCheckDirectory", "./core/mobileapi")
}

function Invoke-MobileBind {
    $gomobile = Get-GoMobileExecutable
    Initialize-AndroidEnvironment
    Invoke-Checked -FilePath $gomobile -Arguments @("init")
    $outputDirectory = Join-Path $projectRoot "build\output"
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    $aarPath = Join-Path $outputDirectory "beresta-core.aar"
    Invoke-Checked -FilePath $gomobile -Arguments @("bind", "-target=android", "-androidapi", "24", "-tags=sqlite_fts5", "-o", $aarPath, "./core/mobileapi")
    Invoke-Checked -FilePath (Get-GoExecutable) -Arguments @("run", "./cmd/normalizezip", $aarPath)
    $digest = (Get-FileHash -LiteralPath $aarPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Set-Content -LiteralPath ($aarPath + ".sha256") -Value ($digest + "  beresta-core.aar") -Encoding ascii -NoNewline
}

function Invoke-MobileBuildAndroid {
    Invoke-MobileBind
    Invoke-FlutterChecked -FilePath (Get-FlutterExecutable) -Arguments @("build", "apk", "--debug") -WorkingDirectory (Join-Path $projectRoot "mobile")
}

function Invoke-MobilePackageAndroid {
    # Produces a signed release APK and AAB. The Gradle build itself fails
    # closed (see mobile/android/app/build.gradle.kts) if the
    # BERESTA_ANDROID_KEYSTORE_* signing environment variables are not set,
    # so this task cannot silently emit a debug-signed release artifact.
    Invoke-MobileBind
    $mobileDirectory = Join-Path $projectRoot "mobile"
    Invoke-FlutterChecked -FilePath (Get-FlutterExecutable) -Arguments @("build", "apk", "--release") -WorkingDirectory $mobileDirectory
    Invoke-FlutterChecked -FilePath (Get-FlutterExecutable) -Arguments @("build", "appbundle", "--release") -WorkingDirectory $mobileDirectory

    $outputDirectory = Join-Path $projectRoot "build\output"
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    $artifacts = @(
        @{ Source = Join-Path $mobileDirectory "build\app\outputs\flutter-apk\app-release.apk"; File = "beresta-android-release.apk" },
        @{ Source = Join-Path $mobileDirectory "build\app\outputs\bundle\release\app-release.aab"; File = "beresta-android-release.aab" }
    )
    $checksumLines = @()
    foreach ($artifact in $artifacts) {
        $destination = Join-Path $outputDirectory $artifact.File
        Copy-Item -LiteralPath $artifact.Source -Destination $destination -Force
        $digest = (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant()
        $checksumLines += "$digest  $($artifact.File)"
    }
    Set-Content -LiteralPath (Join-Path $outputDirectory "android-SHA256SUMS") -Value $checksumLines -Encoding ascii
}

function Assert-AndroidArm64Device {
    Initialize-AndroidEnvironment
    $adb = Join-Path $env:ANDROID_SDK_ROOT "platform-tools\adb.exe"
    if (-not (Test-Path -LiteralPath $adb -PathType Leaf)) {
        throw "Android platform-tools were not found under $env:ANDROID_SDK_ROOT."
    }

    $devicesOutput = & $adb devices
    if ($LASTEXITCODE -ne 0) {
        throw "adb devices failed with exit code $LASTEXITCODE."
    }
    $deviceIds = @($devicesOutput | ForEach-Object {
        if ($_ -match '^([^\s]+)\s+device$') { $Matches[1] }
    })
    foreach ($deviceId in $deviceIds) {
        $abi = (& $adb -s $deviceId shell getprop ro.product.cpu.abi).Trim()
        if ($LASTEXITCODE -eq 0 -and $abi -eq "arm64-v8a") {
            return $deviceId
        }
    }

    throw "No online Android arm64-v8a device is available. Connect one and enable USB debugging."
}

function Invoke-MobileTestAndroid {
    $deviceId = Assert-AndroidArm64Device
    $savedAndroidSerial = $env:ANDROID_SERIAL
    try {
        # Every adb operation must target only the validated arm64 device. The
        # Gradle connected-test task enumerates unrelated offline emulators and
        # can wait forever, so build and install the test artifacts explicitly.
        $env:ANDROID_SERIAL = $deviceId
        Invoke-MobileBind
        Invoke-FlutterChecked -FilePath (Join-Path $projectRoot "mobile\android\gradlew.bat") -Arguments @("app:assembleDebug", "app:assembleDebugAndroidTest") -WorkingDirectory (Join-Path $projectRoot "mobile\android")

        $adb = Join-Path $env:ANDROID_SDK_ROOT "platform-tools\adb.exe"
        $appApk = Join-Path $projectRoot "mobile\build\app\outputs\apk\debug\app-debug.apk"
        $testApk = Join-Path $projectRoot "mobile\build\app\outputs\apk\androidTest\debug\app-debug-androidTest.apk"
        Invoke-Checked -FilePath $adb -Arguments @("-s", $deviceId, "install", "-r", $appApk)
        Invoke-Checked -FilePath $adb -Arguments @("-s", $deviceId, "install", "-r", $testApk)

        $instrumentationOutput = @(& $adb -s $deviceId shell am instrument -w "app.beresta.notes.test/androidx.test.runner.AndroidJUnitRunner" 2>&1)
        $instrumentationExitCode = $LASTEXITCODE
        $instrumentationOutput | Write-Output
        if ($instrumentationExitCode -ne 0) {
            throw "Android instrumentation failed with exit code $instrumentationExitCode."
        }
        $normalizedInstrumentationOutput = @($instrumentationOutput | ForEach-Object { "$_".Trim() })
        if (($normalizedInstrumentationOutput -join [Environment]::NewLine) -notmatch 'OK \(\d+ tests?\)') {
            throw "Android instrumentation did not report a successful test run."
        }
    }
    finally {
        $env:ANDROID_SERIAL = $savedAndroidSerial
    }
}

function Invoke-GoTests {
    $go = Get-GoExecutable
    $packages = @(& $go list ./...)
    if ($LASTEXITCODE -ne 0) {
        throw "go list ./... failed with exit code $LASTEXITCODE."
    }

    # Some Windows endpoint-protection configurations deny execution of a
    # freshly linked binary named locales.test.exe while allowing the same
    # linked bytes under a stable project-specific name. Compile
    # that package separately so a host policy cannot make the complete test
    # suite flaky; every other package still runs through the normal go test
    # driver and the locale binary keeps the standard testing flags.
    $localePackage = "github.com/beresta-app/beresta/locales"
    $regularPackages = @($packages | Where-Object { $_ -ne $localePackage })
    if ($regularPackages.Count -gt 0) {
        # Serial package execution avoids Windows endpoint-protection races
        # against test temp-directory cleanup and SQLCipher backup files.
        Invoke-Checked -FilePath $go -Arguments (@("test", "-p=1") + $regularPackages)
    }

    $outputDirectory = Join-Path $projectRoot "build\output"
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    $localeTest = Join-Path $outputDirectory "locales-check.exe"
    Invoke-Checked -FilePath $go -Arguments @("test", "-c", "-o", $localeTest, "./locales")
    for ($attempt = 0; ; $attempt++) {
        try {
            Invoke-Checked -FilePath $localeTest -Arguments @("-test.v")
            break
        }
        catch {
            if ($attempt -ge 30 -or $_.Exception.Message -notmatch "failed to run|being used by another process|access") {
                throw
            }
            Start-Sleep -Milliseconds 100
        }
    }
}

function Invoke-Tests {
    Invoke-GoTests
    Invoke-Checked -FilePath (Resolve-Executable -Name "npm.cmd") -Arguments @("test") -WorkingDirectory (Join-Path $projectRoot "desktop\frontend")
    Invoke-FlutterChecked -FilePath (Get-FlutterExecutable) -Arguments @("test") -WorkingDirectory (Join-Path $projectRoot "mobile")
}

function Invoke-Build {
    Invoke-LocaleCheck
    Invoke-ServerBuild
    $desktopBuild = Join-Path $projectRoot "desktop\build"
    $desktopWindows = Join-Path $desktopBuild "windows"
    New-Item -ItemType Directory -Force -Path $desktopBuild, $desktopWindows | Out-Null
    Copy-Item -LiteralPath (Join-Path $projectRoot "desktop\assets\appicon.png") -Destination (Join-Path $desktopBuild "appicon.png") -Force
    # icon.ico is derived from the canonical appicon.png. Removing only this
    # generated file forces Wails to refresh every icon size after a brand
    # update instead of silently packaging a stale cached icon.
    Remove-Item -LiteralPath (Join-Path $desktopWindows "icon.ico") -Force -ErrorAction SilentlyContinue
    Invoke-Checked -FilePath (Resolve-Executable -Name "npm.cmd") -Arguments @("run", "build") -WorkingDirectory (Join-Path $projectRoot "desktop\frontend")
    Invoke-Checked -FilePath (Get-WailsExecutable) -Arguments @("build", "-clean", "-tags", "sqlite_fts5") -WorkingDirectory (Join-Path $projectRoot "desktop")
}

function Invoke-ServerBuild {
    $outputDirectory = Join-Path $projectRoot "build\output"
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    Invoke-Checked -FilePath (Get-GoExecutable) -Arguments @("build", "-trimpath", "-o", (Join-Path $outputDirectory "beresta-server.exe"), "./cmd/beresta-server")
}

function Invoke-ServerCrossBuild {
    $go = Get-GoExecutable
    $outputDirectory = Join-Path $projectRoot "build\output\server"
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    $savedCGO = $env:CGO_ENABLED
    $savedGOOS = $env:GOOS
    $savedGOARCH = $env:GOARCH
    $savedGOFLAGS = $env:GOFLAGS
    try {
        $env:CGO_ENABLED = "0"
        $env:GOFLAGS = ""
        $targets = @(
            @{ OS = "windows"; Arch = "amd64"; File = "beresta-server-windows-amd64.exe" },
            @{ OS = "linux"; Arch = "amd64"; File = "beresta-server-linux-amd64" },
            @{ OS = "linux"; Arch = "arm64"; File = "beresta-server-linux-arm64" }
        )
        foreach ($target in $targets) {
            $env:GOOS = $target.OS
            $env:GOARCH = $target.Arch
            Invoke-Checked -FilePath $go -Arguments @("build", "-trimpath", "-ldflags=-s -w", "-o", (Join-Path $outputDirectory $target.File), "./cmd/beresta-server")
        }
    }
    finally {
        $env:CGO_ENABLED = $savedCGO
        $env:GOOS = $savedGOOS
        $env:GOARCH = $savedGOARCH
        $env:GOFLAGS = $savedGOFLAGS
    }
    Write-ServerReleaseProvenance -OutputDirectory $outputDirectory -Targets $targets
}

# Write-ServerReleaseProvenance records, alongside the cross-built server
# binaries, exactly what a release consumer needs to verify what they
# downloaded and where it came from: a checksum file, a per-binary embedded
# module manifest (a minimal, no-extra-tool software bill of materials -
# see docs/server-operations.md), and one provenance.json capturing the
# source commit and build environment. It reads only already-built files;
# it never re-downloads or re-resolves dependencies.
function Write-ServerReleaseProvenance {
    param(
        [Parameter(Mandatory)] [string]$OutputDirectory,
        [Parameter(Mandatory)] [array]$Targets
    )
    $go = Get-GoExecutable
    $checksumLines = @()
    foreach ($target in $Targets) {
        $path = Join-Path $OutputDirectory $target.File
        $digest = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
        $checksumLines += "$digest  $($target.File)"
        $sbomPath = "$path.sbom.txt"
        & $go version -m $path | Out-File -LiteralPath $sbomPath -Encoding ascii
        if ($LASTEXITCODE -ne 0) {
            throw "go version -m failed for $path with exit code $LASTEXITCODE."
        }
    }
    $checksumsPath = Join-Path $OutputDirectory "SHA256SUMS"
    Set-Content -LiteralPath $checksumsPath -Value $checksumLines -Encoding ascii
    if ($env:BERESTA_RELEASE_PRIVATE_KEY_BASE64) {
        Invoke-Checked -FilePath $go -Arguments @("run", "./cmd/beresta-release-sign", "-detached-file", $checksumsPath)
    }

    $commit = "unknown"
    $dirty = $true
    $git = Get-Command git -ErrorAction SilentlyContinue
    if ($git) {
        # git can write non-fatal warnings (e.g. CRLF normalization notices)
        # to stderr; under this script's $ErrorActionPreference = "Stop",
        # even a redirected native-command stderr line becomes a terminating
        # error unless the preference is relaxed for the call itself.
        $savedErrorActionPreference = $ErrorActionPreference
        $ErrorActionPreference = "Continue"
        try {
            $revParse = & $git.Source rev-parse HEAD 2>$null
            if ($LASTEXITCODE -eq 0) { $commit = ($revParse | Select-Object -Last 1).Trim() }
            & $git.Source diff --quiet HEAD 2>$null | Out-Null
            $dirty = ($LASTEXITCODE -ne 0)
        }
        finally {
            $ErrorActionPreference = $savedErrorActionPreference
        }
    }
    $goVersion = (& $go version).Trim()
    $artifacts = @($Targets | ForEach-Object {
        $digest = (Get-FileHash -LiteralPath (Join-Path $OutputDirectory $_.File) -Algorithm SHA256).Hash.ToLowerInvariant()
        [ordered]@{ file = $_.File; os = $_.OS; arch = $_.Arch; sha256 = $digest }
    })
    $provenance = [ordered]@{
        source_commit = $commit
        source_dirty  = $dirty
        go_version    = $goVersion
        built_at_utc  = (Get-Date).ToUniversalTime().ToString("o")
        artifacts     = $artifacts
    }
    ($provenance | ConvertTo-Json -Depth 6) | Set-Content -LiteralPath (Join-Path $OutputDirectory "provenance.json") -Encoding ascii
}

function Invoke-ServerSmoke {
    Invoke-ServerCrossBuild
    Invoke-Checked -FilePath "powershell.exe" -Arguments @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", (Join-Path $projectRoot "build\server\smoke.ps1"), "-ApplicationPath", (Join-Path $projectRoot "build\output\server\beresta-server-windows-amd64.exe"))
}

function Invoke-Package {
    Invoke-LocaleCheck
    Invoke-ServerBuild
    $desktopBuild = Join-Path $projectRoot "desktop\build"
    $desktopWindows = Join-Path $desktopBuild "windows"
    New-Item -ItemType Directory -Force -Path $desktopBuild, $desktopWindows | Out-Null
    Copy-Item -LiteralPath (Join-Path $projectRoot "desktop\assets\appicon.png") -Destination (Join-Path $desktopBuild "appicon.png") -Force
    Remove-Item -LiteralPath (Join-Path $desktopWindows "icon.ico") -Force -ErrorAction SilentlyContinue

    $makeNSIS = Resolve-Executable -Name "makensis.exe" -Fallbacks @("build\tools\nsis-3.12\nsis-3.12\makensis.exe", "C:\Program Files (x86)\NSIS\makensis.exe", "C:\Program Files\NSIS\makensis.exe")
    $nsisDirectory = Split-Path -Parent $makeNSIS
    if (($env:PATH -split [System.IO.Path]::PathSeparator) -notcontains $nsisDirectory) {
        $env:PATH = $nsisDirectory + [System.IO.Path]::PathSeparator + $env:PATH
    }
    Invoke-Checked -FilePath (Resolve-Executable -Name "npm.cmd") -Arguments @("run", "build") -WorkingDirectory (Join-Path $projectRoot "desktop\frontend")
    Invoke-Checked -FilePath (Get-WailsExecutable) -Arguments @("build", "-clean", "-tags", "sqlite_fts5", "-nsis", "-installscope", "user") -WorkingDirectory (Join-Path $projectRoot "desktop")

    $outputDirectory = Join-Path $projectRoot "build\output"
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    Copy-Item -LiteralPath (Join-Path $projectRoot "desktop\build\bin\beresta.exe") -Destination (Join-Path $outputDirectory "beresta.exe") -Force
    Copy-Item -LiteralPath (Join-Path $projectRoot "desktop\build\bin\beresta-updater.exe") -Destination (Join-Path $outputDirectory "beresta-updater.exe") -Force
    $installer = Join-Path $projectRoot "desktop\build\bin\Beresta-amd64-installer.exe"
    if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) {
        throw "Wails did not produce the expected NSIS installer: $installer"
    }
    Copy-Item -LiteralPath $installer -Destination (Join-Path $outputDirectory "Beresta-amd64-installer.exe") -Force
}

function Invoke-InstallerSmoke {
    $installer = Join-Path $projectRoot "build\output\Beresta-amd64-installer.exe"
    if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) {
        Invoke-Package
    }
    Invoke-Checked -FilePath "powershell.exe" -Arguments @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", (Join-Path $projectRoot "build\windows\smoke-installer.ps1"), "-InstallerPath", $installer)
}

function Invoke-ColdStart {
    $application = Join-Path $projectRoot "build\output\beresta.exe"
    if (-not (Test-Path -LiteralPath $application -PathType Leaf)) {
        Invoke-Package
    }
    Invoke-Checked -FilePath "powershell.exe" -Arguments @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", (Join-Path $projectRoot "build\windows\cold-start.ps1"), "-ApplicationPath", $application)
}

function Invoke-CoverageGate {
    # The release-quality spec requires at least 80 percent automated test
    # coverage for the shared core; this gate makes that a build failure
    # rather than an aspiration. Only core/... is measured: server, desktop,
    # and mobile bindings are thin platform adapters over it and are covered
    # by their own integration/end-to-end suites instead.
    $go = Get-GoExecutable
    $outputDirectory = Join-Path $projectRoot "build\output"
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    $coverageProfile = Join-Path $outputDirectory "core-coverage.out"
    Invoke-Checked -FilePath $go -Arguments @("test", "-p=1", "-coverprofile=$coverageProfile", "./core/...")

    $summary = & $go tool cover -func=$coverageProfile
    if ($LASTEXITCODE -ne 0) {
        throw "go tool cover failed with exit code $LASTEXITCODE."
    }
    $totalLine = $summary | Where-Object { $_ -match '^total:\s+\(statements\)\s+([\d.]+)%$' } | Select-Object -Last 1
    if (-not $totalLine -or $totalLine -notmatch '([\d.]+)%$') {
        throw "Could not parse total coverage from go tool cover output."
    }
    $percent = [double]$Matches[1]
    $threshold = 80.0
    Write-Output "Core coverage: $percent% (threshold: $threshold%)"
    if ($percent -lt $threshold) {
        throw "Core coverage $percent% is below the required $threshold% threshold."
    }
}

function Invoke-SecurityScan {
    # Release security gates from the release-quality spec: Go vulnerability
    # analysis and OSV dependency scanning. Both tools are fetched by exact
    # version through `go run`, so no extra CI-side install step is needed
    # and the pinned version is reviewable in this file like any other
    # dependency.
    $go = Get-GoExecutable
    Invoke-Checked -FilePath $go -Arguments @("run", "golang.org/x/vuln/cmd/govulncheck@v1.1.4", "./...")
    Invoke-Checked -FilePath $go -Arguments @("run", "github.com/google/osv-scanner/v2/cmd/osv-scanner@v2.2.1", "--lockfile=go.mod")
}

switch ($Task) {
    "bootstrap" { Invoke-Bootstrap }
    "format" { Invoke-Format }
    "format-check" { Invoke-FormatCheck }
    "locale-check" { Invoke-LocaleCheck }
    "lint" { Invoke-Lint }
    "test" { Invoke-Tests }
    "coverage-gate" { Invoke-CoverageGate }
    "security-scan" { Invoke-SecurityScan }
    "build" { Invoke-Build }
    "server-build" { Invoke-ServerBuild }
    "server-cross-build" { Invoke-ServerCrossBuild }
    "server-smoke" { Invoke-ServerSmoke }
    "package" { Invoke-Package }
    "cold-start" { Invoke-ColdStart }
    "installer-smoke" { Invoke-InstallerSmoke }
    "mobile-check" { Invoke-MobileCompileCheck }
    "mobile-bind-android" { Invoke-MobileBind }
    "mobile-build-android" { Invoke-MobileBuildAndroid }
    "mobile-package-android" { Invoke-MobilePackageAndroid }
    "mobile-test-android" { Invoke-MobileTestAndroid }
    "verify" {
        Invoke-FormatCheck
        Invoke-Lint
        Invoke-Tests
        Invoke-Package
    }
}
