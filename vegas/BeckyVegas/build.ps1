# build.ps1 - compiles BeckyVegas.dll (the VEGAS Pro 18 extension) with the
# Roslyn compiler from Visual Studio Build Tools, against the .NET Framework 4.8
# reference assemblies and VEGAS's own ScriptPortal.Vegas.dll.
#
#   powershell -ExecutionPolicy Bypass -File vegas\BeckyVegas\build.ps1
#
# Output: vegas\BeckyVegas\bin\BeckyVegas.dll. Nothing is installed by this script.
# ASCII only: a double-clicked .ps1 runs under PowerShell 5.1, which misreads UTF-8.
param(
    [string]$VegasDir = "C:\Program Files\VEGAS\VEGAS Pro 18.0"
)
$ErrorActionPreference = "Stop"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path

$csc = @(
    "C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\MSBuild\Current\Bin\Roslyn\csc.exe",
    "C:\Program Files\Microsoft Visual Studio\2022\Community\MSBuild\Current\Bin\Roslyn\csc.exe",
    "C:\Program Files (x86)\Microsoft Visual Studio\2019\BuildTools\MSBuild\Current\Bin\Roslyn\csc.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $csc) { throw "Roslyn csc.exe not found (install Visual Studio Build Tools)." }

$ref = "C:\Program Files (x86)\Reference Assemblies\Microsoft\Framework\.NETFramework\v4.8"
if (-not (Test-Path "$ref\mscorlib.dll")) { throw ".NET Framework 4.8 reference assemblies not found at $ref" }
$vegasApi = Join-Path $VegasDir "ScriptPortal.Vegas.dll"
if (-not (Test-Path $vegasApi)) { throw "ScriptPortal.Vegas.dll not found in $VegasDir" }

$outDir = if ($env:BECKYVEGAS_OUT) { $env:BECKYVEGAS_OUT } else { Join-Path $here "bin" }
New-Item -ItemType Directory -Force -Path $outDir | Out-Null
$out = Join-Path $outDir "BeckyVegas.dll"
$sources = Get-ChildItem -Path $here -Filter *.cs | ForEach-Object { $_.FullName }

$args = @(
    "/nologo", "/noconfig", "/nostdlib+", "/target:library", "/optimize+",
    "/langversion:7.3", "/warn:4", "/nowarn:1591",
    "/out:$out",
    "/r:$ref\mscorlib.dll",
    "/r:$ref\System.dll",
    "/r:$ref\System.Core.dll",
    "/r:$ref\System.Drawing.dll",
    "/r:$ref\System.Windows.Forms.dll",
    "/r:$ref\System.Web.Extensions.dll",
    "/r:$vegasApi"
) + $sources

& $csc @args
if ($LASTEXITCODE -ne 0) { throw "BUILD FAILED (csc exit $LASTEXITCODE)" }
Write-Host "BUILD OK -> $out"
