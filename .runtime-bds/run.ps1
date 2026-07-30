$ErrorActionPreference = 'Stop'

$root = (Resolve-Path '.').Path
$bdsDir = Join-Path $root '.runtime-bds'
$serverLog = Join-Path $bdsDir 'server-output-e2e.log'
$serverErrorLog = Join-Path $bdsDir 'server-error-e2e.log'
$botLog = Join-Path $root '.runtime-bot-e2e.log'
$botErrorLog = Join-Path $root '.runtime-bot-e2e-error.log'
$probeLog = Join-Path $root '.runtime-probe-e2e.log'
$probeErrorLog = Join-Path $root '.runtime-probe-e2e-error.log'
Remove-Item $serverLog, $serverErrorLog, $botLog, $botErrorLog, $probeLog, $probeErrorLog -Force -ErrorAction SilentlyContinue

$startInfo = New-Object System.Diagnostics.ProcessStartInfo
$startInfo.FileName = Join-Path $bdsDir 'bedrock_server.exe'
$startInfo.WorkingDirectory = $bdsDir
$startInfo.UseShellExecute = $false
$startInfo.RedirectStandardInput = $true
$startInfo.RedirectStandardOutput = $true
$startInfo.RedirectStandardError = $true
$server = New-Object System.Diagnostics.Process
$server.StartInfo = $startInfo
$null = $server.Start()
$stdout = $server.StandardOutput.ReadToEndAsync()
$stderr = $server.StandardError.ReadToEndAsync()
$luna = $null
$probe = $null

try {
    $serverReady = $false
    for ($i = 0; $i -lt 120; $i++) {
        Start-Sleep -Milliseconds 500
        if ($server.HasExited) { throw 'BDS exited during startup' }
        $listener = Get-NetUDPEndpoint -LocalPort 19140 -ErrorAction SilentlyContinue |
            Where-Object { $_.OwningProcess -eq $server.Id }
        if ($listener) {
            $serverReady = $true
            break
        }
    }
    if (-not $serverReady) { throw 'Timed out waiting for BDS port 19140' }

    $luna = Start-Process -FilePath (Join-Path $bdsDir 'luna-e2e.exe') `
        -ArgumentList '-config', 'configs/runtime-bot.yaml' `
        -WorkingDirectory $root `
        -RedirectStandardOutput $botLog `
        -RedirectStandardError $botErrorLog `
        -PassThru

    $spawned = $false
    for ($i = 0; $i -lt 60; $i++) {
        Start-Sleep -Milliseconds 500
        $luna.Refresh()
        if ($luna.HasExited) { throw 'Luna exited before spawning' }
        if ((Test-Path $botLog) -and ((Get-Content $botLog -Raw) -match 'spawned in world')) {
            $spawned = $true
            break
        }
    }
    if (-not $spawned) { throw 'Timed out waiting for Luna spawn' }

    $server.StandardInput.WriteLine('clear Luna')
    Start-Sleep -Seconds 1
    $server.StandardInput.WriteLine('give Luna minecraft:oak_log 1')
    Start-Sleep -Seconds 1

    $probe = Start-Process -FilePath (Join-Path $bdsDir 'probe-e2e.exe') `
        -WorkingDirectory $root `
        -RedirectStandardOutput $probeLog `
        -RedirectStandardError $probeErrorLog `
        -PassThru

    $crafted = $false
    for ($i = 0; $i -lt 240; $i++) {
        Start-Sleep -Milliseconds 500
        $luna.Refresh()
        $probe.Refresh()
        if ($luna.HasExited) { throw 'Luna crashed during crafting' }
        if ($probe.HasExited) { throw 'Probe disconnected before crafting completed' }
        if (-not (Test-Path $botLog)) { continue }

        $log = Get-Content $botLog -Raw
        if ($log -match 'item stack request rejected|CraftItem failed|server did not respond to item stack request|connection closed while waiting for item stack response') {
            throw 'Crafting log contains a rejection, timeout, or disconnect'
        }
        if (($log -match 'CraftItem accepted.*item=minecraft:oak_planks') -and ($log -match 'CraftItem accepted.*item=minecraft:stick')) {
            $crafted = $true
            break
        }
    }
    if (-not $crafted) { throw 'Timed out waiting for oak planks and sticks' }

    $server.StandardInput.WriteLine('clear Luna minecraft:stick 0 64')
    Start-Sleep -Seconds 2
    Write-Output 'E2E craft sequence accepted'
}
finally {
    if ($probe -and -not $probe.HasExited) { Stop-Process -Id $probe.Id -Force -ErrorAction SilentlyContinue }
    if ($luna -and -not $luna.HasExited) { Stop-Process -Id $luna.Id -Force -ErrorAction SilentlyContinue }
    if (-not $server.HasExited) {
        $server.StandardInput.WriteLine('stop')
        if (-not $server.WaitForExit(15000)) { Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue }
    }
    $stdout.Result | Set-Content $serverLog
    $stderr.Result | Set-Content $serverErrorLog
}

$serverOutput = Get-Content $serverLog -Raw
if ($serverOutput -notmatch 'Cleared the inventory of Luna,\s+removing 4 items') {
    throw 'Server did not confirm exactly 4 sticks in Luna inventory'
}
Write-Output 'E2E passed: 1 oak log -> 4 oak planks -> 4 sticks'
