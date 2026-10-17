# Installs BitVoice on Windows in one command (PowerShell 5.1 or 7):
#
#   irm https://raw.githubusercontent.com/BioDream-LLC/bitvoice/main/deploy/install.ps1 | iex
#
# It downloads the latest release for this computer from GitHub, checks it against the release's
# SHA256SUMS (and BioDream's release signature when OpenSSL 3 is on this computer, as Git for Windows
# brings), and installs it:
#   As administrator: C:\Program Files\BitVoice, data in C:\ProgramData\BitVoice, a scheduled task
#     "BitVoice" that starts it when Windows starts (web interface on port 8443), and firewall rules
#     letting phones reach it on private and domain networks.
#   Otherwise: %LOCALAPPDATA%\Programs\BitVoice, data in %APPDATA%\BitVoice, started when you sign in,
#     and Windows asks once whether BitVoice may use the network.
# Run it again to upgrade. To choose, save it and run it with parameters:
#   .\install.ps1 -Version v0.1.5      that release instead of the latest
#   .\install.ps1 -Uninstall [-Purge]  remove it (with -Purge, its data too)
# For a copy kept off the internet: $env:BITVOICE_FROM = folder with the .zip and SHA256SUMS.
param(
  [string]$Version = $env:BITVOICE_VERSION,
  [switch]$Uninstall,
  [switch]$Purge,
  [string]$Https = $(if ($env:BITVOICE_HTTPS) { $env:BITVOICE_HTTPS } else { ':8443' })
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = 'BioDream-LLC/bitvoice'
# BioDream's release public key (ed25519, base64); BITVOICE_RELEASE_KEY replaces it for your own builds.
$Key = if ($env:BITVOICE_RELEASE_KEY) { $env:BITVOICE_RELEASE_KEY } else { 'M6UaGaLM1T0SekufRzAlUSeSTjfxpVL18YHE5eKkKxY=' }
$KeyName = if ($Key -eq 'M6UaGaLM1T0SekufRzAlUSeSTjfxpVL18YHE5eKkKxY=') { "BioDream's release key" } else { 'the release key in BITVOICE_RELEASE_KEY' }
$From = $env:BITVOICE_FROM
if ($Purge) { $Uninstall = $true }

function Fail([string]$m) { Write-Host "BitVoice install: $m" -ForegroundColor Red; throw $m }

$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if ($admin) {
  $Dir = Join-Path $env:ProgramFiles 'BitVoice'
  $Data = Join-Path $env:ProgramData 'BitVoice'
} else {
  $Dir = Join-Path $env:LOCALAPPDATA 'Programs\BitVoice'
  $Data = Join-Path $env:APPDATA 'BitVoice'
}
$Task = 'BitVoice'

function Stop-BitVoice {
  if (Get-ScheduledTask -TaskName $Task -ErrorAction SilentlyContinue) {
    Stop-ScheduledTask -TaskName $Task -ErrorAction SilentlyContinue
  }
  Get-Process bitvoice -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq (Join-Path $Dir 'bitvoice.exe') } | Stop-Process -Force
  Start-Sleep -Milliseconds 500
}

if ($Uninstall) {
  Stop-BitVoice
  Unregister-ScheduledTask -TaskName $Task -Confirm:$false -ErrorAction SilentlyContinue
  if ($admin) { Get-NetFirewallRule -DisplayName 'BitVoice*' -ErrorAction SilentlyContinue | Remove-NetFirewallRule }
  Remove-Item -Recurse -Force $Dir -ErrorAction SilentlyContinue
  Remove-Item -Force (Join-Path ([Environment]::GetFolderPath('Programs')) 'BitVoice.lnk') -ErrorAction SilentlyContinue
  if ($Purge) {
    Remove-Item -Recurse -Force $Data -ErrorAction SilentlyContinue
    Write-Host 'BitVoice and its data are removed.'
  } else {
    Write-Host "BitVoice is removed. Its data stays in $Data (-Purge removes it)."
  }
  return
}

