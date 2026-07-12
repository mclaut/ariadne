# Ariadne Windows installer
[CmdletBinding()]
param(
    [string]$Version = "latest",
    [switch]$Yes,
    [switch]$Update,
    [ValidateSet("Auto", "Claude", "Codex", "Both", "None")]
    [string]$Integration = "Auto",
    [switch]$ConfigureClients,
    [switch]$CoreOnly,
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
$VCRuntimeURL = "https://aka.ms/vc14/vc_redist.x64.exe"
$VCRuntimeMinimum = [version]"14.44.0.0"
$UserAgent = "ariadne-windows-installer"
$IntegrationWasSpecified = $PSBoundParameters.ContainsKey("Integration")

function Write-Step([string]$Message) {
    Write-Host "`n==> $Message" -ForegroundColor Cyan
}

function Confirm-Install([string]$Message) {
    if ($Yes) { return $true }
    $answer = Read-Host "$Message [y/N]"
    return $answer -match "^[Yy]$"
}

function Find-ClientCommand([string]$Name, [string[]]$Candidates) {
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    foreach ($path in $Candidates) {
        if ($path -and (Test-Path $path)) { return $path }
    }
    return $null
}

function Find-Codex {
    return Find-ClientCommand "codex" @(
        (Join-Path $env:APPDATA "npm\codex.cmd"),
        (Join-Path $env:APPDATA "npm\codex.ps1"),
        (Join-Path $env:USERPROFILE ".local\bin\codex.exe"),
        (Join-Path $env:LOCALAPPDATA "Programs\codex\codex.exe")
    )
}

function Find-Claude {
    return Find-ClientCommand "claude" @(
        (Join-Path $env:APPDATA "npm\claude.cmd"),
        (Join-Path $env:APPDATA "npm\claude.ps1"),
        (Join-Path $env:USERPROFILE ".local\bin\claude.exe"),
        (Join-Path $env:LOCALAPPDATA "Programs\Claude\claude.exe")
    )
}

function Resolve-Integration([string]$CodexPath, [string]$ClaudePath) {
    if ($CoreOnly -and $script:IntegrationWasSpecified -and $Integration -ne "None") {
        throw "-CoreOnly cannot be combined with -Integration $Integration."
    }
    if ($Update -and -not $ConfigureClients -and -not $CoreOnly -and
        -not $script:IntegrationWasSpecified) {
        return "Preserve"
    }
    if ($CoreOnly) { return "None" }

    $selection = $Integration
    if ($selection -eq "Auto") {
        if ($ConfigureClients -and -not $ClaudePath -and -not $CodexPath) {
            throw "No supported AI client was detected. Install Claude Code or Codex CLI before -ConfigureClients."
        }
        if ($Yes) {
            throw "Non-interactive setup requires -Integration Claude, Codex, Both, or None (or -CoreOnly)."
        }
        Write-Host "`nAI clients detected:"
        Write-Host "  Claude Code: $(if ($ClaudePath) { 'Yes' } else { 'No' })"
        Write-Host "  Codex CLI  : $(if ($CodexPath) { 'Yes' } else { 'No' })"
        if (-not $ClaudePath -and -not $CodexPath) {
            if (Confirm-Install "No supported AI client was detected. Install Ariadne core only?") {
                return "None"
            }
            throw "Installation cancelled. Install Claude Code or Codex CLI, then rerun."
        }
        if ($ClaudePath -and -not $CodexPath) {
            if (Confirm-Install "Configure Ariadne for Claude Code?") { return "Claude" }
            return "None"
        }
        if ($CodexPath -and -not $ClaudePath) {
            if (Confirm-Install "Configure Ariadne for Codex CLI?") { return "Codex" }
            return "None"
        }
        Write-Host "Select client integrations:"
        Write-Host "  [1] Claude Code"
        Write-Host "  [2] Codex CLI"
        Write-Host "  [3] Both"
        Write-Host "  [4] None"
        $choice = Read-Host "Selection"
        $selection = switch ($choice) {
            "1" { "Claude" }
            "2" { "Codex" }
            "3" { "Both" }
            "4" { "None" }
            default { throw "Invalid integration selection: $choice" }
        }
    }

    if (($selection -eq "Claude" -or $selection -eq "Both") -and -not $ClaudePath) {
        throw "Claude Code was selected but its CLI was not found. Install Claude Code or choose another -Integration."
    }
    if (($selection -eq "Codex" -or $selection -eq "Both") -and -not $CodexPath) {
        throw "Codex was selected but its CLI was not found. Install Codex CLI or choose another -Integration."
    }
    return $selection
}

function Test-VirtualMachine {
    try {
        $system = Get-CimInstance Win32_ComputerSystem
        $bios = Get-CimInstance Win32_BIOS
        $text = @($system.Manufacturer, $system.Model, $bios.Manufacturer, $bios.SMBIOSBIOSVersion) -join " "
        return $text -match "VMware|VirtualBox|KVM|QEMU|Hyper-V|Xen|Virtual Machine|Parallels"
    } catch {
        return $false
    }
}

function Get-HardwareInfo {
    try {
        $computer = Get-CimInstance Win32_ComputerSystem
        $cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
        $os = Get-CimInstance Win32_OperatingSystem
        $drive = Get-CimInstance Win32_LogicalDisk -Filter "DeviceID='$($env:SystemDrive)'"
        $machineType = "Physical"
        if (Test-VirtualMachine) { $machineType = "Virtual" }
        return [pscustomobject]@{
            MachineType = $machineType
            Manufacturer = $computer.Manufacturer
            Model = $computer.Model
            CPU = $cpu.Name
            Cores = [int]$cpu.NumberOfCores
            LogicalCPUs = [int]$cpu.NumberOfLogicalProcessors
            RAMGB = [math]::Round($computer.TotalPhysicalMemory / 1GB, 1)
            FreeDiskGB = [math]::Round($drive.FreeSpace / 1GB, 1)
            Windows = $os.Caption
            WindowsVersion = $os.Version
        }
    } catch {
        Write-Warning "Could not read hardware details: $($_.Exception.Message)"
        return $null
    }
}

function Test-SystemRequirements {
    if ($PSVersionTable.PSVersion -lt [version]"5.1") {
        throw "Ariadne requires Windows PowerShell 5.1 or newer."
    }
    if (-not [Environment]::Is64BitOperatingSystem) {
        throw "Ariadne requires 64-bit Windows."
    }
    if (-not (Get-Command Register-ScheduledTask -ErrorAction SilentlyContinue)) {
        throw "Windows Scheduled Tasks are unavailable; Ariadne cannot register its local services."
    }

    $info = Get-HardwareInfo
    if (-not $info) { return }
    Write-Host "`nSystem checks"
    Write-Host "  Machine : $($info.MachineType) - $($info.Manufacturer) $($info.Model)"
    Write-Host "  Windows : $($info.Windows) $($info.WindowsVersion)"
    Write-Host "  CPU     : $($info.CPU) ($($info.Cores) cores / $($info.LogicalCPUs) threads)"
    Write-Host "  RAM     : $($info.RAMGB) GB"
    Write-Host "  Free disk: $($info.FreeDiskGB) GB"

    if (-not $SkipModels) {
        if ($info.RAMGB -lt 8) { throw "Ariadne requires at least 8 GB RAM for the default local models." }
        if ($info.FreeDiskGB -lt 15) { throw "Ariadne requires at least 15 GB free disk for the default local models." }
        if ($info.RAMGB -lt 16) { Write-Warning "16 GB RAM is recommended for the default qwen2.5:7b summary model." }
    }
    if ($info.Cores -lt 4) { Write-Warning "At least 4 CPU cores are recommended." }
    if ($info.MachineType -eq "Virtual") {
        if ($info.LogicalCPUs -lt 4) { Write-Warning "Assign at least 4 vCPUs to this virtual machine." }
        Write-Host "  VM note : expose a modern CPU type (for Proxmox/KVM, prefer 'host')."
    }
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

function Assert-MicrosoftSignature([string]$File) {
    $signature = Get-AuthenticodeSignature -FilePath $File
    if ($signature.Status -ne "Valid") { throw "Microsoft VC++ Runtime signature is not valid." }
    if ($signature.SignerCertificate.Subject -notmatch "(^|, )O=Microsoft Corporation(,|$)") {
        throw "Microsoft VC++ Runtime has an unexpected signer."
    }
}

function Test-VCRuntime {
    $runtime = Join-Path $env:WINDIR "System32\vcruntime140.dll"
    if (-not (Test-Path $runtime)) { return $false }
    try {
        $text = (Get-Item $runtime).VersionInfo.FileVersion
        $match = [regex]::Match($text, "[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+")
        if (-not $match.Success) { return $false }
        return ([version]($match.Value) -ge $VCRuntimeMinimum)
    } catch {
        return $false
    }
}

function Install-VCRuntime([string]$TempDir) {
    if (Test-VCRuntime) { return }

    Write-Step "Installing Microsoft Visual C++ Runtime required by Qdrant"
    Write-Host "Windows may request administrator approval for this Microsoft prerequisite."
    $installer = Join-Path $TempDir "vc_redist.x64.exe"
    $installLog = Join-Path $LogsDir "vc-redist.log"
    Invoke-Download $VCRuntimeURL $installer
    Assert-MicrosoftSignature $installer
    $arguments = "/install /passive /norestart /log `"$installLog`""
    try {
        $process = Start-Process -FilePath $installer -ArgumentList $arguments `
            -Verb RunAs -Wait -PassThru
    } catch {
        throw "Qdrant requires Microsoft Visual C++ Runtime 14.44 or newer. " +
            "Administrator approval was not completed. Install $VCRuntimeURL and rerun Ariadne. " +
            "Original error: $($_.Exception.Message)"
    }
    if (-not (Test-VCRuntime)) {
        throw "Microsoft Visual C++ Runtime did not become available (exit code $($process.ExitCode)). " +
            "See $installLog"
    }
    if ($process.ExitCode -eq 3010) {
        Write-Host "Microsoft VC++ Runtime requested a restart; continuing because the runtime is available."
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

function Show-QdrantFailure([string]$LogPath) {
    Write-Host "`nQdrant startup diagnostics:" -ForegroundColor Yellow
    try {
        $taskInfo = Get-ScheduledTaskInfo -TaskName $TaskQdrant -ErrorAction Stop
        Write-Host "Scheduled task last result: $($taskInfo.LastTaskResult)"
    } catch {
        Write-Host "Scheduled task status could not be read: $($_.Exception.Message)"
    }

    $lines = @()
    if (Test-Path $LogPath) {
        $lines = @(Get-Content -Path $LogPath -Tail 40 -ErrorAction SilentlyContinue |
            Where-Object { $_ -and $_.Trim().Length -gt 0 })
    }
    if ($lines.Count -gt 0) {
        Write-Host "--- last Qdrant log lines ---"
        $lines | ForEach-Object { Write-Host $_ }
        Write-Host "--- end Qdrant log ---"
    } else {
        Write-Host "No Qdrant output was captured. Windows may have rejected the executable before startup."
    }
    if (-not (Test-VCRuntime)) {
        Write-Host "Microsoft Visual C++ Runtime 14.44 or newer is still unavailable."
    }
}

function Install-Qdrant([string]$TempDir) {
    if (Test-HTTP "http://127.0.0.1:6333/healthz") {
        Write-Host "Qdrant is already running; reusing it without reconfiguration."
        return
    }

    Install-VCRuntime $TempDir

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

    $qdrantVersionOutput = & $qdrantExe --version 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "qdrant.exe cannot run. Check Windows, Microsoft VC++ Runtime, and the VM CPU type. " +
            "Output: $($qdrantVersionOutput -join ' ')"
    }
    Write-Host "Qdrant executable check: $($qdrantVersionOutput -join ' ')"

    Write-Step "Registering loopback-only Qdrant"
    $launcher = Join-Path $RuntimeDir "start-qdrant.ps1"
    $qdrantLog = Join-Path $LogsDir "qdrant.log"
    Set-Content -Path $qdrantLog -Value "" -Encoding UTF8
    $content = @(
        '$ErrorActionPreference = "Stop"',
        ('$env:QDRANT__SERVICE__HOST = ' + (ConvertTo-PSLiteral "127.0.0.1")),
        ('$env:QDRANT__STORAGE__STORAGE_PATH = ' + (ConvertTo-PSLiteral $DataDir)),
        ('$env:QDRANT__STORAGE__SNAPSHOTS_PATH = ' + (ConvertTo-PSLiteral (Join-Path $DataDir "snapshots"))),
        ('Set-Location ' + (ConvertTo-PSLiteral $RuntimeDir)),
        'try {',
        ('    & ' + (ConvertTo-PSLiteral $qdrantExe) + ' *>> ' + (ConvertTo-PSLiteral $qdrantLog)),
        '    if ($LASTEXITCODE -ne 0) { throw "qdrant.exe exited with code $LASTEXITCODE" }',
        '} catch {',
        ('    ($_ | Out-String) | Add-Content -Path ' + (ConvertTo-PSLiteral $qdrantLog)),
        '    exit 1',
        '}'
    ) -join "`r`n"
    Set-Content -Path $launcher -Value $content -Encoding UTF8

    $taskArgs = "-NoProfile -NonInteractive -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$launcher`""
    Register-AriadneTask $TaskQdrant "powershell.exe" $taskArgs $RuntimeDir $true
    Start-ScheduledTask -TaskName $TaskQdrant
    if (-not (Wait-HTTP "http://127.0.0.1:6333/healthz" 60)) {
        Show-QdrantFailure $qdrantLog
        throw "Qdrant did not become ready after 60 seconds. See $qdrantLog"
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

function Register-Codex([string]$AriadneExe, [string]$CodexPath) {
    Write-Step "Registering Ariadne with Codex"
    & $CodexPath mcp remove ariadne 2>$null | Out-Null
    & $CodexPath mcp add ariadne -- $AriadneExe
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

function Register-Claude([string]$AriadneExe, [string]$PackageRoot, [string]$ClaudePath) {
    $claudeHome = Join-Path $env:USERPROFILE ".claude"
    $claudeConfig = Join-Path $env:USERPROFILE ".claude.json"
    New-Item -ItemType Directory -Path $claudeHome -Force | Out-Null

    Write-Step "Registering Ariadne with Claude Code"
    Write-Host "Claude CLI: $ClaudePath"
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

function Configure-SelectedClients(
    [string]$Selection,
    [string]$AriadneExe,
    [string]$PackageRoot,
    [string]$CodexPath,
    [string]$ClaudePath
) {
    switch ($Selection) {
        "Claude" { Register-Claude $AriadneExe $PackageRoot $ClaudePath }
        "Codex" { Register-Codex $AriadneExe $CodexPath }
        "Both" {
            Register-Claude $AriadneExe $PackageRoot $ClaudePath
            Register-Codex $AriadneExe $CodexPath
        }
        "None" {
            Write-Host "No AI client configuration was changed."
            Write-Host "After installing a client, run:"
            Write-Host "  .\install.ps1 -ConfigureClients -Integration Claude"
            Write-Host "  .\install.ps1 -ConfigureClients -Integration Codex"
        }
        "Preserve" { Write-Host "Existing AI client configurations are preserved unchanged during update." }
        default { throw "Unsupported integration selection: $Selection" }
    }
}

if ($ConfigureClients -and $Update) {
    throw "-ConfigureClients cannot be combined with -Update."
}
if ($ConfigureClients -and $CoreOnly) {
    throw "-ConfigureClients cannot be combined with -CoreOnly."
}
if ($ConfigureClients) {
    if ($PSVersionTable.PSVersion -lt [version]"5.1") {
        throw "Ariadne requires Windows PowerShell 5.1 or newer."
    }
} else {
    Test-SystemRequirements
}
$codexPath = Find-Codex
$claudePath = Find-Claude
$clientSelection = Resolve-Integration $codexPath $claudePath
if ($ConfigureClients -and $clientSelection -eq "None") {
    throw "-ConfigureClients requires Claude, Codex, or Both."
}
if ($ConfigureClients -and -not (Test-Path (Join-Path $BinDir "ariadne.exe"))) {
    throw "Ariadne core is not installed. Run install.ps1 before -ConfigureClients."
}

if (-not $Update -and -not $ConfigureClients -and -not (Confirm-Install "Install Ariadne for the current Windows user?")) {
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

    if ($ConfigureClients) {
        $ariadneExe = Join-Path $BinDir "ariadne.exe"
        Configure-SelectedClients $clientSelection $ariadneExe $packageRoot $codexPath $claudePath
        Write-Host "`nAriadne client integration is configured." -ForegroundColor Green
        exit 0
    }

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
    Configure-SelectedClients $clientSelection $ariadneExe $packageRoot $codexPath $claudePath
    Register-Tray

    Write-Host "`nAriadne $tag is installed." -ForegroundColor Green
    Write-Host "Runtime: $RuntimeDir"
    Write-Host "Qdrant is bound to 127.0.0.1 and Ollama remains managed by its Windows app."
} finally {
    Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}
