# Ariadne Windows installer
[CmdletBinding()]
param(
    [string]$Version = "latest",
    [switch]$Yes,
    [switch]$Update,
    [switch]$SkipOllama,
    [switch]$SkipModels,
    [string]$EmbeddingModel = "bge-m3",
    [string]$SummaryModel = "qwen2.5:7b"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repository = "mclaut/ariadne"
$RuntimeDir = Join-Path $env:USERPROFILE ".ariadne"
$BinDir = Join-Path $RuntimeDir "bin"
$LogsDir = Join-Path $RuntimeDir "logs"
$DataDir = Join-Path $RuntimeDir "qdrant-data"
$TaskQdrant = "Ariadne Qdrant"
$TaskTray = "Ariadne Tray"
$QdrantVersion = "v1.18.2"
$QdrantAsset = "qdrant-x86_64-pc-windows-msvc.zip"
$QdrantSHA256 = "b2b262cba6f78cf4fa794ae78d73a8f70a221c93c76c75ac8fd6fe95d809b142"
$UserAgent = "ariadne-windows-installer"

function Write-Step([string]$Message) {
    Write-Host "`n==> $Message" -ForegroundColor Cyan
}

function Confirm-Install([string]$Message) {
    if ($Yes) { return $true }
    $answer = Read-Host "$Message [y/N]"
    return $answer -match "^[Yy]$"
}

function Invoke-Download([string]$Url, [string]$OutFile) {
    Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing -Headers @{ "User-Agent" = $UserAgent }
}

function Test-HTTP([string]$Url) {
    try {
        $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 3 -Headers @{ "User-Agent" = $UserAgent }
        return $response.StatusCode -lt 300
    } catch {
        return $false
    }
}

function Wait-HTTP([string]$Url, [int]$Seconds) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        if (Test-HTTP $Url) { return $true }
        Start-Sleep -Seconds 1
    }
    return $false
}

function Get-Release([string]$Requested) {
    if ($Requested -eq "latest") {
        $uri = "https://api.github.com/repos/$Repository/releases/latest"
    } else {
        $tag = if ($Requested.StartsWith("v")) { $Requested } else { "v$Requested" }
        $uri = "https://api.github.com/repos/$Repository/releases/tags/$tag"
    }
    $release = Invoke-RestMethod -Uri $uri -Headers @{ "User-Agent" = $UserAgent; "Accept" = "application/vnd.github+json" }
    if ($release.draft -or $release.prerelease -or $release.tag_name -notmatch "^v[0-9]+\.[0-9]+\.[0-9]+$") {
        throw "Refusing a draft, prerelease, or invalid Ariadne release."
    }
    return $release
}

function Get-AssetURL($Release, [string]$Name) {
    $asset = $Release.assets | Where-Object { $_.name -eq $Name } | Select-Object -First 1
    if (-not $asset) { throw "Release asset not found: $Name" }
    return $asset.browser_download_url
}

function Assert-Checksum([string]$File, [string]$ChecksumsFile, [string]$AssetName) {
    $checksums = Get-Content -Raw -Path $ChecksumsFile
    $pattern = "(?im)^([a-f0-9]{64})\s+\*?" + [regex]::Escape($AssetName) + "\s*$"
    $match = [regex]::Match($checksums, $pattern)
    if (-not $match.Success) { throw "No checksum found for $AssetName" }
    $actual = (Get-FileHash -Path $File -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $match.Groups[1].Value.ToLowerInvariant()) {
        throw "SHA256 mismatch for $AssetName"
    }
}

function Assert-OllamaSignature([string]$File) {
    $signature = Get-AuthenticodeSignature -FilePath $File
    if ($signature.Status -ne "Valid") { throw "Ollama installer signature is not valid." }
    if ($signature.SignerCertificate.Subject -notmatch "(^|, )O=Ollama Inc\.(,|$)") {
        throw "Ollama installer has an unexpected signer."
    }
}

function ConvertTo-PSLiteral([string]$Value) {
    return "'" + $Value.Replace("'", "''") + "'"
}