$arch = switch ($env:PROCESSOR_ARCHITECTURE) { 'AMD64' { 'amd64' } 'ARM64' { 'arm64' } default { Fail "BitVoice is built for x64 and ARM64, not $env:PROCESSOR_ARCHITECTURE" } }
$work = Join-Path ([IO.Path]::GetTempPath()) ('bitvoice-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null
try {
  if (-not $Version) {
    if ($From) {
      $Version = Get-ChildItem $From -Filter "bitvoice-v*-windows-$arch.zip" | ForEach-Object { if ($_.Name -match '^bitvoice-(v[\d.]+)-windows-') { $Matches[1] } } |
        Sort-Object { [version]($_.TrimStart('v')) } | Select-Object -Last 1
    } else {
      $Version = (Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing).tag_name
    }
    if (-not $Version) { Fail 'could not find the latest release' }
  }
  if ($Version -notmatch '^v\d+\.\d+\.\d+$') { Fail "a version looks like v0.1.5, not $Version" }
  $name = "bitvoice-$Version-windows-$arch"
  Write-Host "Installing BitVoice $Version for windows/$arch."
  $zip = Join-Path $work "$name.zip"
  $sums = Join-Path $work 'SHA256SUMS'
  if ($From) {
    Copy-Item (Join-Path $From "$name.zip") $zip
    Copy-Item (Join-Path $From 'SHA256SUMS') $sums
  } else {
    $base = "https://github.com/$Repo/releases/download/$Version"
    Invoke-WebRequest "$base/$name.zip" -OutFile $zip -UseBasicParsing
    Invoke-WebRequest "$base/SHA256SUMS" -OutFile $sums -UseBasicParsing
  }
  $want = Get-Content $sums | ForEach-Object { $f = $_ -split '\s+\*?'; if ($f.Count -ge 2 -and $f[1] -eq "$name.zip") { $f[0] } } | Select-Object -First 1
  if (-not $want) { Fail "SHA256SUMS does not list $name.zip" }
  if ((Get-FileHash $zip -Algorithm SHA256).Hash -ne $want.ToUpper()) { Fail "$name.zip does not match its SHA-256 sum: the download is damaged" }
  Write-Host '  SHA-256 checked.'
  Expand-Archive $zip -DestinationPath $work -Force
  $exe = Join-Path $work "$name\bitvoice.exe"
  if (-not (Test-Path $exe)) { Fail 'the archive holds no bitvoice.exe' }

  # The signature, with OpenSSL 3 when it is here (Git for Windows brings one).
  $openssl = (Get-Command openssl -ErrorAction SilentlyContinue).Source
  if (-not $openssl) { $g = Join-Path $env:ProgramFiles 'Git\usr\bin\openssl.exe'; if (Test-Path $g) { $openssl = $g } }
  if ($openssl -and ((& $openssl version) -match '^OpenSSL ([3-9]|\d\d)\.')) {
    $sig = "$exe.sig"
    if (-not (Test-Path $sig)) { Fail 'bitvoice.exe has no signature beside it: this is not a BioDream release' }
    Set-Content -Path (Join-Path $work 'key.pem') -Encoding ascii -Value "-----BEGIN PUBLIC KEY-----`nMCowBQYDK2VwAyEA$Key`n-----END PUBLIC KEY-----"
    [IO.File]::WriteAllBytes((Join-Path $work 'sig.bin'), [Convert]::FromBase64String((Get-Content $sig -Raw).Trim()))
    & $openssl pkeyutl -verify -pubin -inkey (Join-Path $work 'key.pem') -rawin -in $exe -sigfile (Join-Path $work 'sig.bin') *> $null
    if ($LASTEXITCODE -ne 0) { Fail "the signature of bitvoice.exe does not match ${KeyName}: not installed" }
    Write-Host "  Signature checked: signed by $KeyName."
  } elseif ($env:BITVOICE_REQUIRE_SIGNATURE -eq '1') {
    Fail 'OpenSSL 3 is needed to check the release signature'
  } else {
    Write-Host '  (OpenSSL 3 is not here, so the signature is not checked; the SHA-256 sum was.)'
  }

  Stop-BitVoice
  New-Item -ItemType Directory -Force -Path $Dir, $Data | Out-Null
  Copy-Item (Join-Path $work "$name\*") $Dir -Recurse -Force
  $program = Join-Path $Dir 'bitvoice.exe'
  Write-Host "  Program: $program"

  $argline = "-data `"$Data`" -no-browser -https $Https"
  $action = New-ScheduledTaskAction -Execute $program -Argument $argline -WorkingDirectory $Dir
  $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable `
    -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
  if ($admin) {
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
    Register-ScheduledTask -TaskName $Task -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
    Get-NetFirewallRule -DisplayName 'BitVoice*' -ErrorAction SilentlyContinue | Remove-NetFirewallRule
    New-NetFirewallRule -DisplayName 'BitVoice' -Direction Inbound -Program $program -Action Allow -Profile Private, Domain | Out-Null
    Write-Host '  Starts with Windows (scheduled task "BitVoice", as SYSTEM). Firewall: phones may connect on private and domain networks.'
  } else {
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
    $principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' -WorkingDirectory $Dir `
      -Argument "-NoProfile -WindowStyle Hidden -Command `"& '$program' $($argline.Replace('"', "'"))`""
    Register-ScheduledTask -TaskName $Task -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
    $ws = New-Object -ComObject WScript.Shell
    $lnk = $ws.CreateShortcut((Join-Path ([Environment]::GetFolderPath('Programs')) 'BitVoice.lnk'))
    $lnk.TargetPath = $program; $lnk.Arguments = "-data `"$Data`" -https $Https"; $lnk.WorkingDirectory = $Dir; $lnk.Save()
    Write-Host '  Starts when you sign in (scheduled task "BitVoice"); BitVoice is also in the Start menu.'
  }
  Start-ScheduledTask -TaskName $Task

  $port = ($Https -split ':')[-1]
  $url = "https://127.0.0.1:$port"
  $ok = $false
  for ($i = 0; $i -lt 60 -and -not $ok; $i++) {
    try {
      if ($PSVersionTable.PSVersion.Major -ge 6) {
        $r = Invoke-RestMethod "$url/api/health/live" -SkipCertificateCheck
      } else {
        [Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }
        $r = Invoke-RestMethod "$url/api/health/live" -UseBasicParsing
      }
      $ok = $r.status -eq 'ok'
    } catch { Start-Sleep 1 }
  }
  if (-not $ok) { Fail "BitVoice did not start: run `"$program`" -data `"$Data`" to see why" }
  Write-Host ''
  Write-Host "BitVoice $Version is running. Open $url in a browser to set it up."
  Write-Host "  The browser warns once about BitVoice's own certificate until you give it yours (Settings, Certificate)."
} finally {
  Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
