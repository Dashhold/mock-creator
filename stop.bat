@echo off
REM Stop the stack, keeping the database.
cd /d "%~dp0"

docker compose down

echo.
echo Stopped. Your data is still in the pgdata volume.
echo Start again with: start.bat
echo To erase the database as well: docker compose down -v
echo.
pause
