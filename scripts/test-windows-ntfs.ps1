# Run from a checkout, or place this script beside traffic-amd64.test.exe
# or traffic-arm64.test.exe. Use -TestBinary to select a binary explicitly.
[CmdletBinding()]
param([string]$TestBinary)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    throw 'The NTFS capture tests require Windows.'
}

$tests = @(
    'TestWindowsCaptureACLProtectsOldAndStagedFiles'
    'TestWindowsCaptureACLRefusesExternalHardlink'
    'TestWindowsCaptureACLRefusesReparsePoints'
    'TestPrivateAtomicCaptureBodies'
)
$filter = '^(' + ($tests -join '|') + ')$'

if (-not $TestBinary) {
    $arch = $env:PROCESSOR_ARCHITEW6432
    if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
    $adjacent = Join-Path $PSScriptRoot ("traffic-{0}.test.exe" -f $arch.ToLowerInvariant())
    if (Test-Path -LiteralPath $adjacent -PathType Leaf) { $TestBinary = $adjacent }
}
if ($TestBinary) {
    $command = (Get-Item -LiteralPath $TestBinary).FullName
    $arguments = @('-test.v', '-test.count=1', '-test.run', $filter)
} else {
    $command = 'go'
    $arguments = @('-C', (Split-Path -Parent $PSScriptRoot), 'test', '-count=1', '-v', '-run', $filter, './traffic')
}

$probe = [IO.Path]::GetTempFileName()
try {
    $volume = @(Get-Volume -FilePath $probe)
    if ($volume.Count -ne 1 -or $volume[0].FileSystem -ne 'NTFS') {
        throw "The test scratch file $probe must be on NTFS. Detected filesystem: $($volume.FileSystem -join ', ')."
    }
    $scratch = [IO.Path]::GetDirectoryName($probe)
    Write-Host "Verified NTFS test scratch directory: $scratch"
} finally {
    [IO.File]::Delete($probe)
}

# GetTempPath2 uses SystemTemp for LocalSystem and TMP/TEMP for other users.
$previous = @{}
$results = @{}
$unexpected = @()
try {
    foreach ($key in 'TMP', 'TEMP', 'SystemTemp') {
        $previous[$key] = [Environment]::GetEnvironmentVariable($key)
        [Environment]::SetEnvironmentVariable($key, $scratch)
    }
    & $command @arguments | ForEach-Object {
        Write-Host $_
        if ($_ -cmatch '^--- (PASS|SKIP|FAIL): (\S+) \(') {
            $name = $Matches[2]
            if ($tests -ccontains $name) {
                $results[$name] += @($Matches[1])
            } else {
                $unexpected += $name
            }
        }
    }
    $code = $LASTEXITCODE
} finally {
    foreach ($key in $previous.Keys) {
        [Environment]::SetEnvironmentVariable($key, $previous[$key])
    }
}
if ($code -ne 0) { exit $code }
if ($unexpected.Count) { throw "Unexpected test results for $($unexpected -join ', ')." }
foreach ($test in $tests) {
    $status = $results[$test] -join ', '
    if ($status -cne 'PASS') {
        if (-not $status) { $status = 'missing' }
        throw "Expected one PASS for ${test}. Observed $status."
    }
}
Write-Host 'All four NTFS capture tests passed.'
