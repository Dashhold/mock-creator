@echo off
REM Build and start the whole stack.
cd /d "%~dp0"

echo.
echo Mock Creator
echo ============
echo.

docker info >nul 2>&1
if errorlevel 1 (
    echo Docker is not running. Start Docker Desktop and try again.
    pause
    exit /b 1
)

echo Starting services...
docker compose up -d --build
if errorlevel 1 (
    echo.
    echo Startup failed. Run "docker compose logs" to see why.
    pause
    exit /b 1
)

echo.
docker compose ps

echo.
echo Web interface : http://localhost:3000
echo API           : http://localhost:8090/health
echo Converter     : http://localhost:5001/health
echo.
echo The converter loads its models on first start, which takes a minute or
echo two. The interface works during that time.
echo.
echo Follow the logs with : docker compose logs -f
echo Stop everything with : stop.bat
echo.
pause