function Register-AriadneTask(
    [string]$Name,
    [string]$Executable,
    [string]$Arguments,
    [string]$WorkingDirectory,
    [bool]$Restart
) {
    $action = New-ScheduledTaskAction -Execute $Executable -Argument $Arguments -WorkingDirectory $WorkingDirectory
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User ([System.Security.Principal.WindowsIdentity]::GetCurrent().Name)
    if ($Restart) {
        $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) `
            -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) `
            -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    } else {
        $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) `
            -MultipleInstances IgnoreNew -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    }
    $principal = New-ScheduledTaskPrincipal `
        -UserId ([System.Security.Principal.WindowsIdentity]::GetCurrent().Name) `
        -LogonType Interactive -RunLevel Limited
    $task = New-ScheduledTask -Action $action -Trigger $trigger -Settings $settings -Principal $principal
    Register-ScheduledTask -TaskName $Name -InputObject $task -Force | Out-Null
}

function Install-Qdrant([string]$TempDir) {
    if (Test-HTTP "http://127.0.0.1:6333/healthz") {
        Write-Host "Qdrant is already running; reusing it without reconfiguration."
        return
    }

    $qdrantExe = Join-Path $BinDir "qdrant.exe"
    if (-not (Test-Path $qdrantExe)) {
        Write-Step "Installing native Qdrant $QdrantVersion"
        $archive = Join-Path $TempDir $QdrantAsset
        $url = "https://github.com/qdrant/qdrant/releases/download/$QdrantVersion/$QdrantAsset"
        Invoke-Download $url $archive
        $actual = (Get-FileHash -Path $archive -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $QdrantSHA256) { throw "Qdrant SHA256 verification failed." }
        $expanded = Join-Path $TempDir "qdrant"
        Expand-Archive -Path $archive -DestinationPath $expanded -Force
        $source = Get-ChildItem -Path $expanded -Recurse -Filter "qdrant.exe" | Select-Object -First 1
        if (-not $source) { throw "qdrant.exe was not found in the official archive." }
        Copy-Item -Path $source.FullName -Destination $qdrantExe -Force
    }

    Write-Step "Registering loopback-only Qdrant"
    $launcher = Join-Path $RuntimeDir "start-qdrant.ps1"
    $qdrantLog = Join-Path $LogsDir "qdrant.log"
    $content = @(
        '$ErrorActionPreference = "Stop"',
        ('$env:QDRANT__SERVICE__HOST = ' + (ConvertTo-PSLiteral "127.0.0.1")),
        ('$env:QDRANT__STORAGE__STORAGE_PATH = ' + (ConvertTo-PSLiteral $DataDir)),
        ('$env:QDRANT__STORAGE__SNAPSHOTS_PATH = ' + (ConvertTo-PSLiteral (Join-Path $DataDir "snapshots"))),
        ('Set-Location ' + (ConvertTo-PSLiteral $RuntimeDir)),
        ('& ' + (ConvertTo-PSLiteral $qdrantExe) + ' *>> ' + (ConvertTo-PSLiteral $qdrantLog))
    ) -join "`r`n"
    Set-Content -Path $launcher -Value $content -Encoding UTF8

    $taskArgs = "-NoProfile -NonInteractive -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$launcher`""
    Register-AriadneTask $TaskQdrant "powershell.exe" $taskArgs $RuntimeDir $true
    Start-ScheduledTask -TaskName $TaskQdrant
    if (-not (Wait-HTTP "http://127.0.0.1:6333/healthz" 30)) {
        throw "Qdrant did not become ready. See $qdrantLog"
    }
}

