# Installs BitVoice on Windows in one command (PowerShell 5.1 or 7):
#
#   irm https://raw.githubusercontent.com/BioDream-LLC/bitvoice/main/deploy/install.ps1 | iex
#
# It downloads the latest release for this computer from GitHub, checks it against the release's
# SHA256SUMS and BioDream's release signature (with Windows' own ssh-keygen, or OpenSSL 3 when it is
# here; releases before v0.1.14 need OpenSSL 3), and installs it:
#   As administrator: C:\Program Files\BitVoice, data in C:\ProgramData\BitVoice, a scheduled task
#     "BitVoice" that starts it when Windows starts (web interface on port 8443), and firewall rules
#     letting phones reach it on private and domain networks.
#   Otherwise: %LOCALAPPDATA%\Programs\BitVoice, data in %APPDATA%\BitVoice, started when you sign in,
#     and Windows asks once whether BitVoice may use the network.
# Run it again to upgrade. To choose, save it and run it with parameters:
#   .\install.ps1 -Version v0.1.5      that release instead of the latest
#   .\install.ps1 -Uninstall [-Purge]  remove it (with -Purge, its data too); or from Settings, Apps,
#                                      Installed apps, which asks about the data
# For a copy kept off the internet: $env:BITVOICE_FROM = folder with the .zip and SHA256SUMS.
param(
  [string]$Version = $env:BITVOICE_VERSION,
  [switch]$Uninstall,
  [switch]$Purge,
  [string]$Https = $(if ($env:BITVOICE_HTTPS) { $env:BITVOICE_HTTPS } else { ':8443' })
)
$ErrorActionPreference = 'Stop'
# Windows PowerShell started from PowerShell 7 inherits 7's module folders first, and then its own
# cmdlets (Get-FileHash, Expand-Archive) fail to load. Its own folders only, for this script.
if ($PSVersionTable.PSEdition -ne 'Core' -and $env:PSModulePath) {
  $env:PSModulePath = ($env:PSModulePath -split ';' | Where-Object { $_ -and $_ -notmatch '\\PowerShell\\(7[^\\]*\\)?Modules\\?$' -and $_ -notmatch '\\Documents\\PowerShell\\Modules' }) -join ';'
}
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

