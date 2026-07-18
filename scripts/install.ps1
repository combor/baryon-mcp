#requires -Version 5.1

[CmdletBinding()]
param(
    [string[]] $Client = @("claude", "codex"),
    [string] $InstallDir = (Join-Path ([Environment]::GetFolderPath("LocalApplicationData")) "Programs\baryon-mcp"),
    [string] $ConfigDir = (Join-Path ([Environment]::GetFolderPath("LocalApplicationData")) "baryon-mcp"),
    [string] $TlsCert,
    [string] $Version,
    [switch] $Reconfigure,
    [switch] $ForceClientConfig,
    [switch] $SkipClientConfig
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$Repository = "combor/baryon-mcp"
$SupportedClients = @("claude", "codex")

function Assert-Windows {
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
        throw "install.ps1 must run on Windows"
    }

    $architecture = if ($env:PROCESSOR_ARCHITEW6432) {
        $env:PROCESSOR_ARCHITEW6432
    }
    else {
        $env:PROCESSOR_ARCHITECTURE
    }
    if ($architecture -ne "AMD64") {
        throw "unsupported Windows architecture: $architecture (only amd64 is released)"
    }
}

function Get-Release {
    param([string] $RequestedVersion)

    if ($RequestedVersion) {
        $tag = if ($RequestedVersion.StartsWith("v")) { $RequestedVersion } else { "v$RequestedVersion" }
        if ($tag -notmatch "^v[0-9A-Za-z._-]+$") {
            throw "invalid release tag: $tag"
        }
        $apiUrl = "https://api.github.com/repos/$Repository/releases/tags/$tag"
    }
    else {
        $apiUrl = if ($env:BARYON_INSTALLER_RELEASE_API_URL) {
            $env:BARYON_INSTALLER_RELEASE_API_URL
        }
        else {
            "https://api.github.com/repos/$Repository/releases/latest"
        }
    }

    [Net.ServicePointManager]::SecurityProtocol =
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    Invoke-RestMethod -UseBasicParsing -Uri $apiUrl -Headers @{ "User-Agent" = "baryon-mcp-installer" }
}

function Move-FileIntoPlace {
    param(
        [string] $Source,
        [string] $Destination
    )

    if (Test-Path -LiteralPath $Destination -PathType Leaf) {
        $backup = "$Destination.backup-$([Guid]::NewGuid().ToString('N'))"
        try {
            [IO.File]::Replace($Source, $Destination, $backup, $true)
        }
        finally {
            Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath $backup
        }
    }
    else {
        [IO.File]::Move($Source, $Destination)
    }
}

function Get-ExpectedHash {
    param(
        [string] $ChecksumContent,
        [string] $AssetName
    )

    $pattern = "^(?<hash>[0-9A-Fa-f]{64})\s+" + [Regex]::Escape($AssetName) + "`$"
    $hashes = @(
        foreach ($line in ($ChecksumContent -split "`r?`n")) {
            if ($line -match $pattern) {
                $Matches["hash"]
            }
        }
    )
    if ($hashes.Count -ne 1) {
        throw "expected exactly one checksum for $AssetName"
    }
    $hashes[0]
}