function Install-Ollama([string]$TempDir) {
    if (Test-HTTP "http://127.0.0.1:11434/api/version") { return }
    if ($SkipOllama) {
        throw "Ollama is not running. Install it from https://ollama.com/download/windows and rerun."
    }
    Write-Step "Installing Ollama for Windows"
    $installer = Join-Path $TempDir "OllamaSetup.exe"
    Invoke-Download "https://ollama.com/download/OllamaSetup.exe" $installer
    Assert-OllamaSignature $installer
    $process = Start-Process -FilePath $installer `
        -ArgumentList "/VERYSILENT /NORESTART /SUPPRESSMSGBOXES" -PassThru
    $process.WaitForExit()
    if ($process.ExitCode -ne 0) { throw "Ollama installer failed with exit code $($process.ExitCode)." }
    $ollamaDir = Join-Path $env:LOCALAPPDATA "Programs\Ollama"
    if (Test-Path $ollamaDir) { $env:PATH = "$ollamaDir;$env:PATH" }
    if (-not (Wait-HTTP "http://127.0.0.1:11434/api/version" 45)) {
        throw "Ollama did not become ready after installation."
    }
}

function Install-Models {
    if ($SkipModels) { return }
    $ollama = Get-Command "ollama.exe" -ErrorAction SilentlyContinue
    $ollamaPath = $null
    if ($ollama) { $ollamaPath = $ollama.Source }
    if (-not $ollama) {
        $fallback = Join-Path $env:LOCALAPPDATA "Programs\Ollama\ollama.exe"
        if (Test-Path $fallback) { $ollamaPath = $fallback }
    }
    if (-not $ollamaPath) { throw "ollama.exe was not found after installation." }
    Write-Step "Ensuring local models are present"
    & $ollamaPath pull $EmbeddingModel
    if ($LASTEXITCODE -ne 0) { throw "Could not pull $EmbeddingModel." }
    & $ollamaPath pull $SummaryModel
    if ($LASTEXITCODE -ne 0) { throw "Could not pull $SummaryModel." }
}

function Register-Codex([string]$AriadneExe) {
    $codex = Get-Command "codex" -ErrorAction SilentlyContinue
    if (-not $codex) {
        Write-Host "Codex CLI was not found; register $AriadneExe as a stdio MCP server when Codex is installed."
        return
    }
    $codexPath = $codex.Source
    Write-Step "Registering Ariadne with Codex"
    & $codexPath mcp remove ariadne 2>$null | Out-Null
    & $codexPath mcp add ariadne -- $AriadneExe
    if ($LASTEXITCODE -ne 0) { throw "Codex MCP registration failed." }
}

function Read-JsonObject([string]$Path) {
    if (-not (Test-Path $Path)) { return [pscustomobject]@{} }
    try {
        return Get-Content -Raw -Path $Path | ConvertFrom-Json
    } catch {
        throw "$Path is not valid JSON; refusing to overwrite it."
    }
}

function Set-ObjectProperty($Object, [string]$Name, $Value) {
    if ($Object.PSObject.Properties[$Name]) {
        $Object.$Name = $Value
    } else {
        $Object | Add-Member -MemberType NoteProperty -Name $Name -Value $Value
    }
}

function Register-Claude([string]$AriadneExe, [string]$PackageRoot) {
    $claudeHome = Join-Path $env:USERPROFILE ".claude"
    $claudeConfig = Join-Path $env:USERPROFILE ".claude.json"
    New-Item -ItemType Directory -Path $claudeHome -Force | Out-Null

    Write-Step "Registering Ariadne with Claude Code"
    if (Test-Path $claudeConfig) {
        Copy-Item $claudeConfig "$claudeConfig.bak-ariadne" -Force
    }
    $config = Read-JsonObject $claudeConfig
    $servers = if ($config.PSObject.Properties["mcpServers"]) { $config.mcpServers } else { [pscustomobject]@{} }
    Set-ObjectProperty $servers "ariadne" ([pscustomobject]@{
        type = "stdio"
        command = $AriadneExe
        args = @()
    })
    Set-ObjectProperty $config "mcpServers" $servers
    $config | ConvertTo-Json -Depth 30 | Set-Content -Path $claudeConfig -Encoding UTF8

    $skillSource = Join-Path $PackageRoot "skills\ariadne"
    if (Test-Path $skillSource) {
        $skillDest = Join-Path $claudeHome "skills\ariadne"
        if (Test-Path $skillDest) { Remove-Item -Path $skillDest -Recurse -Force }
        New-Item -ItemType Directory -Path (Split-Path $skillDest) -Force | Out-Null
        Copy-Item -Path $skillSource -Destination $skillDest -Recurse -Force
    }

    $settingsPath = Join-Path $claudeHome "settings.json"
    $settings = Read-JsonObject $settingsPath
    $existing = if (Test-Path $settingsPath) { Get-Content -Raw -Path $settingsPath } else { "" }
    if ($existing -notmatch "ariadne-hook") {
        if (Test-Path $settingsPath) { Copy-Item $settingsPath "$settingsPath.bak-ariadne" -Force }
        $hooks = if ($settings.PSObject.Properties["hooks"]) { $settings.hooks } else { [pscustomobject]@{} }
        $hookExe = Join-Path $BinDir "ariadne-hook.exe"
        $startHook = [pscustomobject]@{
            matcher = "startup|resume|clear"
            hooks = @([pscustomobject]@{ type = "command"; command = "`"$hookExe`" session-start"; timeout = 15 })
        }
        $endHook = [pscustomobject]@{
            hooks = @([pscustomobject]@{ type = "command"; command = "`"$hookExe`" session-end"; timeout = 10 })
        }
        $starts = if ($hooks.PSObject.Properties["SessionStart"]) { @($hooks.SessionStart) } else { @() }
        $ends = if ($hooks.PSObject.Properties["SessionEnd"]) { @($hooks.SessionEnd) } else { @() }
        Set-ObjectProperty $hooks "SessionStart" @($starts + $startHook)
        Set-ObjectProperty $hooks "SessionEnd" @($ends + $endHook)
        Set-ObjectProperty $settings "hooks" $hooks
        $settings | ConvertTo-Json -Depth 30 | Set-Content -Path $settingsPath -Encoding UTF8
    }
}

function Register-Tray {
    Write-Step "Registering the Ariadne tray monitor"
    $tray = Join-Path $BinDir "ariadne-tray.exe"
    Register-AriadneTask $TaskTray $tray "" $RuntimeDir $false
    Start-ScheduledTask -TaskName $TaskTray
}

if (-not [Environment]::Is64BitOperatingSystem) {
    throw "Ariadne requires 64-bit Windows."
}
if (-not $Update -and -not (Confirm-Install "Install Ariadne for the current Windows user?")) {
    Write-Host "Installation cancelled."
    exit 0
}

$tempDir = Join-Path $env:TEMP ("ariadne-install-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tempDir -Force | Out-Null
try {
    New-Item -ItemType Directory -Path $BinDir, $LogsDir, $DataDir -Force | Out-Null
    $release = Get-Release $Version
    $tag = [string]$release.tag_name
    $assetName = "ariadne-$tag-windows-amd64.zip"

    Write-Step "Downloading Ariadne $tag"
    $archive = Join-Path $tempDir $assetName
    $checksums = Join-Path $tempDir "SHA256SUMS"
    Invoke-Download (Get-AssetURL $release $assetName) $archive
    Invoke-Download (Get-AssetURL $release "SHA256SUMS") $checksums
    Assert-Checksum $archive $checksums $assetName

    $expanded = Join-Path $tempDir "ariadne"
    Expand-Archive -Path $archive -DestinationPath $expanded -Force
    $ariadneSource = Get-ChildItem -Path $expanded -Recurse -Filter "ariadne.exe" |
        Where-Object { $_.Directory.Name -eq "bin" } | Select-Object -First 1
    if (-not $ariadneSource) { throw "The Ariadne release archive has an unexpected layout." }
    $packageRoot = Split-Path $ariadneSource.Directory.FullName

    Get-Process -Name "ariadne-tray", "ariadne", "ariadnectl", "import", "ariadne-hook" `
        -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    foreach ($binary in Get-ChildItem -Path (Join-Path $packageRoot "bin") -Filter "*.exe") {
        Copy-Item -Path $binary.FullName -Destination (Join-Path $BinDir $binary.Name) -Force
    }
    Set-Content -Path (Join-Path $RuntimeDir "version") -Value $tag -Encoding ASCII

    Install-Qdrant $tempDir
    Install-Ollama $tempDir
    Install-Models

    $ariadneExe = Join-Path $BinDir "ariadne.exe"
    Register-Codex $ariadneExe
    Register-Claude $ariadneExe $packageRoot
    Register-Tray

    Write-Host "`nAriadne $tag is installed." -ForegroundColor Green
    Write-Host "Runtime: $RuntimeDir"
    Write-Host "Qdrant is bound to 127.0.0.1 and Ollama remains managed by its Windows app."
} finally {
    Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}