# Removing BitVoice: kept as text because the installer also writes it beside the program as
# uninstall.ps1, which Windows' Installed apps runs (a script run by "irm | iex" has no file of its own).
# __DIR__, __DATA__, __TASK__ and __ADMIN__ are filled in.
$UninstallScript = @'
param([switch]$Purge, [switch]$Quiet)
$ErrorActionPreference = 'Stop'
$Dir = '__DIR__'; $Data = '__DATA__'; $Task = '__TASK__'; $ForAll = [bool]__ADMIN__
$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if ($ForAll -and -not $admin) {
  # Installed for everyone: removing it takes an administrator. (Installed apps starts this unelevated.)
  $a = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$PSCommandPath`"") + $(if ($Purge) { '-Purge' }) + $(if ($Quiet) { '-Quiet' })
  Start-Process powershell.exe -Verb RunAs -ArgumentList $a -Wait
  return
}
if (-not $Quiet -and -not $Purge -and $PSCommandPath -and (Test-Path $Data)) {
  # From Installed apps: ask, since nobody gave -Purge.
  $Purge = (Read-Host "Remove BitVoice's data too: settings, call history, voicemail and recordings in $Data? [y/N]") -match '^(y|yes)$'
}
if (Get-ScheduledTask -TaskName $Task -ErrorAction SilentlyContinue) {
  Stop-ScheduledTask -TaskName $Task -ErrorAction SilentlyContinue
  Unregister-ScheduledTask -TaskName $Task -Confirm:$false
}
foreach ($n in 'bitvoice', 'whisper-server') {
  Get-Process $n -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq (Join-Path $Dir "$n.exe") } | Stop-Process -Force
}
Start-Sleep -Milliseconds 500
if ($ForAll) { Get-NetFirewallRule -DisplayName 'BitVoice*' -ErrorAction SilentlyContinue | Remove-NetFirewallRule }
# The rules Windows made when someone answered its firewall question for this program.
$asked = @(Get-NetFirewallApplicationFilter -Program (Join-Path $Dir 'bitvoice.exe') -ErrorAction SilentlyContinue | Get-NetFirewallRule -ErrorAction SilentlyContinue)
if ($asked -and $admin) { $asked | Remove-NetFirewallRule }
elseif ($asked) { Write-Host 'Windows keeps the network permission it asked you about; an administrator can remove it in Windows Security, Firewall, Allow an app (bitvoice.exe).' }
$programs = [Environment]::GetFolderPath($(if ($ForAll) { 'CommonPrograms' } else { 'Programs' }))
Remove-Item -Force (Join-Path $programs 'BitVoice.lnk') -ErrorAction SilentlyContinue
$hive = if ($ForAll) { 'HKLM:' } else { 'HKCU:' }
Remove-Item -Force -Recurse "$hive\Software\Microsoft\Windows\CurrentVersion\Uninstall\BitVoice" -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force $Dir -ErrorAction SilentlyContinue
if ($Purge) {
  Remove-Item -Recurse -Force $Data -ErrorAction SilentlyContinue
  Write-Host 'BitVoice and its data are removed.'
} else {
  Write-Host "BitVoice is removed. Its data stays in $Data."
}
if ($PSCommandPath -and -not $Quiet) { Start-Sleep 3 } # in its own window: long enough to read
'@
$UninstallScript = $UninstallScript.Replace('__DIR__', $Dir.Replace("'", "''")).Replace('__DATA__', $Data.Replace("'", "''")).
  Replace('__TASK__', $Task).Replace('__ADMIN__', $(if ($admin) { '1' } else { '0' }))

function Stop-BitVoice {
  if (Get-ScheduledTask -TaskName $Task -ErrorAction SilentlyContinue) {
    Stop-ScheduledTask -TaskName $Task -ErrorAction SilentlyContinue
  }
  Get-Process bitvoice -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq (Join-Path $Dir 'bitvoice.exe') } | Stop-Process -Force
  # whisper-server, which BitVoice starts for speech to text, holds its program open while it runs.
  Get-Process whisper-server -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq (Join-Path $Dir 'whisper-server.exe') } | Stop-Process -Force
  Start-Sleep -Milliseconds 500
}

if ($Uninstall) {
  & ([scriptblock]::Create($UninstallScript)) -Purge:$Purge -Quiet
  if (-not $Purge) { Write-Host '(-Purge removes it too.)' }
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

  # The signature: with OpenSSL 3 when it is here (newer Git for Windows brings one), or else with
  # Windows' own ssh-keygen (Windows 10 and 11) and the same key's OpenSSH signature (.sshsig, from
  # v0.1.14 on). bitvoice.exe, and whisper-server.exe (speech to text) when the download has it.
  $signed = @($exe, (Join-Path $work "$name\whisper-server.exe")) | Where-Object { Test-Path $_ }
  $openssl = (Get-Command openssl -ErrorAction SilentlyContinue).Source
  # Git for Windows has two; either may be OpenSSL 3, depending on its version.
  $three = { param($o) $o -and ((& $o version 2>$null) -match '^OpenSSL ([3-9]|\d\d)\.') }
  if (-not (& $three $openssl)) {
    $openssl = 'mingw64\bin', 'usr\bin' | ForEach-Object { Join-Path $env:ProgramFiles "Git\$_\openssl.exe" } |
      Where-Object { (Test-Path $_) -and (& $three $_) } | Select-Object -First 1
  }
  $keygen = Join-Path $env:SystemRoot 'System32\OpenSSH\ssh-keygen.exe'
  $sshsigs = @($signed | Where-Object { Test-Path "$_.sshsig" }).Count -eq $signed.Count
  if ($openssl) {
    Set-Content -Path (Join-Path $work 'key.pem') -Encoding ascii -Value "-----BEGIN PUBLIC KEY-----`nMCowBQYDK2VwAyEA$Key`n-----END PUBLIC KEY-----"
    foreach ($p in $signed) {
      $pn = Split-Path $p -Leaf
      $sig = "$p.sig"
      if (-not (Test-Path $sig)) { Fail "$pn has no signature beside it: this is not a BioDream release" }
      [IO.File]::WriteAllBytes((Join-Path $work 'sig.bin'), [Convert]::FromBase64String((Get-Content $sig -Raw).Trim()))
      & $openssl pkeyutl -verify -pubin -inkey (Join-Path $work 'key.pem') -rawin -in $p -sigfile (Join-Path $work 'sig.bin') *> $null
      if ($LASTEXITCODE -ne 0) { Fail "the signature of $pn does not match ${KeyName}: not installed" }
    }
    Write-Host "  Signature checked: signed by $KeyName."
  } elseif ((Test-Path $keygen) -and $sshsigs) {
    # The key as OpenSSH writes it: string "ssh-ed25519", string key.
    $raw = [Convert]::FromBase64String($Key)
    $blob = [byte[]](0, 0, 0, 11) + [Text.Encoding]::ASCII.GetBytes('ssh-ed25519') + [byte[]](0, 0, 0, $raw.Length) + $raw
    $signers = Join-Path $work 'signers'
    Set-Content -Path $signers -Encoding ascii -Value "biodream ssh-ed25519 $([Convert]::ToBase64String($blob))"
    foreach ($p in $signed) {
      $pn = Split-Path $p -Leaf
      # The program goes in as ssh-keygen's input, byte for byte (a PowerShell pipe would turn it into text).
      $v = Start-Process $keygen -ArgumentList '-Y', 'verify', '-f', "`"$signers`"", '-I', 'biodream', '-n', 'file', '-s', "`"$p.sshsig`"" `
        -RedirectStandardInput $p -RedirectStandardOutput (Join-Path $work 'v.out') -RedirectStandardError (Join-Path $work 'v.err') `
        -NoNewWindow -Wait -PassThru
      if ($v.ExitCode -ne 0) { Fail "the signature of $pn does not match ${KeyName}: not installed" }
    }
    Write-Host "  Signature checked (with Windows' ssh-keygen): signed by $KeyName."
  } elseif ((Test-Path $keygen) -and [version]$Version.TrimStart('v') -ge [version]'0.1.14') {
    # Every release from v0.1.14 on has them: one missing was taken away.
    Fail "the download has no OpenSSH signature (.sshsig) beside each program: this is not a BioDream release"
  } elseif ($env:BITVOICE_REQUIRE_SIGNATURE -eq '1') {
    Fail 'the release signature cannot be checked here (it needs OpenSSL 3, or a release from v0.1.14 on)'
  } else {
    Write-Host '  (This release can be checked only with OpenSSL 3, which is not here: the signature is not checked; the SHA-256 sum was.)'
  }

  # A web port another program holds: Windows may let BitVoice open it too, and the browser then reaches
  # whichever answers. Say so before changing anything. (An older BitVoice of this install is fine.)
  $webPort = [int](($Https -split ':')[-1])
  $holders = Get-NetTCPConnection -LocalPort $webPort -State Listen -ErrorAction SilentlyContinue |
    ForEach-Object { Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue } |
    Where-Object { $_.Path -ne (Join-Path $Dir 'bitvoice.exe') } | Sort-Object Id -Unique
  if ($holders) {
    Fail ("port $webPort is in use by " + (($holders | ForEach-Object { "$($_.ProcessName) (process $($_.Id))" }) -join ', ') +
      ". Stop it, or choose another port: .\install.ps1 -Https :8444")
  }

  Stop-BitVoice
  New-Item -ItemType Directory -Force -Path $Dir, $Data | Out-Null
  Copy-Item (Join-Path $work "$name\*") $Dir -Recurse -Force
  $program = Join-Path $Dir 'bitvoice.exe'
  Write-Host "  Program: $program"

  # The log in the data folder: a scheduled task has no window to show it in. Releases before v0.1.14
  # have no -log and would stop at it; their output goes nowhere, as it always did.
  $log = Join-Path $Data 'bitvoice.log'
  # (Its help goes to stderr, which Windows PowerShell 5.1 turns into errors that Stop would end the script at.)
  $hasLog = (& { $ErrorActionPreference = 'Continue'; & $program -h 2>&1 | Out-String }) -match '(?m)^\s+-log\b'
  $argline = "-data `"$Data`"" + $(if ($hasLog) { " -log `"$log`"" } else { '' }) + " -no-browser -https $Https"
  $action = New-ScheduledTaskAction -Execute $program -Argument $argline -WorkingDirectory $Dir
  $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable `
    -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
  if ($admin) {
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
    Register-ScheduledTask -TaskName $Task -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
    Get-NetFirewallRule -DisplayName 'BitVoice*' -ErrorAction SilentlyContinue | Remove-NetFirewallRule
    New-NetFirewallRule -DisplayName 'BitVoice' -Direction Inbound -Program $program -Action Allow -Profile Private, Domain | Out-Null
    # ProgramData lets every user of this computer read what is in it: BitVoice's database and keys
    # are for SYSTEM and administrators only.
    & icacls.exe $Data /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' /T /Q | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail "could not limit $Data to administrators" }
    # For everyone: the Start menu opens the web interface (BitVoice itself runs as SYSTEM).
    $lnk = (New-Object -ComObject WScript.Shell).CreateShortcut((Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'BitVoice.lnk'))
    $lnk.TargetPath = Join-Path $env:SystemRoot 'explorer.exe'; $lnk.Arguments = "https://127.0.0.1:$(($Https -split ':')[-1])"
    $lnk.IconLocation = "$program,0"; $lnk.Description = 'Open the BitVoice web interface'; $lnk.Save()
    Write-Host '  Starts with Windows (scheduled task "BitVoice", as SYSTEM); its data is for administrators only. Firewall: phones may connect on private and domain networks.'
    # Windows calls a new Wi-Fi or network Public until someone says otherwise, and the rule above lets
    # nothing in there. A home or office network (a private address) should be Private; a cloud server
    # (a public address) needs its ports opened. Either way it is for the owner to decide, not this script.
    foreach ($net in @(Get-NetConnectionProfile -ErrorAction SilentlyContinue | Where-Object NetworkCategory -eq 'Public')) {
      $ip = (Get-NetIPAddress -InterfaceIndex $net.InterfaceIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue | Select-Object -First 1).IPAddress
      $lan = $ip -match '^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.)'
      Write-Host ''
      if ($lan) {
        Write-Host "  Phones on `"$($net.Name)`" ($($net.InterfaceAlias), $ip) cannot reach BitVoice yet: Windows calls that" -ForegroundColor Yellow
        Write-Host '  network Public. If it is your home or office network, make it Private: Settings, Network & internet,' -ForegroundColor Yellow
        Write-Host "  $($net.InterfaceAlias), Properties, Private network. Or, as administrator:" -ForegroundColor Yellow
        Write-Host "    Set-NetConnectionProfile -InterfaceIndex $($net.InterfaceIndex) -NetworkCategory Private"
      } else {
        Write-Host "  $($net.InterfaceAlias) ($ip) is a network Windows calls Public, where the rule lets nothing in. On a" -ForegroundColor Yellow
        Write-Host '  cloud server, allow BitVoice there yourself (and in the provider''s firewall):' -ForegroundColor Yellow
        Write-Host "    New-NetFirewallRule -DisplayName 'BitVoice (public)' -Direction Inbound -Program '$program' -Action Allow -Profile Public"
      }
    }
  } else {
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
    $principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' -WorkingDirectory $Dir `
      -Argument "-NoProfile -WindowStyle Hidden -Command `"& '$program' $($argline.Replace('"', "'"))`""
    Register-ScheduledTask -TaskName $Task -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
    $ws = New-Object -ComObject WScript.Shell
    $lnk = $ws.CreateShortcut((Join-Path ([Environment]::GetFolderPath('Programs')) 'BitVoice.lnk'))
    # It starts BitVoice when it is not running, and opens the one that is (v0.1.14 on) when it is.
    $lnk.TargetPath = $program; $lnk.Arguments = "-data `"$Data`" -https $Https"; $lnk.WorkingDirectory = $Dir
    $lnk.Description = 'Open BitVoice'; $lnk.Save()
    Write-Host '  Starts when you sign in (scheduled task "BitVoice"); BitVoice is also in the Start menu.'
  }
  Set-Content -Path (Join-Path $Dir 'uninstall.ps1') -Value $UninstallScript -Encoding UTF8
  $key = "$(if ($admin) { 'HKLM:' } else { 'HKCU:' })\Software\Microsoft\Windows\CurrentVersion\Uninstall\BitVoice"
  New-Item -Path $key -Force | Out-Null
  $size = [int]((Get-ChildItem $Dir -Recurse -File | Measure-Object Length -Sum).Sum / 1KB)
  @{ DisplayName = 'BitVoice'; DisplayVersion = $Version.TrimStart('v'); Publisher = 'BioDream LLC'; DisplayIcon = $program
    InstallLocation = $Dir; URLInfoAbout = 'https://bitvoice.org'; HelpLink = 'https://bitvoice.org/manual/'
    UninstallString = "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$(Join-Path $Dir 'uninstall.ps1')`""
    QuietUninstallString = "powershell.exe -NoProfile -ExecutionPolicy Bypass -File `"$(Join-Path $Dir 'uninstall.ps1')`" -Quiet"
  }.GetEnumerator() | ForEach-Object { New-ItemProperty -Path $key -Name $_.Key -Value $_.Value -PropertyType String -Force | Out-Null }
  foreach ($d in 'NoModify', 'NoRepair', 'EstimatedSize') {
    New-ItemProperty -Path $key -Name $d -Value $(if ($d -eq 'EstimatedSize') { $size } else { 1 }) -PropertyType DWord -Force | Out-Null
  }

  # Only what this start writes counts (an older start's setup code is no use).
  $seen = if (Test-Path $log) { @(Get-Content $log).Count } else { 0 }
  Start-ScheduledTask -TaskName $Task

  $port = ($Https -split ':')[-1]
  $url = "https://127.0.0.1:$port"
  $ok = $false
  for ($i = 0; $i -lt 60 -and -not $ok; $i++) {
    # A program that stopped will not answer: say why now rather than after a minute.
    if ($i -ge 2 -and (Get-ScheduledTask -TaskName $Task).State -ne 'Running') { break }
    try {
      if ($PSVersionTable.PSVersion.Major -ge 6) {
        $r = Invoke-RestMethod "$url/api/health/live" -SkipCertificateCheck -TimeoutSec 3
      } else {
        # Windows PowerShell 5.1: a script block as the certificate callback fails ("no Runspace")
        # when .NET calls it on another thread, as it does on Windows Server 2022; a compiled one
        # does not. It is used for this one request to this computer only.
        if (-not ('BitVoiceLocalCheck' -as [type])) {
          Add-Type -TypeDefinition 'public static class BitVoiceLocalCheck { static bool Accept(object s, System.Security.Cryptography.X509Certificates.X509Certificate c, System.Security.Cryptography.X509Certificates.X509Chain ch, System.Net.Security.SslPolicyErrors e) { return true; } public static readonly System.Net.Security.RemoteCertificateValidationCallback Callback = Accept; }'
        }
        $was = [Net.ServicePointManager]::ServerCertificateValidationCallback
        $protocols = [Net.ServicePointManager]::SecurityProtocol
        try {
          [Net.ServicePointManager]::ServerCertificateValidationCallback = [BitVoiceLocalCheck]::Callback
          if ($protocols -ne [Net.SecurityProtocolType]::SystemDefault) {
            [Net.ServicePointManager]::SecurityProtocol = $protocols -bor [Net.SecurityProtocolType]::Tls12 # older Windows offers TLS 1.0 alone
          }
          $r = Invoke-RestMethod "$url/api/health/live" -UseBasicParsing -TimeoutSec 3
        } finally {
          [Net.ServicePointManager]::ServerCertificateValidationCallback = $was
          [Net.ServicePointManager]::SecurityProtocol = $protocols
        }
      }
      $ok = $r.status -eq 'ok'
    } catch { Start-Sleep 1 }
  }
  if (-not $ok) {
    # Its own words: the log's new lines, or else what it prints when started here for a few seconds.
    $why = if (Test-Path $log) { @(Get-Content $log | Select-Object -Skip $seen) } else { @() }
    if (-not $why) {
      Stop-BitVoice
      $out = Join-Path $work 'start.txt'
      $p = Start-Process $program -ArgumentList "-data `"$Data`" -no-browser -https $Https" -WorkingDirectory $Dir `
        -RedirectStandardError $out -RedirectStandardOutput "$out.1" -WindowStyle Hidden -PassThru
      if (-not $p.WaitForExit(8000)) { $p.Kill() }
      $why = @(Get-Content $out, "$out.1" -ErrorAction SilentlyContinue)
    }
    $why | Select-Object -Last 15 | ForEach-Object { Write-Host "    $_" }
    Fail "BitVoice did not start (above, what it said). Its data: $Data"
  }
  Write-Host ''
  Write-Host "BitVoice $Version is running. Open $url in a browser to set it up."
  Write-Host "  The browser warns once about BitVoice's own certificate until you give it yours (Settings, Certificate)."
  if ($hasLog) { Write-Host "  Its log: $log" }
  # From another computer the setup wizard asks for the code the program printed when it started.
  $code = Get-Content $log -ErrorAction SilentlyContinue | Select-Object -Skip $seen | Select-String 'Setup code for another computer: (\d+)' | Select-Object -Last 1
  if ($code) { Write-Host "  Setup code (the wizard asks for it from another computer): $($code.Matches[0].Groups[1].Value)" }
} finally {
  Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
