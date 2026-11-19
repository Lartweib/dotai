# Installs dotai from a GitHub release on Windows (decision 0048).
#
#   irm https://raw.githubusercontent.com/Lartweib/dotai/main/install.ps1 | iex
#   $env:DOTAI_VERSION = '0.1.0'; irm https://raw.githubusercontent.com/Lartweib/dotai/main/install.ps1 | iex
#
# It downloads the Windows archive and SHA256SUMS from the release over
# HTTPS, checks the archive against SHA256SUMS before extracting it, and
# moves dotai.exe into the install directory. It needs no administrator
# rights. When that directory is not on Path, it appends it to the user's
# Path, keeping the existing value as it is; DOTAI_NO_MODIFY_PATH=1 only
# prints the command instead.
#
# Environment:
#   DOTAI_VERSION           the release to install, such as 0.1.0 or v0.1.0
#                           (default: the latest release)
#   DOTAI_INSTALL_DIR       where dotai.exe goes, an absolute path
#                           (default: $env:LOCALAPPDATA\Programs\dotai)
#   DOTAI_NO_MODIFY_PATH=1  never change the user's Path
#   DOTAI_RELEASE_BASE_URL  the releases URL, for tests and mirrors: https,
#                           or http on 127.0.0.1 or localhost only
#                           (default: https://github.com/Lartweib/dotai/releases)
#
# For the tests only, never for users:
#   DOTAI_TEST_USER_ENV_KEY an HKCU subkey under Software\dotai-install-test\
#                           that stands in for HKCU\Environment, so that a
#                           test never changes the real user Path
#   DOTAI_TEST_FAIL_SWAP=1  fail between the two renames of the update
#
# Everything runs inside a script block, so that under iex the preferences
# set here and an error never end the caller's session.
& {
  Set-StrictMode -Version 2.0
  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue' # Invoke-WebRequest is slow with a progress bar on 5.1
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

  if ([Environment]::OSVersion.Platform -ne 'Win32NT') {
    throw 'dotai install: install.ps1 is for Windows; use install.sh on Linux and macOS'
  }
  $arch = $env:PROCESSOR_ARCHITEW6432
  if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
  if ($arch -ne 'AMD64') {
    throw "dotai install: unsupported architecture $arch; the Windows release is built for amd64 (x64) only"
  }

  $base = $env:DOTAI_RELEASE_BASE_URL
  if (-not $base) { $base = 'https://github.com/Lartweib/dotai/releases' }
  $base = $base.TrimEnd('/')
  if ($base -notmatch '^https://' -and $base -notmatch '^http://(127\.0\.0\.1|localhost)(:\d+)?(/|$)') {
    throw "dotai install: DOTAI_RELEASE_BASE_URL must be an https URL, or http on 127.0.0.1 or localhost: $base"
  }
  # ConvertTo-InstallDir checks that path is absolute, a drive path or a
  # UNC share, and drops trailing \ unless the path is a root such as C:\.
  # It keeps the path as given rather than through GetFullPath, which on
  # Windows PowerShell 5.1 expands 8.3 names such as RUNNER~1; the Path
  # check would then miss an entry written the short way.
  function ConvertTo-InstallDir([string]$path) {
    if ($path -notmatch '^([A-Za-z]:\\|\\\\[^\\]+\\[^\\]+)') {
      throw "dotai install: DOTAI_INSTALL_DIR must be an absolute path: $path"
    }
    $root = [IO.Path]::GetPathRoot($path)
    $trimmed = $path.TrimEnd('\')
    if ($trimmed.Length -le $root.TrimEnd('\').Length) { return $root }
    return $trimmed
  }
  # End of ConvertTo-InstallDir.

  # Get-File downloads url into file. With an https base URL, a redirect
  # that ends outside https is refused.
  function Get-File([string]$url, [string]$file) {
    $resp = [Net.WebRequest]::Create($url).GetResponse()
    try {
      if ($base -match '^https://' -and $resp.ResponseUri.Scheme -ne 'https') {
        throw "redirected to $($resp.ResponseUri.AbsoluteUri), which is not https"
      }
      $in = $resp.GetResponseStream()
      $out = [IO.File]::Create($file)
      try { $in.CopyTo($out) } finally { $out.Close(); $in.Close() }
    } finally {
      $resp.Close()
    }
  }

  $dir = $env:DOTAI_INSTALL_DIR
  if (-not $dir) { $dir = Join-Path $env:LOCALAPPDATA 'Programs\dotai' }
  $dir = ConvertTo-InstallDir $dir

  $version = $env:DOTAI_VERSION
  if (-not $version) {
    # The latest release is the tag that <base>/latest redirects to.
    try {
      $req = [Net.WebRequest]::Create("$base/latest")
      $req.Method = 'HEAD'
      $resp = $req.GetResponse()
      $url = $resp.ResponseUri.AbsoluteUri
      $resp.Close()
    } catch {
      throw "dotai install: cannot reach $base/latest: $($_.Exception.Message)"
    }
    if ($base -match '^https://' -and $url -notmatch '^https://') {
      throw "dotai install: $base/latest redirected to $url, which is not https"
    }
    if ($url -notmatch '/tag/([^/?#]+)$') { throw "dotai install: no release found at $base/latest" }
    $version = $Matches[1]
  }
  $version = $version -replace '^v', ''
  if ($version -notmatch '^[0-9A-Za-z.-]+$') { throw "dotai install: invalid version: $version" }
  $tag = "v$version"
  $archive = "dotai_${version}_windows_amd64.zip"

  $tmp = Join-Path ([IO.Path]::GetTempPath()) ("dotai-install-" + [Guid]::NewGuid())
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    Write-Host "downloading dotai $tag for windows/amd64"
    try {
      Get-File "$base/download/$tag/$archive" (Join-Path $tmp $archive)
    } catch {
      throw "dotai install: cannot download $base/download/$tag/$archive; release $tag may not exist or have no windows_amd64 archive: $($_.Exception.Message)"
    }
    try {
      Get-File "$base/download/$tag/SHA256SUMS" (Join-Path $tmp 'SHA256SUMS')
    } catch {
      throw "dotai install: cannot download $base/download/$tag/SHA256SUMS: $($_.Exception.Message)"
    }

    $want = $null
    foreach ($line in Get-Content (Join-Path $tmp 'SHA256SUMS')) {
      $f = $line -split '\s+', 2
      if ($f.Count -eq 2 -and ($f[1] -eq $archive -or $f[1] -eq "*$archive")) { $want = $f[0]; break }
    }
    if (-not $want) { throw "dotai install: SHA256SUMS of $tag has no line for $archive" }
    $got = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $tmp $archive)).Hash
    if ($got -ne $want) {
      throw "dotai install: checksum mismatch for ${archive}: got $got, want $want; nothing was installed"
    }
    Write-Host 'checksum verified'

    Expand-Archive -LiteralPath (Join-Path $tmp $archive) -DestinationPath (Join-Path $tmp 'x')
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $exe = Join-Path $dir 'dotai.exe'
    $id = [Guid]::NewGuid().ToString('N')
    $new = Join-Path $dir "dotai.exe.new-$id"
    $old = Join-Path $dir "dotai.exe.old-$id"
    # A running dotai.exe cannot be overwritten but can be renamed, so the
    # old one is moved aside under a unique name and the new one renamed
    # into place. The finally block undoes a half-done swap on any stop,
    # Ctrl+C included. Only after a successful swap are old ones removed,
    # this one and any left by an earlier run while still running.
    $moved = $false
    $done = $false
    $reason = 'interrupted'
    $restored = $true
    try {
      Copy-Item -LiteralPath (Join-Path $tmp 'x\dotai.exe') -Destination $new
      if (Test-Path -LiteralPath $exe) {
        Move-Item -LiteralPath $exe -Destination $old
        $moved = $true
      }
      if ($env:DOTAI_TEST_FAIL_SWAP -eq '1') { throw 'test-only failure between the renames' }
      Move-Item -LiteralPath $new -Destination $exe
      $done = $true
    } catch {
      $reason = $_.Exception.Message
    } finally {
      if (-not $done) {
        Remove-Item -LiteralPath $new -Force -ErrorAction SilentlyContinue
        if ($moved -and -not (Test-Path -LiteralPath $exe)) {
          try { Move-Item -LiteralPath $old -Destination $exe } catch { $restored = $false }
        }
      }
    }
    if (-not $done) {
      if (-not $restored) {
        throw "dotai install: cannot replace $exe ($reason), nor restore it; the previous dotai.exe is $old, rename it back to dotai.exe"
      }
      throw "dotai install: cannot replace $exe ($reason); the previous dotai.exe is kept. Close any running dotai, such as dotai ui, and run the installer again"
    }
    Get-ChildItem -LiteralPath $dir -Filter 'dotai.exe.old-*' | Remove-Item -Force -ErrorAction SilentlyContinue
    Write-Host "installed $exe"
  } finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }

  if (Get-Command gh -ErrorAction SilentlyContinue) {
    Write-Host "to check its build provenance, download $base/download/$tag/$archive and run:"
    Write-Host "  gh attestation verify $archive --repo Lartweib/dotai"
  }

  # Whether a Path entry names the install directory, once its variables,
  # such as %USERPROFILE%, are expanded and a trailing \ is ignored.
  function Test-InstallDir([string]$entry) {
    $entry -and ([Environment]::ExpandEnvironmentVariables($entry).TrimEnd('\') -eq $dir.TrimEnd('\'))
  }
  if (@($env:Path -split ';' | Where-Object { Test-InstallDir $_ }).Count -eq 0) {
    if ($env:DOTAI_NO_MODIFY_PATH -eq '1') {
      Write-Host "$dir is not on your Path; to add it for your user, run:"
      $quoted = $dir -replace "'", "''"
      Write-Host "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';$quoted', 'User')"
    } else {
      $subkey = 'Environment'
      # Test-only: a stand-in key, accepted only under its fixed prefix.
      if ($env:DOTAI_TEST_USER_ENV_KEY -match '^Software\\dotai-install-test\\[0-9A-Za-z-]+$') {
        $subkey = $env:DOTAI_TEST_USER_ENV_KEY
      }
      $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($subkey)
      try {
        # The raw value, with entries such as %USERPROFILE% unexpanded, and
        # its kind are kept; the directory is appended at the end.
        $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $env:Path = $env:Path.TrimEnd(';') + ";$dir"
        if (@($raw -split ';' | Where-Object { Test-InstallDir $_ }).Count -gt 0) {
          Write-Host "$dir is on your user Path already; added it to this session's Path"
        } else {
          $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
          if ($key.GetValueNames() -contains 'Path') { $kind = $key.GetValueKind('Path') }
          $value = $dir
          if ($raw.EndsWith(';')) { $value = $raw + $dir } elseif ($raw) { $value = "$raw;$dir" }
          $key.SetValue('Path', $value, $kind)
          if ($subkey -eq 'Environment') {
            # Setting a user variable through .NET tells running programs,
            # such as Explorer, that the environment changed.
            [Environment]::SetEnvironmentVariable('DOTAI_INSTALL_NOTIFY', '1', 'User')
            [Environment]::SetEnvironmentVariable('DOTAI_INSTALL_NOTIFY', $null, 'User')
            Write-Host "added $dir to your user Path; new terminals pick it up, and this session has it already"
          } else {
            Write-Host "added $dir to the Path of the test key HKCU\$subkey; the real user Path was not changed"
          }
        }
      } finally {
        $key.Close()
      }
    }
  }
  & $exe version
  if ($LASTEXITCODE -ne 0) { throw "dotai install: $exe version failed" }
}
