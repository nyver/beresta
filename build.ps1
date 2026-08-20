[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet("bootstrap", "format", "format-check", "locale-check", "lint", "test", "build", "package", "cold-start", "installer-smoke", "mobile-check", "mobile-bind-android", "mobile-build-android", "mobile-test-android", "verify")]
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
    return Resolve-Executable -Name "flutter.bat" -Fallbacks @("build\tools\flutter-sdk\bin\flutter.bat")
}

function Get-DartExecutable {
    return Resolve-Executable -Name "dart.bat" -Fallbacks @("build\tools\flutter-sdk\bin\dart.bat")
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
    Invoke-Checked -FilePath $gomobile -Arguments @("bind", "-target=android", "-androidapi", "24", "-tags=sqlite_fts5", "-o", (Join-Path $outputDirectory "beresta-core.aar"), "./core/mobileapi")
}

function Invoke-MobileBuildAndroid {
    Invoke-MobileBind
    Invoke-FlutterChecked -FilePath (Get-FlutterExecutable) -Arguments @("build", "apk", "--debug") -WorkingDirectory (Join-Path $projectRoot "mobile")
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
        Invoke-Checked -FilePath $go -Arguments (@("test") + $regularPackages)
    }

    $outputDirectory = Join-Path $projectRoot "build\output"
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    $localeTest = Join-Path $outputDirectory "locales-check.exe"
    Invoke-Checked -FilePath $go -Arguments @("test", "-c", "-o", $localeTest, "./locales")
    Invoke-Checked -FilePath $localeTest -Arguments @("-test.v")
}

function Invoke-Tests {
    Invoke-GoTests
    Invoke-Checked -FilePath (Resolve-Executable -Name "npm.cmd") -Arguments @("test") -WorkingDirectory (Join-Path $projectRoot "desktop\frontend")
    Invoke-FlutterChecked -FilePath (Get-FlutterExecutable) -Arguments @("test") -WorkingDirectory (Join-Path $projectRoot "mobile")
}

function Invoke-Build {
    Invoke-LocaleCheck
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

function Invoke-Package {
    Invoke-LocaleCheck
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

switch ($Task) {
    "bootstrap" { Invoke-Bootstrap }
    "format" { Invoke-Format }
    "format-check" { Invoke-FormatCheck }
    "locale-check" { Invoke-LocaleCheck }
    "lint" { Invoke-Lint }
    "test" { Invoke-Tests }
    "build" { Invoke-Build }
    "package" { Invoke-Package }
    "cold-start" { Invoke-ColdStart }
    "installer-smoke" { Invoke-InstallerSmoke }
    "mobile-check" { Invoke-MobileCompileCheck }
    "mobile-bind-android" { Invoke-MobileBind }
    "mobile-build-android" { Invoke-MobileBuildAndroid }
    "mobile-test-android" { Invoke-MobileTestAndroid }
    "verify" {
        Invoke-FormatCheck
        Invoke-Lint
        Invoke-Tests
        Invoke-Package
    }
}
