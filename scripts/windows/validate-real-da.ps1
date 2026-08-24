param(
    [Parameter(Mandatory = $true)]
    [string]$AdapterPath,

    [Parameter(Mandatory = $true)]
    [string]$ServerDirectory,

    [Parameter(Mandatory = $true)]
    [ValidateSet('386', 'amd64')]
    [string]$AdapterArch,

    [ValidateRange(1, 100000)]
    [int]$SoakIterations = 200,

    [string]$StabilityProbePath,

    [ValidateRange(0, 10)]
    [int]$FailureCycles = 0,

    [switch]$Destructive,

    [string]$DestructiveConfirmation,

    [ValidateRange(0, 20)]
    [int]$AdapterCrashCycles = 0,

    [ValidatePattern('^[A-Za-z0-9._]{1,32}$')]
    [string]$RunLabel = 'default'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Add-Type -AssemblyName System.Net.Http

if ($Destructive.IsPresent -and $DestructiveConfirmation -cne 'DISPOSABLE_VM_ONLY') {
    throw '-Destructive requires -DestructiveConfirmation DISPOSABLE_VM_ONLY'
}
if (-not $Destructive.IsPresent -and -not [string]::IsNullOrEmpty($DestructiveConfirmation)) {
    throw '-DestructiveConfirmation is valid only with -Destructive'
}

function Assert-True {
    param(
        [Parameter(Mandatory = $true)]
        [bool]$Condition,

        [Parameter(Mandatory = $true)]
        [string]$Message
    )

    if (-not $Condition) {
        throw $Message
    }
}

function Invoke-NativeProcess {
    param(
        [Parameter(Mandatory = $true)]
        [string]$FilePath,

        [string[]]$ArgumentList = @(),

        [ValidateRange(1, 1800)]
        [int]$TimeoutSeconds = 30
    )

    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $FilePath
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    # ProcessStartInfo.ArgumentList is unavailable in Windows PowerShell 5.1.
    # Quote with the CommandLineToArgvW/MSVC rules so this validation script
    # runs on a stock Windows Server VM as well as PowerShell 7 runners.
    $quotedArguments = @($ArgumentList | ForEach-Object {
        $argument = [string]$_
        if ($argument.Length -eq 0) {
            '""'
        }
        elseif ($argument -notmatch '[\s"]') {
            $argument
        }
        else {
            $escaped = [regex]::Replace($argument, '(\\*)"', '$1$1\"')
            $escaped = [regex]::Replace($escaped, '(\\+)$', '$1$1')
            '"' + $escaped + '"'
        }
    })
    $startInfo.Arguments = $quotedArguments -join ' '
    $process = [Diagnostics.Process]::Start($startInfo)
    if ($null -eq $process) {
        throw "could not start $FilePath"
    }
    $standardOutput = $process.StandardOutput.ReadToEndAsync()
    $standardError = $process.StandardError.ReadToEndAsync()
    if (-not $process.WaitForExit($TimeoutSeconds * 1000)) {
        try {
            $process.Kill($true)
        }
        catch {
            Write-Warning "could not terminate timed-out native process $($process.Id)"
        }
        throw "$FilePath exceeded its $TimeoutSeconds second validation timeout"
    }
    $outputText = $standardOutput.GetAwaiter().GetResult()
    $errorText = $standardError.GetAwaiter().GetResult()
    if (-not [string]::IsNullOrWhiteSpace($outputText)) {
        Write-Host $outputText.TrimEnd()
    }
    if (-not [string]::IsNullOrWhiteSpace($errorText)) {
        Write-Host $errorText.TrimEnd()
    }
    if ($process.ExitCode -ne 0) {
        throw "$FilePath failed with exit code $($process.ExitCode)"
    }
}

function ConvertTo-RequestJSON {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Value
    )

    return $Value | ConvertTo-Json -Depth 12 -Compress
}

function Send-AdapterRequest {
    param(
        [Parameter(Mandatory = $true)]
        [ValidateSet('GET', 'POST')]
        [string]$Method,

        [Parameter(Mandatory = $true)]
        [string]$Path,

        [string]$Body
    )

    $methodValue = if ($Method -eq 'GET') { [Net.Http.HttpMethod]::Get } else { [Net.Http.HttpMethod]::Post }
    $request = [Net.Http.HttpRequestMessage]::new($methodValue, "$script:BaseURL$Path")
    if ($PSBoundParameters.ContainsKey('Body')) {
        $request.Content = [Net.Http.StringContent]::new($Body, [Text.Encoding]::UTF8, 'application/json')
    }
    try {
        $response = $script:HTTPClient.SendAsync($request).GetAwaiter().GetResult()
        try {
            $text = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
            $json = if ([string]::IsNullOrWhiteSpace($text)) { $null } else { $text | ConvertFrom-Json }
            return [pscustomobject]@{
                Status = [int]$response.StatusCode
                JSON = $json
            }
        }
        finally {
            $response.Dispose()
        }
    }
    finally {
        $request.Dispose()
    }
}

function Require-Status {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Response,

        [Parameter(Mandatory = $true)]
        [int]$Expected,

        [Parameter(Mandatory = $true)]
        [string]$Operation
    )

    if ($Response.Status -ne $Expected) {
        throw "$Operation returned HTTP $($Response.Status), expected $Expected"
    }
    return $Response.JSON
}