function Install-Binary {
    param(
        [object] $Release,
        [string] $Destination
    )

    $releaseVersion = ([string] $Release.tag_name).TrimStart("v")
    if ($releaseVersion -notmatch "^[0-9A-Za-z._-]+$") {
        throw "invalid release tag: $($Release.tag_name)"
    }

    $archiveName = "baryon-mcp_${releaseVersion}_windows_amd64.zip"
    $checksumName = "baryon-mcp_${releaseVersion}_SHA256SUMS"
    $archiveAsset = @($Release.assets | Where-Object { $_.name -eq $archiveName })
    $checksumAsset = @($Release.assets | Where-Object { $_.name -eq $checksumName })
    if ($archiveAsset.Count -ne 1 -or $checksumAsset.Count -ne 1) {
        throw "release $($Release.tag_name) is missing $archiveName or $checksumName"
    }

    $tempDir = Join-Path ([IO.Path]::GetTempPath()) ("baryon-mcp-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $tempDir | Out-Null
    try {
        $archivePath = Join-Path $tempDir $archiveName
        $checksumPath = Join-Path $tempDir $checksumName
        Write-Host "Downloading baryon-mcp $($Release.tag_name) for windows/amd64..."
        Invoke-WebRequest -UseBasicParsing -Uri $archiveAsset[0].browser_download_url -OutFile $archivePath
        Invoke-WebRequest -UseBasicParsing -Uri $checksumAsset[0].browser_download_url -OutFile $checksumPath

        $expectedHash = Get-ExpectedHash -ChecksumContent (Get-Content -Raw $checksumPath) -AssetName $archiveName
        $actualHash = (Get-FileHash -Algorithm SHA256 -Path $archivePath).Hash
        if (-not $actualHash.Equals($expectedHash, [StringComparison]::OrdinalIgnoreCase)) {
            throw "checksum mismatch for $archiveName"
        }

        $extractDir = Join-Path $tempDir "extracted"
        Expand-Archive -Path $archivePath -DestinationPath $extractDir
        $sourceBinary = Join-Path $extractDir "baryon-mcp.exe"
        if (-not (Test-Path -LiteralPath $sourceBinary -PathType Leaf)) {
            throw "release archive does not contain baryon-mcp.exe"
        }

        New-Item -ItemType Directory -Force -Path $Destination | Out-Null
        $targetBinary = Join-Path $Destination "baryon-mcp.exe"
        $stagedBinary = Join-Path $Destination (".baryon-mcp-" + [Guid]::NewGuid().ToString("N") + ".exe")
        Copy-Item -LiteralPath $sourceBinary -Destination $stagedBinary
        try {
            Move-FileIntoPlace -Source $stagedBinary -Destination $targetBinary
        }
        catch {
            Remove-Item -Force -ErrorAction SilentlyContinue -LiteralPath $stagedBinary
            throw "could not replace $targetBinary; close clients running baryon-mcp and retry: $_"
        }
        Set-Content -Encoding ASCII -Path (Join-Path $Destination "version.txt") -Value $Release.tag_name
        $targetBinary
    }
    finally {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue -LiteralPath $tempDir
    }
}

function Find-Certificate {
    param([string] $RequestedPath)

    if ($RequestedPath) {
        return $RequestedPath
    }
    if ($env:PROTON_BRIDGE_TLS_CERT) {
        return $env:PROTON_BRIDGE_TLS_CERT
    }

    $probes = @(
        (Join-Path $env:APPDATA "protonmail\bridge-v3\cert.pem"),
        (Join-Path $env:APPDATA "protonmail\bridge\cert.pem")
    )
    foreach ($probe in $probes) {
        if (Test-Path -LiteralPath $probe -PathType Leaf) {
            return $probe
        }
    }
    Read-Host "Path to Proton Bridge's exported cert.pem"
}

function Install-Certificate {
    param(
        [string] $RequestedPath,
        [string] $Destination,
        [bool] $Replace
    )

    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    $target = Join-Path $Destination "cert.pem"
    if ((Test-Path -LiteralPath $target -PathType Leaf) -and -not $RequestedPath -and -not $Replace) {
        Write-Host "Reusing $target"
        return $target
    }

    $source = Find-Certificate -RequestedPath $RequestedPath
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
        throw "TLS certificate not found: $source"
    }
    if (-not (Select-String -Quiet -SimpleMatch "-----BEGIN CERTIFICATE-----" -LiteralPath $source)) {
        throw "TLS certificate does not contain a CERTIFICATE PEM block"
    }

    if ([IO.Path]::GetFullPath($source) -ne [IO.Path]::GetFullPath($target)) {
        $staged = Join-Path $Destination (".cert-" + [Guid]::NewGuid().ToString("N") + ".pem")
        Copy-Item -LiteralPath $source -Destination $staged
        Move-FileIntoPlace -Source $staged -Destination $target
    }
    $target
}

function Test-SecureStringEmpty {
    param([Security.SecureString] $Value)

    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Value)
    try {
        ([Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)).Length -eq 0
    }
    finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
    }
}

