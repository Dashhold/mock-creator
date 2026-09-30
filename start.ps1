# Build and start the whole stack.
#
# Usage:
#   .\start.ps1            build anything stale and start
#   .\start.ps1 -Fresh     wipe the database first (destructive)
#   .\start.ps1 -Rebuild   force a full rebuild with no cache

param(
    [switch]$Fresh,
    [switch]$Rebuild
)

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

Write-Host ''
Write-Host 'Mock Creator' -ForegroundColor Cyan
Write-Host '============' -ForegroundColor Cyan
Write-Host ''

# Docker has to be running before anything else is worth trying.
docker info *> $null
if ($LASTEXITCODE -ne 0) {
    Write-Host 'Docker is not running. Start Docker Desktop and try again.' -ForegroundColor Red
    exit 1
}

if ($Fresh) {
    Write-Host 'This deletes the database volume, including every uploaded document.' -ForegroundColor Yellow
    $answer = Read-Host 'Type DELETE to continue'
    if ($answer -ne 'DELETE') {
        Write-Host 'Cancelled.' -ForegroundColor Yellow
        exit 0
    }
    docker compose down -v
}

if ($Rebuild) {
    Write-Host 'Building images from scratch. The converter image is large, so expect 10-20 minutes.' -ForegroundColor Yellow
    docker compose build --no-cache
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

Write-Host 'Starting services...' -ForegroundColor Cyan
docker compose up -d --build
if ($LASTEXITCODE -ne 0) {
    Write-Host ''
    Write-Host 'Startup failed. Check the output above, or run: docker compose logs' -ForegroundColor Red
    exit $LASTEXITCODE
}

Write-Host ''
docker compose ps

Write-Host ''
Write-Host 'Web interface : http://localhost:3000' -ForegroundColor Green
Write-Host 'API           : http://localhost:8090/health'
Write-Host 'Converter     : http://localhost:5001/health'
Write-Host ''
Write-Host 'The converter downloads nothing at runtime, but it does load its models'
Write-Host 'on first start, which takes a minute or two. The interface works during'
Write-Host 'that time and shows the converter as warming up.'
Write-Host ''
Write-Host 'Follow the logs with : docker compose logs -f'
Write-Host 'Stop everything with : .\stop.ps1'
Write-Host ''
