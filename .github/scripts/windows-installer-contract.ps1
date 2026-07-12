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
foreach ($name in @("Integration", "ConfigureClients", "CoreOnly")) {
    if ($parameters -notcontains $name) { throw "Missing installer parameter: $name" }
}

foreach ($functionName in @(
    "Find-Codex", "Find-Claude", "Resolve-Integration", "Get-HardwareInfo",
    "Test-VirtualMachine", "Test-SystemRequirements", "Configure-SelectedClients"
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

Write-Host "Windows installer integration and hardware contract passed."
