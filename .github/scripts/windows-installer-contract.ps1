# Static contract checks for the Windows installer's client-selection surface.
[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$repoRoot = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$installer = Join-Path $repoRoot "install.ps1"
$text = Get-Content -Raw -Path $installer
$tokens = $null
$errors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseFile(
    $installer, [ref]$tokens, [ref]$errors
)
if ($errors.Count -gt 0) {
    $errors | Format-List | Out-String | Write-Error
}

$parameters = $ast.ParamBlock.Parameters.Name.VariablePath.UserPath
foreach ($name in @(
    "Integration", "ConfigureClients", "CoreOnly", "QdrantHost",
    "QdrantRestPort", "QdrantGrpcPort", "QdrantApiKeyFile", "QdrantTls",
    "AllowInsecureRemoteQdrant"
)) {
    if ($parameters -notcontains $name) { throw "Missing installer parameter: $name" }
}

foreach ($functionName in @(
    "Find-Codex", "Find-Claude", "Resolve-Integration", "Get-HardwareInfo",
    "Test-VirtualMachine", "Test-SystemRequirements", "Configure-SelectedClients",
    "Assert-QdrantConfig", "Get-QdrantHeaders", "Get-AriadneMCPEnvironment",
    "Write-AriadneEnvironmentLauncher", "Import-QdrantEnvironmentDefaults"
)) {
    if ($text -notmatch ("(?m)^function " + [regex]::Escape($functionName) + "\b")) {
        throw "Missing installer function: $functionName"
    }
}

foreach ($value in @("Auto", "Claude", "Codex", "Both", "None")) {
    if ($text -notmatch ('ValidateSet\([^\)]*"' + [regex]::Escape($value) + '"')) {
        throw "Integration ValidateSet is missing: $value"
    }
}

if ($text -notmatch 'Non-interactive setup requires -Integration') {
    throw "Non-interactive integration selection is not enforced."
}
if ($text -notmatch 'Existing AI client configurations are preserved unchanged during update') {
    throw "Update mode does not document its preserve-config behavior."
}
if ($text -notmatch '\$qdrantVersionOutput\s*=\s*&\s*\$qdrantExe\s+--version') {
    throw "The direct Qdrant executable check is missing."
}
if ($text -notmatch 'ARIADNE_QDRANT_API_KEY_FILE' -or $text -notmatch 'Remote Qdrant requires -QdrantApiKeyFile') {
    throw "Remote Qdrant key-file policy is missing."
}
if ($text -notmatch 'ARIADNE_QDRANT_REST must be an absolute URL') {
    throw "Remote Qdrant environment is not preserved across self-update."
}
if ($parameters -contains "QdrantApiKey") {
    throw "The Windows installer must not accept a raw Qdrant key parameter."
}

$importDefaults = $ast.Find({
    param($node)
    $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and
        $node.Name -eq "Import-QdrantEnvironmentDefaults"
}, $true)
Invoke-Expression $importDefaults.Extent.Text
$script:InstallerBoundParameters = @{}
$script:QdrantHost = "127.0.0.1"
$script:QdrantRestPort = 6333
$script:QdrantGrpcPort = 6334
$script:QdrantApiKeyFile = ""
$script:QdrantTls = $false
$script:AllowInsecureRemoteQdrant = $false
$env:ARIADNE_QDRANT_HOST = "qdrant.example"
$env:ARIADNE_QDRANT_PORT = "7444"
$env:ARIADNE_QDRANT_REST = "https://qdrant.example:7443"
$env:ARIADNE_QDRANT_API_KEY_FILE = "C:\Ariadne\qdrant.key"
$env:ARIADNE_QDRANT_TLS = "1"
Import-QdrantEnvironmentDefaults
if ($script:QdrantHost -ne "qdrant.example" -or $script:QdrantRestPort -ne 7443 -or
    $script:QdrantGrpcPort -ne 7444 -or -not $script:QdrantTls -or
    $script:QdrantApiKeyFile -ne "C:\Ariadne\qdrant.key") {
    throw "Remote Qdrant environment is not inherited correctly during self-update."
}
$script:InstallerBoundParameters = @{ QdrantHost = $true }
$script:QdrantHost = "explicit.example"
Import-QdrantEnvironmentDefaults
if ($script:QdrantHost -ne "explicit.example") {
    throw "Explicit Qdrant parameters must override inherited self-update environment."
}

Write-Host "Windows installer integration and hardware contract passed."
