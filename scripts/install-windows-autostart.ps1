#Requires -Version 5.1
<#
.SYNOPSIS
Registers the Neru daemon as an at-logon scheduled task on Windows.

.DESCRIPTION
An alternative to the HKCU Run key that install-windows.sh writes. A scheduled
task buys three things the Run key cannot: elevation without a UAC prompt at
logon, restart on failure, and a start delay.

Elevation is the reason most people want this. User Interface Privilege
Isolation blocks both UIA reads and SendInput against windows of a higher
integrity level, so a non-elevated daemon silently does nothing in Task Manager,
regedit or an admin terminal - hints find no elements and clicks land nowhere.

Registering an elevated task itself requires an elevated session. Run this from
an admin PowerShell, or let it re-launch itself:

    Start-Process pwsh -Verb RunAs -ArgumentList '-NoProfile','-File','scripts\install-windows-autostart.ps1'

.PARAMETER Exe
Path to neru.exe. Defaults to whatever `neru` resolves to on PATH.

.PARAMETER Config
Optional config path, passed as `-c`. Omitted entirely when not given, which
leaves Neru on its default lookup.

.PARAMETER User
The account the task runs as. Defaults to the current user.

.PARAMETER TaskName
Defaults to Neru.

.PARAMETER DelaySeconds
Delay after logon before starting. Useful when another tool has to come up
first; not needed for kanata, whose CLI calls simply fail until the daemon is
listening.

.PARAMETER NoElevate
Register the task without highest privileges. Elevated windows stay unreachable.

.EXAMPLE
.\scripts\install-windows-autostart.ps1 -Config "$env:APPDATA\neru\config.toml"

.EXAMPLE
Remove it again:

    Unregister-ScheduledTask -TaskName Neru -Confirm:$false
#>
[CmdletBinding()]
param(
    [string]$Exe,
    [string]$Config,
    [string]$User = "$env:USERDOMAIN\$env:USERNAME",
    [string]$TaskName = 'Neru',
    [int]$DelaySeconds = 0,
    [switch]$NoElevate
)

$ErrorActionPreference = 'Stop'

if (-not $Exe) {
    $resolved = Get-Command neru -CommandType Application -ErrorAction SilentlyContinue
    if (-not $resolved) {
        throw 'neru.exe not found on PATH; pass -Exe with its full path.'
    }

    $Exe = $resolved.Source
}

$Exe = (Resolve-Path -LiteralPath $Exe).Path

if ($Config) {
    $Config = (Resolve-Path -LiteralPath $Config).Path
}

$runLevel = if ($NoElevate) { 'Limited' } else { 'Highest' }

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$isAdmin = (New-Object Security.Principal.WindowsPrincipal($identity)).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator
)
if ($runLevel -eq 'Highest' -and -not $isAdmin) {
    throw 'Registering an elevated task needs an elevated session. Re-run from an admin PowerShell, or pass -NoElevate.'
}

$argument = 'launch'
if ($Config) {
    $argument = "launch -c `"$Config`""
}

$action = New-ScheduledTaskAction -Execute $Exe -Argument $argument
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $User

if ($DelaySeconds -gt 0) {
    # The cmdlet has no -Delay for a logon trigger; the CIM property takes an
    # ISO 8601 duration.
    $trigger.Delay = "PT$($DelaySeconds)S"
}

$principal = New-ScheduledTaskPrincipal -UserId $User -LogonType Interactive -RunLevel $runLevel

# ExecutionTimeLimit zero means no limit: the daemon is meant to outlive the
# session, and the default three days would kill it. IgnoreNew keeps the task
# from fighting a daemon started by hand.
$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -StartWhenAvailable `
    -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -RestartCount 3 `
    -RestartInterval (New-TimeSpan -Minutes 1) `
    -MultipleInstances IgnoreNew `
    -Hidden

Register-ScheduledTask -TaskName $TaskName `
    -Action $action `
    -Trigger $trigger `
    -Principal $principal `
    -Settings $settings `
    -Description 'Neru keyboard-driven navigation daemon' `
    -Force | Out-Null

Get-ScheduledTask -TaskName $TaskName | Select-Object TaskName, State
Write-Output "action: $Exe $argument"
Write-Output "run level: $runLevel"
Write-Output "remove with: Unregister-ScheduledTask -TaskName $TaskName -Confirm:`$false"
