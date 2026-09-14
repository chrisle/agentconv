$ErrorActionPreference = 'Stop'

$repo = 'chrisle/agentconv'
if (-not [Environment]::Is64BitOperatingSystem) {
    throw 'agentconv currently provides a Windows x64 release only.'
}

$destination = Join-Path $HOME '.local\bin'
New-Item -ItemType Directory -Path $destination -Force | Out-Null
$url = "https://github.com/$repo/releases/latest/download/agentconv-windows-amd64.exe"
Invoke-WebRequest -Uri $url -OutFile (Join-Path $destination 'agentconv.exe')

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $destination) {
    [Environment]::SetEnvironmentVariable('Path', "$destination;$userPath", 'User')
}
$env:Path = "$destination;$env:Path"
Write-Host "Installed agentconv to $destination\agentconv.exe"
