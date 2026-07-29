$startInfo = New-Object System.Diagnostics.ProcessStartInfo
$startInfo.FileName = (Resolve-Path '.runtime-bds/bedrock_server.exe').Path
$startInfo.WorkingDirectory = (Resolve-Path '.runtime-bds').Path
$startInfo.UseShellExecute = $false
$startInfo.RedirectStandardInput = $true
$startInfo.RedirectStandardOutput = $true
$startInfo.RedirectStandardError = $true
$process = New-Object System.Diagnostics.Process
$process.StartInfo = $startInfo
$null = $process.Start()
$stdout = $process.StandardOutput.ReadToEndAsync()
$stderr = $process.StandardError.ReadToEndAsync()
Start-Sleep -Seconds 18
if (-not $process.HasExited) {
    $process.StandardInput.WriteLine('give Luna oak_log 1')
}
Start-Sleep -Seconds 40
if (-not $process.HasExited) {
    $process.StandardInput.WriteLine('stop')
}
$process.WaitForExit()
$stdout.Result | Set-Content '.runtime-bds/server-output.log'
$stderr.Result | Set-Content '.runtime-bds/server-error.log'
exit $process.ExitCode
