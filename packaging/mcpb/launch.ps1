$ErrorActionPreference = "Stop"
$architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
if ($architecture -ne "X64") {
    Write-Error "Ariadne MCPB currently supports x64 Windows."
    exit 1
}

& (Join-Path $PSScriptRoot "windows-amd64\ariadne.exe")
exit $LASTEXITCODE