function Start-Adapter {
    param(
        [Parameter(Mandatory = $true)]
        [bool]$WriteEnabled,

        [Parameter(Mandatory = $true)]
        [string]$Label
    )

    $env:OPCDA_SOURCE_PROG_ID = $script:ProgID
    Remove-Item Env:OPCDA_SOURCE_CLSID -ErrorAction SilentlyContinue
    $env:OPCDA_HTTP_LISTEN = '127.0.0.1:18080'
    $env:OPCDA_WRITE_ENABLED = $WriteEnabled.ToString().ToLowerInvariant()
    $env:OPCDA_MAX_HTTP_CONNECTIONS = '64'
    $env:OPCDA_MAX_CONCURRENT_REQUESTS = '32'
    $env:OPCDA_MAX_HTTP_HEADER_BYTES = '32768'
    $env:OPCDA_HTTP_READ_HEADER_TIMEOUT = '5s'
    $env:OPCDA_HTTP_READ_TIMEOUT = '15s'
    $env:OPCDA_HTTP_WRITE_TIMEOUT = '15s'
    $env:OPCDA_HTTP_IDLE_TIMEOUT = '30s'
    $env:OPCDA_RECONNECT_INITIAL = '200ms'
    $env:OPCDA_RECONNECT_MAX = '2s'
    $env:OPCDA_REQUEST_DEADLINE = '10s'
    $env:OPCDA_COM_CALL_WATCHDOG = '15s'

    $stdout = Join-Path $script:WorkingDirectory "adapter-$Label.stdout.log"
    $stderr = Join-Path $script:WorkingDirectory "adapter-$Label.stderr.log"
    return Start-Process -FilePath $script:AdapterExecutable -PassThru `
        -RedirectStandardOutput $stdout -RedirectStandardError $stderr
}

function Stop-Adapter {
    param([Diagnostics.Process]$Process)

    if ($null -eq $Process) {
        return
    }
    try {
        $Process.Refresh()
        if (-not $Process.HasExited) {
            Stop-Process -Id $Process.Id -Force
            $Process.WaitForExit(5000) | Out-Null
        }
    }
    catch {
        Write-Warning "could not stop adapter process $($Process.Id): $($_.Exception.Message)"
    }
}

function Wait-Connected {
    param(
        [uint64]$MinimumGeneration = 1,
        [int]$TimeoutSeconds = 60
    )

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        try {
            $response = Send-AdapterRequest -Method GET -Path '/v1/status'
            if ($response.Status -eq 200 -and
                $response.JSON.state -eq 'connected' -and
                [uint64]$response.JSON.source.connectionGeneration -ge $MinimumGeneration) {
                return $response.JSON
            }
        }
        catch {
            # The listener or source may still be starting. No response body is logged.
        }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)

    throw "adapter did not connect with generation >= $MinimumGeneration within $TimeoutSeconds seconds"
}

function Wait-SourceFailure {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$ExpectedHRESULTs,

        [int]$TimeoutSeconds = 30
    )

    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        try {
            $response = Send-AdapterRequest -Method GET -Path '/v1/status'
            if ($response.Status -eq 200 -and
                $response.JSON.state -in @('connecting', 'disconnected', 'reconnecting') -and
                $null -ne $response.JSON.source.lastError -and
                $null -ne $response.JSON.source.lastError.hresult) {
                $observed = [string]$response.JSON.source.lastError.hresult.hex
                if ($observed -in $ExpectedHRESULTs) {
                    return $response.JSON
                }
                throw "unexpected source HRESULT $observed while waiting for $($ExpectedHRESULTs -join ', ')"
            }
        }
        catch {
            if ($_.Exception.Message.StartsWith('unexpected source HRESULT')) {
                throw
            }
            # The listener may still be starting. No response body is logged.
        }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)

    throw "adapter did not expose source HRESULT $($ExpectedHRESULTs -join ', ') within $TimeoutSeconds seconds"
}

function Get-RegistryValueSnapshot {
    param(
        [Parameter(Mandatory = $true)]
        [Microsoft.Win32.RegistryKey]$Key,

        [Parameter(Mandatory = $true)]
        [string]$Name
    )

    if ($Name -notin $Key.GetValueNames()) {
        return [pscustomobject]@{ Exists = $false; Value = $null; Kind = $null }
    }
    return [pscustomobject]@{
        Exists = $true
        Value = $Key.GetValue($Name, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        Kind = $Key.GetValueKind($Name)
    }
}

function Restore-RegistryValue {
    param(
        [Parameter(Mandatory = $true)]
        [Microsoft.Win32.RegistryKey]$Key,

        [Parameter(Mandatory = $true)]
        [string]$Name,

        [Parameter(Mandatory = $true)]
        [object]$Snapshot
    )

    if ($Snapshot.Exists) {
        $Key.SetValue($Name, $Snapshot.Value, $Snapshot.Kind)
    }
    else {
        $Key.DeleteValue($Name, $false)
    }
    $Key.Flush()
}

function Convert-SDDLToBinary {
    param(
        [Parameter(Mandatory = $true)]
        [string]$SDDL
    )

    $descriptor = [Security.AccessControl.RawSecurityDescriptor]::new($SDDL)
    $bytes = [byte[]]::new($descriptor.BinaryLength)
    $descriptor.GetBinaryForm($bytes, 0)
    return $bytes
}

function Restore-COMPermissionSnapshots {
    param(
        [Parameter(Mandatory = $true)]
        [Microsoft.Win32.RegistryKey]$Key,

        [Parameter(Mandatory = $true)]
        [object]$LaunchSnapshot,

        [Parameter(Mandatory = $true)]
        [object]$AccessSnapshot,

        [Parameter(Mandatory = $true)]
        [object]$RunAsSnapshot
    )

    $restoreErrors = [Collections.Generic.List[string]]::new()
    foreach ($entry in @(
        [pscustomobject]@{ Name = 'LaunchPermission'; Snapshot = $LaunchSnapshot },
        [pscustomobject]@{ Name = 'AccessPermission'; Snapshot = $AccessSnapshot },
        [pscustomobject]@{ Name = 'RunAs'; Snapshot = $RunAsSnapshot }
    )) {
        try {
            Restore-RegistryValue -Key $Key -Name $entry.Name -Snapshot $entry.Snapshot
        }
        catch {
            [void]$restoreErrors.Add("$($entry.Name): $($_.Exception.Message)")
        }
    }
    if ($restoreErrors.Count -ne 0) {
        throw "one or more COM permission values could not be restored: $($restoreErrors -join '; ')"
    }
}

function Invoke-DestructiveCOMPermissionValidation {
    param(
        [Parameter(Mandatory = $true)]
        [Microsoft.Win32.RegistryView]$RegistryView,

        [Parameter(Mandatory = $true)]
        [string]$AppID
    )

    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $currentSID = $identity.User.Value
    $machineClasses = [Microsoft.Win32.RegistryKey]::OpenBaseKey(
        [Microsoft.Win32.RegistryHive]::LocalMachine,
        $RegistryView
    )
    $appKey = $null
    try {
        $appKey = $machineClasses.OpenSubKey("SOFTWARE\Classes\AppID\$AppID", $true)
        Assert-True ($null -ne $appKey) "AppID $AppID is missing from the expected registry view"

        $launchSnapshot = Get-RegistryValueSnapshot -Key $appKey -Name 'LaunchPermission'
        $accessSnapshot = Get-RegistryValueSnapshot -Key $appKey -Name 'AccessPermission'
        $runAsSnapshot = Get-RegistryValueSnapshot -Key $appKey -Name 'RunAs'
        $permissionAdapter = $null
        try {
            # COM_RIGHTS_EXECUTE | EXECUTE_LOCAL | ACTIVATE_LOCAL. The explicit
            # user deny wins over group membership; no remote right is present.
            $launchDeny = Convert-SDDLToBinary "O:BAG:BAD:(D;;0x0000000B;;;$currentSID)(A;;0x0000000B;;;SY)(A;;0x0000000B;;;BA)"
            $appKey.SetValue('LaunchPermission', $launchDeny, [Microsoft.Win32.RegistryValueKind]::Binary)
            $appKey.Flush()
            Stop-ServerProcesses
            $permissionAdapter = Start-Adapter -WriteEnabled $false -Label 'launch-denied'
            $denied = Wait-SourceFailure -ExpectedHRESULTs @('0x80070005')
            Assert-True ($denied.source.lastError.operation -eq 'CoCreateInstance(IOPCServer)') `
                'Launch denial did not identify CoCreateInstance'
            Restore-RegistryValue -Key $appKey -Name 'LaunchPermission' -Snapshot $launchSnapshot
            $recovered = Wait-Connected -TimeoutSeconds 60
            Assert-True ($null -eq $recovered.source.lastError) `
                'successful reconnect did not clear the source diagnostic'
            Write-Host 'DESTRUCTIVE_COM_LAUNCH_DENIAL_PASS hresult=0x80070005 recovered=true remoteRights=false'
            Stop-Adapter $permissionAdapter
            $permissionAdapter = $null

            # The pinned fixture initializes its own process security. Record
            # whether an AppID AccessPermission deny is enforced or overridden;
            # either way the registry value must be restored exactly.
            $accessDeny = Convert-SDDLToBinary "O:BAG:BAD:(D;;0x00000003;;;$currentSID)(A;;0x00000003;;;SY)(A;;0x00000003;;;BA)"
            $appKey.SetValue('AccessPermission', $accessDeny, [Microsoft.Win32.RegistryValueKind]::Binary)
            $appKey.Flush()
            Stop-ServerProcesses
            $permissionAdapter = Start-Adapter -WriteEnabled $false -Label 'access-denied'
            $accessOutcome = 'server-process-security-overrode-appid'
            try {
                [void](Wait-Connected -TimeoutSeconds 15)
            }
            catch {
                $accessDenied = Wait-SourceFailure -ExpectedHRESULTs @('0x80070005')
                Assert-True ($accessDenied.source.lastError.operation -eq 'CoCreateInstance(IOPCServer)') `
                    'Access denial did not identify CoCreateInstance'
                $accessOutcome = 'appid-access-denied'
            }
            Restore-RegistryValue -Key $appKey -Name 'AccessPermission' -Snapshot $accessSnapshot
            if ($accessOutcome -eq 'appid-access-denied') {
                [void](Wait-Connected -TimeoutSeconds 60)
            }
            Write-Host "DESTRUCTIVE_COM_ACCESS_PERMISSION_OBSERVED outcome=$accessOutcome restored=true remoteRights=false"
            Stop-Adapter $permissionAdapter
            $permissionAdapter = $null

            # A deliberately nonexistent RunAs identity must fail closed. Do
            # not configure or log a password for this negative test.
            $appKey.SetValue('RunAs', '.\opcda-review-missing-account', [Microsoft.Win32.RegistryValueKind]::String)
            $appKey.Flush()
            Stop-ServerProcesses
            $permissionAdapter = Start-Adapter -WriteEnabled $false -Label 'runas-invalid'
            $runAsDenied = Wait-SourceFailure -ExpectedHRESULTs @('0x8000401A', '0x8007052E', '0x80080005')
            Assert-True ($runAsDenied.source.lastError.operation -eq 'CoCreateInstance(IOPCServer)') `
                'RunAs failure did not identify CoCreateInstance'
            $runAsHRESULT = [string]$runAsDenied.source.lastError.hresult.hex
            Restore-RegistryValue -Key $appKey -Name 'RunAs' -Snapshot $runAsSnapshot
            [void](Wait-Connected -TimeoutSeconds 60)
            Write-Host "DESTRUCTIVE_COM_RUNAS_FAILURE_PASS hresult=$runAsHRESULT recovered=true credentialStored=false"
        }
        finally {
            Stop-Adapter $permissionAdapter
            try {
                Restore-COMPermissionSnapshots -Key $appKey `
                    -LaunchSnapshot $launchSnapshot `
                    -AccessSnapshot $accessSnapshot `
                    -RunAsSnapshot $runAsSnapshot
            }
            finally {
                Stop-ServerProcesses
            }
        }
    }
    finally {
        if ($null -ne $appKey) {
            $appKey.Dispose()
        }
        $machineClasses.Dispose()
        $identity.Dispose()
    }
}

