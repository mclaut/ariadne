# Smoke-test the exact Qdrant asset pinned by install.ps1.
[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$repoRoot = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$installerText = Get-Content -Raw -Path (Join-Path $repoRoot "install.ps1")

function Read-PinnedValue([string]$Name) {
    $pattern = '(?m)^\$' + [regex]::Escape($Name) + ' = "([^"]+)"\s*$'
    $match = [regex]::Match($installerText, $pattern)
    if (-not $match.Success) { throw "Could not read $Name from install.ps1" }
    return $match.Groups[1].Value
}

function Read-PinnedVersion([string]$Name) {
    $pattern = '(?m)^\$' + [regex]::Escape($Name) + ' = \[version\]"([^"]+)"\s*$'
    $match = [regex]::Match($installerText, $pattern)
    if (-not $match.Success) { throw "Could not read $Name from install.ps1" }
    return [version]($match.Groups[1].Value)
}

function Read-Log([string]$Path) {
    if (-not (Test-Path $Path)) { return "<no log>" }
    return (Get-Content -Raw -Path $Path)
}

$version = Read-PinnedValue "QdrantVersion"
$asset = Read-PinnedValue "QdrantAsset"
$expectedSHA256 = Read-PinnedValue "QdrantSHA256"
$vcRuntimeURL = Read-PinnedValue "VCRuntimeURL"
$vcRuntimeMinimum = Read-PinnedVersion "VCRuntimeMinimum"
$baseTemp = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }
$tempDir = Join-Path $baseTemp ("ariadne-qdrant-smoke-" + [guid]::NewGuid().ToString("N"))
$archive = Join-Path $tempDir $asset
$expanded = Join-Path $tempDir "expanded"
$data = Join-Path $tempDir "data"
$stdout = Join-Path $tempDir "qdrant.stdout.log"
$stderr = Join-Path $tempDir "qdrant.stderr.log"
$vcRuntimeInstaller = Join-Path $tempDir "vc_redist.x64.exe"
$process = $null
$oldHost = $env:QDRANT__SERVICE__HOST
$oldStorage = $env:QDRANT__STORAGE__STORAGE_PATH
$oldSnapshots = $env:QDRANT__STORAGE__SNAPSHOTS_PATH

New-Item -ItemType Directory -Path $tempDir, $expanded, $data -Force | Out-Null
try {
    Invoke-WebRequest -Uri $vcRuntimeURL -OutFile $vcRuntimeInstaller -UseBasicParsing `
        -Headers @{ "User-Agent" = "ariadne-windows-ci" }
    $signature = Get-AuthenticodeSignature -FilePath $vcRuntimeInstaller
    if ($signature.Status -ne "Valid") { throw "Microsoft VC++ Runtime signature is not valid." }
    if ($signature.SignerCertificate.Subject -notmatch "(^|, )O=Microsoft Corporation(,|$)") {
        throw "Microsoft VC++ Runtime has an unexpected signer."
    }
    $runtime = Join-Path $env:WINDIR "System32\vcruntime140.dll"
    if (-not (Test-Path $runtime)) { throw "Windows runner has no vcruntime140.dll" }
    $runtimeText = (Get-Item $runtime).VersionInfo.FileVersion
    $runtimeMatch = [regex]::Match($runtimeText, "[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+")
    if (-not $runtimeMatch.Success -or ([version]($runtimeMatch.Value) -lt $vcRuntimeMinimum)) {
        throw "Windows runner VC++ Runtime $runtimeText is older than $vcRuntimeMinimum"
    }

    $url = "https://github.com/qdrant/qdrant/releases/download/$version/$asset"
    Invoke-WebRequest -Uri $url -OutFile $archive -UseBasicParsing `
        -Headers @{ "User-Agent" = "ariadne-windows-ci" }
    $actualSHA256 = (Get-FileHash -Path $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualSHA256 -ne $expectedSHA256) {
        throw "Qdrant SHA256 mismatch: got $actualSHA256, want $expectedSHA256"
    }

    Expand-Archive -Path $archive -DestinationPath $expanded -Force
    $qdrant = Get-ChildItem -Path $expanded -Recurse -Filter "qdrant.exe" | Select-Object -First 1
    if (-not $qdrant) { throw "qdrant.exe was not found in $asset" }

    $env:QDRANT__SERVICE__HOST = "127.0.0.1"
    $env:QDRANT__STORAGE__STORAGE_PATH = $data
    $env:QDRANT__STORAGE__SNAPSHOTS_PATH = Join-Path $data "snapshots"
    $process = Start-Process -FilePath $qdrant.FullName -WorkingDirectory $tempDir `
        -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru

    $healthy = $false
    $deadline = (Get-Date).AddSeconds(60)
    while ((Get-Date) -lt $deadline) {
        $process.Refresh()
        if ($process.HasExited) {
            throw "Qdrant exited with code $($process.ExitCode).`n" +
                "stdout:`n$(Read-Log $stdout)`nstderr:`n$(Read-Log $stderr)"
        }
        try {
            $response = Invoke-WebRequest -Uri "http://127.0.0.1:6333/healthz" `
                -UseBasicParsing -TimeoutSec 2
            if ($response.StatusCode -lt 300) {
                $healthy = $true
                break
            }
        } catch {
            Start-Sleep -Seconds 1
        }
    }
    if (-not $healthy) {
        throw "Qdrant did not become healthy within 60 seconds.`n" +
            "stdout:`n$(Read-Log $stdout)`nstderr:`n$(Read-Log $stderr)"
    }
    Write-Host "Qdrant $version passed the Windows health check."
} finally {
    if ($process) {
        $process.Refresh()
        if (-not $process.HasExited) { Stop-Process -Id $process.Id -Force }
    }
    $env:QDRANT__SERVICE__HOST = $oldHost
    $env:QDRANT__STORAGE__STORAGE_PATH = $oldStorage
    $env:QDRANT__STORAGE__SNAPSHOTS_PATH = $oldSnapshots
    Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}