function Install-Credentials {
    param(
        [string] $Destination,
        [bool] $Replace
    )

    Assert-Windows
    New-Item -ItemType Directory -Force -Path $Destination | Out-Null
    $credentialPath = Join-Path $Destination "bridge-credentials.json"
    if ((Test-Path -LiteralPath $credentialPath -PathType Leaf) -and -not $Replace) {
        Write-Host "Reusing DPAPI-protected Proton Bridge credentials"
        return $credentialPath
    }

    $username = Read-Host "Proton Bridge IMAP username"
    if ([string]::IsNullOrWhiteSpace($username)) {
        throw "Bridge username is required"
    }
    $password = Read-Host "Proton Bridge-generated password" -AsSecureString
    if (Test-SecureStringEmpty -Value $password) {
        throw "Bridge password is required"
    }

    $secureUsername = ConvertTo-SecureString -String $username -AsPlainText -Force
    try {
        $protected = [ordered]@{
            username = ConvertFrom-SecureString -SecureString $secureUsername
            password = ConvertFrom-SecureString -SecureString $password
        }
        $staged = Join-Path $Destination (".credentials-" + [Guid]::NewGuid().ToString("N") + ".json")
        $protected | ConvertTo-Json | Set-Content -Encoding UTF8 -Path $staged
        Move-FileIntoPlace -Source $staged -Destination $credentialPath
    }
    finally {
        $secureUsername.Dispose()
        $password.Dispose()
        $username = $null
    }
    $credentialPath
}

function Write-Launcher {
    param(
        [string] $Destination,
        [string] $CredentialDirectory
    )

    $escapedConfigDir = $CredentialDirectory.Replace("'", "''")
    $content = @'
$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$configDir = '__CONFIG_DIR__'
$credentials = Get-Content -Raw (Join-Path $configDir "bridge-credentials.json") | ConvertFrom-Json
$secureUsername = ConvertTo-SecureString $credentials.username
$securePassword = ConvertTo-SecureString $credentials.password
$usernamePointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureUsername)
$passwordPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($securePassword)
$exitCode = 1

try {
    $env:PROTON_BRIDGE_USERNAME = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($usernamePointer)
    $env:PROTON_BRIDGE_PASSWORD = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($passwordPointer)
    $env:PROTON_BRIDGE_TLS_CERT = Join-Path $configDir "cert.pem"

    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = Join-Path $PSScriptRoot "baryon-mcp.exe"
    $startInfo.UseShellExecute = $false
    $startInfo.RedirectStandardInput = $false
    $startInfo.RedirectStandardOutput = $false
    $startInfo.RedirectStandardError = $false

    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $startInfo
    if (-not $process.Start()) {
        throw "could not start baryon-mcp.exe"
    }
    $process.WaitForExit()
    $exitCode = $process.ExitCode
    $process.Dispose()
}
finally {
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($usernamePointer)
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($passwordPointer)
    $secureUsername.Dispose()
    $securePassword.Dispose()
    Remove-Item Env:PROTON_BRIDGE_USERNAME -ErrorAction SilentlyContinue
    Remove-Item Env:PROTON_BRIDGE_PASSWORD -ErrorAction SilentlyContinue
}

exit $exitCode
'@.Replace("__CONFIG_DIR__", $escapedConfigDir)

    $launcherPath = Join-Path $Destination "baryon-launch.ps1"
    $staged = Join-Path $Destination (".baryon-launch-" + [Guid]::NewGuid().ToString("N") + ".ps1")
    Set-Content -Encoding UTF8 -Path $staged -Value $content
    Move-FileIntoPlace -Source $staged -Destination $launcherPath
    $launcherPath
}

function Get-WindowsPowerShellPath {
    $systemPowerShell = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
    if (Test-Path -LiteralPath $systemPowerShell -PathType Leaf) {
        return $systemPowerShell
    }
    (Get-Command powershell.exe -ErrorAction Stop).Source
}

function Get-ClientAdapters {
    param(
        [string] $PowerShellPath,
        [string] $LauncherPath
    )

    $launcherArgs = @(
        $PowerShellPath,
        "-NoLogo",
        "-NoProfile",
        "-NonInteractive",
        "-ExecutionPolicy",
        "Bypass",
        "-File",
        $LauncherPath
    )
    @{
        claude = @{
            command = "claude"
            exists = @("mcp", "get", "baryon")
            remove = @("mcp", "remove", "--scope", "user", "baryon")
            add = @("mcp", "add", "--transport", "stdio", "--scope", "user", "baryon", "--") + $launcherArgs
        }
        codex = @{
            command = "codex"
            exists = @("mcp", "get", "baryon", "--json")
            remove = @("mcp", "remove", "baryon")
            add = @("mcp", "add", "baryon", "--") + $launcherArgs
        }
    }
}

