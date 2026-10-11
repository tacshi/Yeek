<#
Builds Yeek's Windows release installers on Windows, for x64 and arm64:
per-user installers that let the app update itself. It is
scripts/win-release.sh for Windows.

  scripts/win-release.ps1                 # the version in mygo.json
  scripts/win-release.ps1 0.2.0           # another version
  scripts/win-release.ps1 -Arch x64       # only some: x64, arm64

The installers are copied to build/windows-release/dist as
Yeek-<version>-{x64,arm64}-setup.exe. With the update signing key
(MYGO_UPDATER_PRIVATE_KEY, or the file `go tool mygo keygen` wrote, which
YEEK_UPDATE_KEY overrides), each build also gets its signed update archive
and manifest.

MyGo uses an installed NSIS, else downloads it. The installers are not code
signed, so SmartScreen warns about them until Yeek has a Windows signing
certificate. -Changelog, -Out and -Upload are as in win-release.sh.
#>
[CmdletBinding()]
param(
	[Parameter(Position = 0)][string]$Version,
	[ValidateSet('x64', 'amd64', 'arm64')][string[]]$Arch = @('x64', 'arm64'),
	[switch]$Upload,
	[string]$Changelog,
	[string]$Out
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Fail([string]$message) {
	[Console]::Error.WriteLine("win-release.ps1: $message")
	exit 1
}
function Info([string]$message) { Write-Host "==> $message" }

$RootDir = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
Set-Location $RootDir
if (-not $Out) { $Out = Join-Path $RootDir 'build/windows-release' }
if (-not [IO.Path]::IsPathRooted($Out)) { $Out = Join-Path $RootDir $Out }
$Out = [IO.Path]::GetFullPath($Out)

if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Fail 'needs go' }

$ConfigPath = Join-Path $RootDir 'mygo.json'
$Committed = [IO.File]::ReadAllText($ConfigPath)
if (-not $Version) { $Version = ($Committed | ConvertFrom-Json).version }
$Version = $Version -replace '^v', ''
if ($Version -notmatch '^\d+\.\d+\.\d+$') { Fail "the version must look like 1.2.3, not $Version" }

$Arch = @($Arch | ForEach-Object { if ($_ -eq 'amd64') { 'x64' } else { $_ } } | Select-Object -Unique)
$Platforms = $Arch | ForEach-Object { if ($_ -eq 'x64') { 'windows/amd64' } else { 'windows/arm64' } }

# The update signing key, as load_update_key in lib/release.sh finds it:
# `go tool mygo keygen` writes it under the user config directory.
if (-not $env:MYGO_UPDATER_PRIVATE_KEY) {
	$keyFile = if ($env:YEEK_UPDATE_KEY) { $env:YEEK_UPDATE_KEY } else { Join-Path $env:APPDATA 'mygo/update-keys/mygo-update.key' }
	if (Test-Path -LiteralPath $keyFile -PathType Leaf) {
		$env:MYGO_UPDATER_PRIVATE_KEY = [IO.File]::ReadAllText($keyFile).Trim()
	} elseif ($Upload) {
		Fail 'no update signing key: set MYGO_UPDATER_PRIVATE_KEY, or YEEK_UPDATE_KEY to the key file'
	} else {
		Write-Warning 'no update signing key; building without update archives'
	}
}

# The build's version and changelog go in mygo.json for this run only; the
# committed file is restored however the script ends.
$config = $Committed | ConvertFrom-Json
$config.version = $Version
if ($Changelog) { $config.updates | Add-Member -NotePropertyName changelog -NotePropertyValue $Changelog -Force }
$utf8 = [Text.UTF8Encoding]::new($false)
[IO.File]::WriteAllText($ConfigPath, ($config | ConvertTo-Json -Depth 32) + "`n", $utf8)
$cgo = $env:CGO_ENABLED
try {
	$buildArgs = @('tool', 'mygo', 'build', '-platform', ($Platforms -join ','), '-o', $Out)
	if ($Upload) { $buildArgs += '-upload' }
	Info "building Yeek $Version for Windows $($Arch -join ' ')"
	# No cgo: the 1Password SDK needs none on Windows, and arm64 cross-builds.
	$env:CGO_ENABLED = '0'
	& go @buildArgs
	if ($LASTEXITCODE -ne 0) { Fail "mygo build failed ($LASTEXITCODE)" }
} finally {
	$env:CGO_ENABLED = $cgo
	[IO.File]::WriteAllText($ConfigPath, $Committed, $utf8)
}

$dist = Join-Path $Out 'dist'
New-Item -ItemType Directory -Force -Path $dist | Out-Null
foreach ($a in $Arch) {
	$goArch = if ($a -eq 'x64') { 'amd64' } else { 'arm64' }
	$setup = Join-Path $Out "windows-$goArch/Yeek Setup $Version $goArch.exe"
	if (-not (Test-Path -LiteralPath $setup -PathType Leaf)) { Fail "expected $setup after the build" }
	$target = Join-Path $dist "Yeek-$Version-$a-setup.exe"
	Copy-Item -LiteralPath $setup -Destination $target -Force
	Info $target
}
