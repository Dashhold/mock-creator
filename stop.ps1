# Stop the stack. The database volume is kept unless -Wipe is given.

param(
    [switch]$Wipe
)

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

if ($Wipe) {
    Write-Host 'This deletes the database volume, including every uploaded document.' -ForegroundColor Yellow
    $answer = Read-Host 'Type DELETE to continue'
    if ($answer -ne 'DELETE') {
        Write-Host 'Cancelled.' -ForegroundColor Yellow
        exit 0
    }
    docker compose down -v
    Write-Host 'Stopped and wiped.' -ForegroundColor Green
    exit 0
}

docker compose down
Write-Host ''
Write-Host 'Stopped. Your data is still in the pgdata volume.' -ForegroundColor Green
Write-Host 'Start again with: .\start.ps1'