function Invoke-NativeCommand {
    param(
        [string] $Command,
        [string[]] $Arguments,
        [string] $WorkingDirectory,
        [switch] $SuppressOutput
    )

    $previousPreference = $ErrorActionPreference
    $exitCode = 1
    $locationPushed = $false
    try {
        # Windows PowerShell converts redirected native stderr to error records.
        # Keep a normal nonzero exit available to the caller instead of turning
        # an expected "not found" probe into a terminating error.
        $ErrorActionPreference = "Continue"
        if ($WorkingDirectory) {
            Push-Location -LiteralPath $WorkingDirectory
            $locationPushed = $true
        }
        if ($SuppressOutput) {
            & $Command @Arguments *> $null
        }
        else {
            & $Command @Arguments | Out-Host
        }
        $exitCode = $LASTEXITCODE
    }
    finally {
        if ($locationPushed) {
            Pop-Location
        }
        $ErrorActionPreference = $previousPreference
    }
    $exitCode
}

function Register-Client {
    param(
        [string] $Name,
        [hashtable] $Adapter,
        [bool] $Replace,
        [string] $WorkingDirectory
    )

    $command = [string] $Adapter["command"]
    if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
        Write-Warning "$Name is not installed; skipped its configuration"
        return
    }

    $existsArgs = [string[]] $Adapter["exists"]
    $removeArgs = [string[]] $Adapter["remove"]
    $addArgs = [string[]] $Adapter["add"]

    $exists = (Invoke-NativeCommand -Command $command -Arguments $existsArgs -WorkingDirectory $WorkingDirectory -SuppressOutput) -eq 0
    if ($exists -and -not $Replace) {
        Write-Warning "$Name already has a baryon entry; left it unchanged"
        return
    }
    if ($exists) {
        Invoke-NativeCommand -Command $command -Arguments $removeArgs -WorkingDirectory $WorkingDirectory -SuppressOutput | Out-Null
    }

    if ((Invoke-NativeCommand -Command $command -Arguments $addArgs -WorkingDirectory $WorkingDirectory) -ne 0) {
        throw "could not configure $Name"
    }
    Write-Host "Configured $Name"
}

function Invoke-Installer {
    Assert-Windows
    $InstallDir = [IO.Path]::GetFullPath($InstallDir)
    $ConfigDir = [IO.Path]::GetFullPath($ConfigDir)
    foreach ($clientName in $Client) {
        if ($SupportedClients -notcontains $clientName) {
            throw "unsupported client $clientName (supported: $($SupportedClients -join ', '))"
        }
    }

    $release = Get-Release -RequestedVersion $Version
    $binaryPath = Install-Binary -Release $release -Destination $InstallDir
    $certificatePath = Install-Certificate -RequestedPath $TlsCert -Destination $ConfigDir -Replace $Reconfigure.IsPresent
    $credentialPath = Install-Credentials -Destination $ConfigDir -Replace $Reconfigure.IsPresent
    $launcherPath = Write-Launcher -Destination $InstallDir -CredentialDirectory $ConfigDir

    if (-not $SkipClientConfig) {
        $adapters = Get-ClientAdapters -PowerShellPath (Get-WindowsPowerShellPath) -LauncherPath $launcherPath
        $clientWorkingDirectory = Join-Path ([IO.Path]::GetTempPath()) ("baryon-client-config-" + [Guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Path $clientWorkingDirectory | Out-Null
        try {
            foreach ($clientName in $Client) {
                Register-Client -Name $clientName -Adapter $adapters[$clientName] -Replace $ForceClientConfig.IsPresent -WorkingDirectory $clientWorkingDirectory
            }
        }
        finally {
            Remove-Item -Recurse -Force -ErrorAction SilentlyContinue -LiteralPath $clientWorkingDirectory
        }
    }

    Write-Host "Installed baryon-mcp $($release.tag_name)"
    Write-Host "  binary:     $binaryPath"
    Write-Host "  launcher:   $launcherPath"
    Write-Host "  certificate: $certificatePath"
    Write-Verbose "Credentials: $credentialPath"
}

if ($MyInvocation.InvocationName -ne ".") {
    Invoke-Installer
}