function Get-ServerProcesses {
    return @(Get-Process -Name $script:ServerProcessName -ErrorAction SilentlyContinue)
}

function Stop-ServerProcesses {
    foreach ($process in Get-ServerProcesses) {
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        $process.WaitForExit(5000) | Out-Null
    }
}

function Register-Server {
    Write-Host 'Registering OPC Foundation local COM test server'
    Invoke-NativeProcess -FilePath $script:ServerExecutable -ArgumentList @('/RegServer')
    $script:ServerRegistered = $true
    Write-Host 'Registered OPC Foundation local COM test server'
}

function Unregister-Server {
    Write-Host 'Unregistering OPC Foundation local COM test server'
    Stop-ServerProcesses
    Invoke-NativeProcess -FilePath $script:ServerExecutable -ArgumentList @('/UnregServer')
    $script:ServerRegistered = $false
    Write-Host 'Unregistered OPC Foundation local COM test server'
}

function Test-LocalDetection {
    $before = @(Get-ServerProcesses)
    Assert-True ($before.Count -eq 0) 'test server was running before registration-only detection'

    $output = @(& $script:AdapterExecutable detect --timeout 10s 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "local detection failed with exit code ${LASTEXITCODE}: $($output -join [Environment]::NewLine)"
    }
    try {
        $detected = ($output -join [Environment]::NewLine) | ConvertFrom-Json
    }
    catch {
        throw "local detection did not return valid JSON: $($_.Exception.Message)"
    }
    Assert-True ($detected.scope -ceq 'local') 'detection scope was not local'
    Assert-True ($detected.category -ceq 'OPC_DA_20') 'detection category was not OPC_DA_20'
    Assert-True ($detected.categoryId -ceq '{63D5F432-CFE4-11D1-B2C8-0060083BA1FB}') `
        'detection category ID was not the official OPC DA 2.0 CATID'
    Assert-True ($detected.detectorArchitecture -ceq $AdapterArch) 'detection architecture did not match the adapter build'

    $matches = @($detected.servers | Where-Object {
        $_.progId -ceq $script:ProgID -and ([Guid]$_.clsid) -eq ([Guid]$expectedCLSID)
    })
    Assert-True ($matches.Count -eq 1) 'registered OPC Foundation DA server was not detected exactly once'
    $after = @(Get-ServerProcesses)
    Assert-True ($after.Count -eq 0) 'registration-only detection activated the vendor DA server'
    Write-Host "Local OPC_DA_20 detection passed without vendor activation: $($script:ProgID)"
}

function Read-Items {
    param([string[]]$ItemIDs)

    $items = @($ItemIDs | ForEach-Object { [ordered]@{ itemId = $_ } })
    $body = ConvertTo-RequestJSON ([ordered]@{ source = 'device'; items = $items })
    $response = Send-AdapterRequest -Method POST -Path '/v1/read' -Body $body
    return Require-Status -Response $response -Expected 200 -Operation 'Read'
}

function Get-ResourceSample {
    param([Diagnostics.Process]$Process)

    $Process.Refresh()
    return [pscustomobject]@{
        Handles = [int64]$Process.HandleCount
        PrivateBytes = [int64]$Process.PrivateMemorySize64
    }
}

$script:AdapterExecutable = (Resolve-Path -LiteralPath $AdapterPath).Path
$script:StabilityProbeExecutable = if ([string]::IsNullOrWhiteSpace($StabilityProbePath)) {
    $null
}
else {
    (Resolve-Path -LiteralPath $StabilityProbePath).Path
}
$script:ServerRoot = (Resolve-Path -LiteralPath $ServerDirectory).Path
$script:WorkingDirectory = Join-Path ([IO.Path]::GetTempPath()) "opcda-adapter-real-da-$AdapterArch-$RunLabel"
if (Test-Path -LiteralPath $script:WorkingDirectory) {
    throw "validation working directory already exists: $script:WorkingDirectory"
}
New-Item -ItemType Directory -Path $script:WorkingDirectory | Out-Null

$serverPlatform = if ($AdapterArch -eq '386') { 'x86' } else { 'x64' }
$script:ServerProcessName = "OpcTestServer_$serverPlatform"
$script:ServerExecutable = Join-Path $script:ServerRoot "$($script:ServerProcessName).exe"
if ($script:ServerExecutable.Contains('-')) {
    throw 'the upstream test server command-line parser requires a registration path without hyphens'
}
$script:ProgID = "OPC.$($script:ServerProcessName).1"
$expectedCLSID = if ($AdapterArch -eq '386') {
    '{F8582CF3-88FB-11DA-A5ED-0060B0692061}'
}
else {
    '{F8582CF8-88FB-11DA-A5ED-0060B0692061}'
}
$script:AppID = if ($AdapterArch -eq '386') {
    '{F8582CF4-88FB-11DA-A5ED-0060B0692061}'
}
else {
    '{F8582CF9-88FB-11DA-A5ED-0060B0692061}'
}
$registryView = if ($AdapterArch -eq '386') {
    [Microsoft.Win32.RegistryView]::Registry32
}
else {
    [Microsoft.Win32.RegistryView]::Registry64
}
$regsvr32 = if ($AdapterArch -eq '386') {
    Join-Path $env:WINDIR 'SysWOW64\regsvr32.exe'
}
else {
    Join-Path $env:WINDIR 'System32\regsvr32.exe'
}
$proxyNames = @('opccomn_ps.dll', 'opcproxy.dll', 'opcsec_ps.dll')
$registeredProxies = [Collections.Generic.List[string]]::new()
$script:ServerRegistered = $false
$adapter = $null
$script:BaseURL = 'http://127.0.0.1:18080'
$script:HTTPClient = [Net.Http.HttpClient]::new()
$script:HTTPClient.Timeout = [TimeSpan]::FromSeconds(12)

try {
    foreach ($proxyName in $proxyNames) {
        $proxyPath = Join-Path $script:ServerRoot $proxyName
        Assert-True (Test-Path -LiteralPath $proxyPath) "missing proxy/stub: $proxyName"
        Write-Host "Registering audited proxy/stub $proxyName"
        Invoke-NativeProcess -FilePath $regsvr32 -ArgumentList @('/s', $proxyPath)
        [void]$registeredProxies.Add($proxyPath)
        Write-Host "Registered audited proxy/stub $proxyName"
    }

    Assert-True (Test-Path -LiteralPath $script:ServerExecutable) 'missing OPC Foundation test server executable'
    Register-Server

    $classes = [Microsoft.Win32.RegistryKey]::OpenBaseKey(
        [Microsoft.Win32.RegistryHive]::ClassesRoot,
        $registryView
    )
    try {
        $classKey = $classes.OpenSubKey("$($script:ProgID)\CLSID")
        Assert-True ($null -ne $classKey) "ProgID was not registered in the expected $serverPlatform registry view"
        try {
            $actualCLSID = [Guid]([string]$classKey.GetValue(''))
            Assert-True ($actualCLSID -eq [Guid]$expectedCLSID) 'registered CLSID does not match the pinned source'
        }
        finally {
            $classKey.Dispose()
        }
    }
    finally {
        $classes.Dispose()
    }

    Write-Host "Registered source=$($script:ProgID) architecture=$serverPlatform using local COM"
    Test-LocalDetection

    if ($Destructive.IsPresent) {
        Write-Host 'Starting destructive local COM permission validation'
        Invoke-DestructiveCOMPermissionValidation -RegistryView $registryView -AppID $script:AppID
        Write-Host 'Completed destructive local COM permission validation'
    }

    $adapter = Start-Adapter -WriteEnabled $false -Label 'write-disabled'
    Write-Host 'Waiting for initial local-COM connection with Write disabled'
    $status = Wait-Connected
    Write-Host 'Initial local-COM connection established'
    Assert-True ($status.capabilities.browse -eq 'supported') 'Browse capability was not detected'
    Assert-True ([bool]$status.capabilities.read) 'Read capability was not reported'
    Assert-True ([bool]$status.capabilities.write) 'Write capability was not reported'
    Assert-True (-not [bool]$status.writeEnabled) 'Write unexpectedly started enabled'
    Assert-True ([bool]$status.frontend.http.listening) 'HTTP listener was not reported as listening'
    $adapterListeners = @(Get-NetTCPConnection -State Listen -OwningProcess $adapter.Id -ErrorAction SilentlyContinue)
    Assert-True ($adapterListeners.Count -eq 1) 'adapter did not expose exactly one TCP listener'
    Assert-True ($adapterListeners[0].LocalAddress -eq '127.0.0.1' -and
        [int]$adapterListeners[0].LocalPort -eq 18080) `
        'default validation listener was reachable beyond IPv4 loopback'

    $disabledResponse = Send-AdapterRequest -Method POST -Path '/v1/write' -Body '{}'
    $disabled = Require-Status -Response $disabledResponse -Expected 403 -Operation 'disabled Write'
    Assert-True ($disabled.error.layer -eq 'adapter') 'disabled Write error layer was not adapter'
    Assert-True ($disabled.error.code -eq 'WRITE_DISABLED') 'disabled Write did not return WRITE_DISABLED'

    $rootBody = ConvertTo-RequestJSON ([ordered]@{ path = @(); filter = 'all' })
    $rootResponse = Send-AdapterRequest -Method POST -Path '/v1/browse' -Body $rootBody
    $root = Require-Status -Response $rootResponse -Expected 200 -Operation 'root Browse'
    $testBranch = @($root.entries | Where-Object { $_.kind -eq 'branch' -and $_.name -eq 'Test' })
    Assert-True ($testBranch.Count -eq 1) 'root Browse did not return the Test branch exactly once'

    $nestedBody = ConvertTo-RequestJSON ([ordered]@{ path = @('Test'); filter = 'all' })
    $nestedResponse = Send-AdapterRequest -Method POST -Path '/v1/browse' -Body $nestedBody
    $nested = Require-Status -Response $nestedResponse -Expected 200 -Operation 'nested Browse'
    Assert-True ($nested.path.Count -eq 1 -and $nested.path[0] -eq 'Test') 'nested Browse path was not preserved'
    $browseItems = @($nested.entries | Where-Object { $_.kind -eq 'item' })
    Assert-True ($browseItems.Count -eq 3) 'nested Browse did not return exactly three configured items'
    foreach ($expectedItemID in @('Test/Int32', 'Test/Float', 'Test/String')) {
        Assert-True (@($browseItems | Where-Object { $_.itemId -ceq $expectedItemID }).Count -eq 1) `
            "Browse did not preserve exact ItemID $expectedItemID"
    }

    $partial = Read-Items @('Test/Int32', '__opcda_adapter_invalid_item__')
    Assert-True ($partial.results.Count -eq 2) 'partial Read result count changed'
    $known = $partial.results[0]
    $unknown = $partial.results[1]
    Assert-True ($known.itemId -ceq 'Test/Int32' -and [bool]$known.ok) 'known Read item failed or moved'
    Assert-True ($known.dataType.name -eq 'VT_I4' -and [int]$known.dataType.code -eq 3) 'actual VARTYPE was not VT_I4'
    Assert-True ($known.canonicalDataType.name -eq 'VT_I4' -and [int]$known.canonicalDataType.code -eq 3) 'canonical VARTYPE was not VT_I4'
    Assert-True ([int]$known.quality -eq 192) 'raw source Quality was not OPC_QUALITY_GOOD (0x00C0)'
    Assert-True (([bool]$known.timestampPresent -and $null -ne $known.timestamp) -or
        (-not [bool]$known.timestampPresent -and $null -eq $known.timestamp)) `
        'source timestamp and timestampPresent contradicted each other'
    Assert-True ([int]$known.hresult.value -eq 0 -and $known.hresult.hex -eq '0x00000000') 'successful item HRESULT was not preserved'
    Assert-True ($unknown.itemId -ceq '__opcda_adapter_invalid_item__' -and -not [bool]$unknown.ok) 'invalid ItemID result failed ordering or success semantics'
    Assert-True ([int]$unknown.hresult.value -lt 0 -and $unknown.hresult.hex -eq '0xC0040007') 'invalid ItemID HRESULT was not OPC_E_UNKNOWNITEMID'

    Stop-Adapter $adapter
    $adapter = $null
    Start-Sleep -Milliseconds 500

    $adapter = Start-Adapter -WriteEnabled $true -Label 'write-enabled'
    Write-Host 'Waiting for local-COM connection with Write explicitly enabled'
    $writeStatus = Wait-Connected
    Write-Host 'Write-enabled local-COM connection established'
    Assert-True ([bool]$writeStatus.writeEnabled) 'explicit Write enablement was not reported'

    $floatRead = Read-Items @('Test/Float')
    $float = $floatRead.results[0]
    Assert-True ([bool]$float.ok -and $float.canonicalDataType.name -eq 'VT_R4') 'safe Write item was not readable as VT_R4'
    Assert-True ([int]$float.accessRights.raw -eq 3 -and [bool]$float.accessRights.read -and [bool]$float.accessRights.write) `
        'read/write access rights were not preserved'

    $mismatchBody = ConvertTo-RequestJSON ([ordered]@{
        items = @([ordered]@{
            itemId = 'Test/Float'
            dataType = 'VT_R8'
            valueEncoding = 'json'
            value = $float.value
        })
    })
    $mismatchResponse = Send-AdapterRequest -Method POST -Path '/v1/write' -Body $mismatchBody
    $mismatch = Require-Status -Response $mismatchResponse -Expected 200 -Operation 'type-mismatch Write'
    Assert-True (-not [bool]$mismatch.results[0].ok -and $mismatch.results[0].errorCode -eq 'TYPE_MISMATCH') `
        'strict typed Write did not reject a canonical VARTYPE mismatch'

    $safeWriteBody = ConvertTo-RequestJSON ([ordered]@{
        items = @([ordered]@{
            itemId = 'Test/Float'
            dataType = $float.canonicalDataType.name
            valueEncoding = $float.valueEncoding
            value = $float.value
        })
    })
    $safeWriteResponse = Send-AdapterRequest -Method POST -Path '/v1/write' -Body $safeWriteBody
    $safeWrite = Require-Status -Response $safeWriteResponse -Expected 200 -Operation 'safe typed Write'
    Assert-True ([bool]$safeWrite.results[0].ok -and [int]$safeWrite.results[0].hresult.value -eq 0) `
        'safe typed value Write failed'

    $stringRead = Read-Items @('Test/String')
    $readOnly = $stringRead.results[0]
    Assert-True ([bool]$readOnly.ok -and $readOnly.canonicalDataType.name -eq 'VT_BSTR') 'read-only BSTR item could not be read'
    Assert-True ([int]$readOnly.accessRights.raw -eq 1 -and [bool]$readOnly.accessRights.read -and -not [bool]$readOnly.accessRights.write) `
        'read-only access rights were not preserved'
    $deniedBody = ConvertTo-RequestJSON ([ordered]@{
        items = @([ordered]@{
            itemId = 'Test/String'
            dataType = $readOnly.canonicalDataType.name
            valueEncoding = $readOnly.valueEncoding
            value = $readOnly.value
        })
    })
    $deniedResponse = Send-AdapterRequest -Method POST -Path '/v1/write' -Body $deniedBody
    $denied = Require-Status -Response $deniedResponse -Expected 200 -Operation 'write-denied source item'
    Assert-True (-not [bool]$denied.results[0].ok -and [int]$denied.results[0].hresult.value -lt 0 -and
        $denied.results[0].hresult.hex -eq '0xC0040006') `
        'source did not deny Write to its read-only item'

    $beforeRestart = Wait-Connected
    $oldGeneration = [uint64]$beforeRestart.source.connectionGeneration
    $oldReconnectCount = [uint64]$beforeRestart.runtime.reconnectCount
    Stop-ServerProcesses
    Unregister-Server
    Write-Host 'OPC DA source intentionally unavailable for reconnect validation'

    $outageBody = ConvertTo-RequestJSON ([ordered]@{
        source = 'device'
        items = @([ordered]@{ itemId = 'Test/Int32' })
    })
    $outageResponse = Send-AdapterRequest -Method POST -Path '/v1/read' -Body $outageBody
    $returnedStaleSuccess = $outageResponse.Status -eq 200 -and
        $null -ne $outageResponse.JSON -and
        $outageResponse.JSON.results.Count -gt 0 -and
        [bool]$outageResponse.JSON.results[0].ok
    Assert-True (-not $returnedStaleSuccess) 'Read returned a successful value while the DA server was unavailable'

    # A Write submitted after the source is unavailable must fail immediately
    # and must not be retained for replay after reconnect. The fixture starts
    # Test/Float at 3.14159, so 17.25 is an unambiguous replay marker.
    $outageWriteBody = ConvertTo-RequestJSON ([ordered]@{
        items = @([ordered]@{
            itemId = 'Test/Float'
            dataType = 'VT_R4'
            valueEncoding = 'json'
            value = 17.25
        })
    })
    $outageWriteResponse = Send-AdapterRequest -Method POST -Path '/v1/write' -Body $outageWriteBody
    Assert-True ($outageWriteResponse.Status -ne 200) `
        'Write submitted during a source outage unexpectedly returned an item result'
    Assert-True ($outageWriteResponse.JSON.error.layer -eq 'adapter' -and
        $outageWriteResponse.JSON.error.code -eq 'RUNTIME_UNAVAILABLE') `
        'Write submitted during a source outage did not fail as RUNTIME_UNAVAILABLE'

    $sawDisconnected = $false
    $stateDeadline = [DateTime]::UtcNow.AddSeconds(10)
    do {
        $stateResponse = Send-AdapterRequest -Method GET -Path '/v1/status'
        if ($stateResponse.Status -eq 200 -and $stateResponse.JSON.state -in @('disconnected', 'reconnecting')) {
            $sawDisconnected = $true
            break
        }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $stateDeadline)
    Assert-True $sawDisconnected 'runtime did not expose disconnected/reconnecting state during a real outage'

    Register-Server
    Write-Host 'Waiting for adapter reconnect with a newer connection generation'
    $reconnected = Wait-Connected -MinimumGeneration ($oldGeneration + 1) -TimeoutSeconds 60
    Write-Host 'Adapter reconnected with a newer connection generation'
    Assert-True ([uint64]$reconnected.source.connectionGeneration -gt $oldGeneration) 'connection generation did not advance after reconnect'
    Assert-True ([uint64]$reconnected.runtime.reconnectCount -gt $oldReconnectCount) 'reconnect count did not advance'
    $afterReconnect = Read-Items @('Test/Int32')
    Assert-True ([bool]$afterReconnect.results[0].ok) 'known ItemID did not lazy re-register after reconnect'
    $afterReconnectFloat = Read-Items @('Test/Float')
    Assert-True ([bool]$afterReconnectFloat.results[0].ok -and
        [double]$afterReconnectFloat.results[0].value -ne 17.25) `
        'outage Write marker appeared after reconnect, indicating forbidden replay'

    for ($crashCycle = 1; $crashCycle -le $AdapterCrashCycles; $crashCycle++) {
        Stop-Adapter $adapter
        $adapter = $null
        Start-Sleep -Milliseconds 100
        $adapter = Start-Adapter -WriteEnabled $true -Label "forced-restart-$crashCycle"
        $crashRecovered = Wait-Connected -TimeoutSeconds 60
        Assert-True ([uint64]$crashRecovered.source.connectionGeneration -eq 1) `
            "forced adapter restart $crashCycle retained a prior-process connection generation"
        $crashRead = Read-Items @('Test/Int32')
        Assert-True ([bool]$crashRead.results[0].ok) `
            "forced adapter restart $crashCycle did not recover a known ItemID"
        Assert-True (@(Get-ServerProcesses).Count -eq 1) `
            "forced adapter restart $crashCycle left an unexpected DA server process count"
        Write-Host "Destructive adapter restart $crashCycle/$AdapterCrashCycles recovered generation=1"
    }

    $adapterBefore = Get-ResourceSample $adapter
    for ($failureCycle = 1; $failureCycle -le $FailureCycles; $failureCycle++) {
        $cycleStatus = Wait-Connected
        $cycleGeneration = [uint64]$cycleStatus.source.connectionGeneration
        $cycleReconnectCount = [uint64]$cycleStatus.runtime.reconnectCount
        Unregister-Server
        Write-Host "Stability failure cycle $failureCycle/${FailureCycles}: source unavailable"

        for ($outageAttempt = 0; $outageAttempt -lt 8; $outageAttempt++) {
            $cycleOutage = $null
            try {
                $cycleOutage = Send-AdapterRequest -Method POST -Path '/v1/read' -Body $outageBody
            }
            catch {
                # A bounded transport failure while the source disappears is acceptable.
            }
            if ($null -ne $cycleOutage) {
                $cycleReturnedSuccess = $cycleOutage.Status -eq 200 -and
                    $null -ne $cycleOutage.JSON -and
                    $cycleOutage.JSON.results.Count -gt 0 -and
                    [bool]$cycleOutage.JSON.results[0].ok
                Assert-True (-not $cycleReturnedSuccess) `
                    "failure cycle $failureCycle returned a successful stale value during outage attempt $outageAttempt"
            }
            Start-Sleep -Milliseconds 50
        }

        $cycleSawDisconnected = $false
        $cycleStateDeadline = [DateTime]::UtcNow.AddSeconds(10)
        do {
            $cycleState = Send-AdapterRequest -Method GET -Path '/v1/status'
            if ($cycleState.Status -eq 200 -and $cycleState.JSON.state -in @('disconnected', 'reconnecting')) {
                $cycleSawDisconnected = $true
                break
            }
            Start-Sleep -Milliseconds 100
        } while ([DateTime]::UtcNow -lt $cycleStateDeadline)
        Assert-True $cycleSawDisconnected "failure cycle $failureCycle did not expose unavailable state"

        Register-Server
        $cycleRecovered = Wait-Connected -MinimumGeneration ($cycleGeneration + 1) -TimeoutSeconds 60
        Assert-True ([uint64]$cycleRecovered.runtime.reconnectCount -gt $cycleReconnectCount) `
            "failure cycle $failureCycle did not advance reconnect count"
        $cycleRead = Read-Items @('Test/Int32')
        Assert-True ([bool]$cycleRead.results[0].ok) `
            "failure cycle $failureCycle did not lazy re-register the known ItemID"
        Write-Host "Stability failure cycle $failureCycle/$FailureCycles recovered generation=$($cycleRecovered.source.connectionGeneration)"
    }

    $serverProcesses = @(Get-ServerProcesses)
    Assert-True ($serverProcesses.Count -eq 1) 'expected exactly one activated OPC test server process before soak'
    $serverBefore = Get-ResourceSample $serverProcesses[0]
    if ($null -ne $script:StabilityProbeExecutable) {
        Write-Host 'Starting HTTP stability profile (normal, invalid, anomalous, rapid, concurrent, overload)'
        Invoke-NativeProcess -FilePath $script:StabilityProbeExecutable -ArgumentList @(
            '-base-url', $script:BaseURL,
            '-rapid-requests', '5000',
            '-workers', '16',
            '-requests-per-worker', '200',
            '-overload-requests', '48',
            '-request-slots', '32',
            '-slow-connections', '48',
            '-header-timeout', '5s',
            '-body-timeout', '15s'
        ) -TimeoutSeconds 1200
        Write-Host 'Completed HTTP stability profile'
    }
    Write-Host "Starting bounded Read soak iterations=$SoakIterations"
    for ($iteration = 0; $iteration -lt $SoakIterations; $iteration++) {
        $soakRead = Read-Items @('Test/Int32')
        Assert-True ([bool]$soakRead.results[0].ok) "soak Read failed at iteration $iteration"
    }
    Write-Host "Completed bounded Read soak iterations=$SoakIterations"
    $adapterAfter = Get-ResourceSample $adapter
    $serverAfterProcesses = @(Get-ServerProcesses)
    Assert-True ($serverAfterProcesses.Count -eq 1) 'expected exactly one activated OPC test server process after soak'
    $serverAfter = Get-ResourceSample $serverAfterProcesses[0]

    $adapterHandleDelta = $adapterAfter.Handles - $adapterBefore.Handles
    $adapterPrivateDelta = $adapterAfter.PrivateBytes - $adapterBefore.PrivateBytes
    $serverHandleDelta = $serverAfter.Handles - $serverBefore.Handles
    $serverPrivateDelta = $serverAfter.PrivateBytes - $serverBefore.PrivateBytes
    Assert-True ($adapterAfter.Handles -lt 2048 -and $adapterHandleDelta -lt 128) 'adapter handle growth exceeded the validation bound'
    Assert-True ($adapterAfter.PrivateBytes -lt 536870912 -and $adapterPrivateDelta -lt 67108864) 'adapter private-byte growth exceeded the validation bound'
    Assert-True ($serverAfter.Handles -lt 2048 -and $serverHandleDelta -lt 128) 'test server handle growth exceeded the validation bound'
    Assert-True ($serverAfter.PrivateBytes -lt 536870912 -and $serverPrivateDelta -lt 67108864) 'test server private-byte growth exceeded the validation bound'

    $valueLogMatches = @(Get-ChildItem -LiteralPath $script:WorkingDirectory -Filter 'adapter-*.log' -File |
        Select-String -SimpleMatch -Pattern '"value":', '"valueEncoding":')
    Assert-True ($valueLogMatches.Count -eq 0) 'adapter logs contained a process-value JSON field'

    $stabilityEnabled = $null -ne $script:StabilityProbeExecutable
    Write-Host "REAL_DA_VALIDATION_PASS arch=$AdapterArch server=$serverPlatform browse=root+nested read=partial write=disabled+typed+denied reconnect=true failureCycles=$FailureCycles destructive=$($Destructive.IsPresent) adapterCrashCycles=$AdapterCrashCycles stability=$stabilityEnabled soakIterations=$SoakIterations"
    Write-Host "READ_METADATA actualType=$($known.dataType.name) canonicalType=$($known.canonicalDataType.name) qualityRaw=$($known.quality) timestampPresent=$($known.timestampPresent) successHRESULT=$($known.hresult.hex) invalidHRESULT=$($unknown.hresult.hex)"
    Write-Host "RESOURCE_DELTAS adapterHandles=$adapterHandleDelta adapterPrivateBytes=$adapterPrivateDelta serverHandles=$serverHandleDelta serverPrivateBytes=$serverPrivateDelta"
}
finally {
    Stop-Adapter $adapter
    Stop-ServerProcesses
    if ($script:ServerRegistered) {
        try {
            Unregister-Server
        }
        catch {
            Write-Warning "test server cleanup failed: $($_.Exception.Message)"
        }
    }
    for ($index = $registeredProxies.Count - 1; $index -ge 0; $index--) {
        try {
            Invoke-NativeProcess -FilePath $regsvr32 -ArgumentList @('/s', '/u', $registeredProxies[$index])
        }
        catch {
            Write-Warning "proxy/stub cleanup failed: $($_.Exception.Message)"
        }
    }
    $script:HTTPClient.Dispose()
}
