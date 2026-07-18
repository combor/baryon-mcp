$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

. (Join-Path $PSScriptRoot "install.ps1")

$hash = "a" * 64
$assetName = "baryon-mcp_9.9.9_windows_amd64.zip"
$checksum = "$hash  $assetName`n"
if ((Get-ExpectedHash -ChecksumContent $checksum -AssetName $assetName) -ne $hash) {
    throw "checksum parser returned the wrong hash"
}

$missingRejected = $false
try {
    Get-ExpectedHash -ChecksumContent $checksum -AssetName "missing.zip" | Out-Null
}
catch {
    $missingRejected = $true
}
if (-not $missingRejected) {
    throw "checksum parser accepted a missing asset"
}

if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
    $failureCommand = Join-Path $env:SystemRoot "System32\cmd.exe"
    $failureArgs = @("/c", "echo expected failure 1>&2 & exit /b 1")
}
else {
    $failureCommand = "/bin/sh"
    $failureArgs = @("-c", "echo expected failure >&2; exit 1")
}
if ((Invoke-NativeCommand -Command $failureCommand -Arguments $failureArgs -SuppressOutput) -ne 1) {
    throw "native command exit code was not preserved"
}

if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
    $successArgs = @("/c", "echo expected success & exit /b 0")
}
else {
    $successArgs = @("-c", "echo expected success; exit 0")
}
if ((Invoke-NativeCommand -Command $failureCommand -Arguments $successArgs) -ne 0) {
    throw "native command stdout polluted its returned exit code"
}

$testDir = Join-Path ([IO.Path]::GetTempPath()) ("baryon-installer-test-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $testDir | Out-Null
try {
    $configDir = Join-Path $testDir "Config With Spaces"
    if ((Invoke-NativeCommand -Command $failureCommand -Arguments $successArgs -WorkingDirectory $testDir -SuppressOutput) -ne 0) {
        throw "native command working directory failed"
    }
    $launcherPath = Write-Launcher -Destination $testDir -CredentialDirectory $configDir
    $launcher = Get-Content -Raw $launcherPath
    if ($launcher -notmatch [Regex]::Escape("UseShellExecute = `$false")) {
        throw "launcher does not disable shell execution"
    }
    foreach ($stream in @("Input", "Output", "Error")) {
        if ($launcher -notmatch [Regex]::Escape("RedirectStandard$stream = `$false")) {
            throw "launcher does not preserve standard $stream"
        }
    }

    $powerShellPath = "C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe"
    $adapters = Get-ClientAdapters -PowerShellPath $powerShellPath -LauncherPath $launcherPath
    $claudeAdd = [string[]] $adapters["claude"]["add"]
    $codexAdd = [string[]] $adapters["codex"]["add"]
    if ($claudeAdd[0] -ne "mcp" -or $claudeAdd[6] -ne "baryon" -or
        $claudeAdd[7] -ne "--" -or $claudeAdd[8] -ne $powerShellPath) {
        throw "Claude adapter arguments are incorrect"
    }
    if ($codexAdd[0] -ne "mcp" -or $codexAdd[2] -ne "baryon" -or
        $codexAdd[3] -ne "--" -or $codexAdd[4] -ne $powerShellPath) {
        throw "Codex adapter arguments are incorrect"
    }
}
finally {
    Remove-Item -Recurse -Force -LiteralPath $testDir
}

Write-Host "PowerShell installer tests passed"
