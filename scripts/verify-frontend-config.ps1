# Verifies the frontend's nginx configuration and entrypoint script can actually
# run in a container. Downloads nginx for Windows and busybox-w32 to a temporary
# directory, renders the template, runs the entrypoint script, checks nginx can
# parse the result, then cleans up.
#
#   powershell -ExecutionPolicy Bypass -File .\scripts\verify-frontend-config.ps1
param(
    [string]$NginxVersion = "1.27.3"
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Say($msg) { Write-Host "    $msg" }
function Check($what, $ok, $details = '') {
    if ($ok) {
        Write-Host "    [PASS] $what" -ForegroundColor Green
    } else {
        Write-Host "    [FAIL] $what" -ForegroundColor Red
        if ($details) { Write-Host "      $details" -ForegroundColor Red }
        throw "check failed: $what"
    }
}

$tempDir = Join-Path $env:TEMP "mock-verify-$(Get-Random)"
New-Item -ItemType Directory -Path $tempDir | Out-Null
Say "temporary directory: $tempDir"

try {
    # ---------------------------------------------------------------------------
    Step "Download nginx for Windows"
    $nginxZip = Join-Path $tempDir "nginx.zip"
    Invoke-WebRequest -Uri "https://nginx.org/download/nginx-$NginxVersion.zip" `
        -OutFile $nginxZip
    Expand-Archive -Path $nginxZip -DestinationPath $tempDir -Force
    $nginxDir = Get-ChildItem $tempDir -Filter "nginx-*" -Directory |
        Select-Object -First 1 -ExpandProperty FullName
    $nginxExe = Join-Path $nginxDir "nginx.exe"
    Check "nginx.exe exists" (Test-Path $nginxExe)
    $nginxVersion = (& $nginxExe -v 2>&1) -join "`n"
    Say $nginxVersion

    # ---------------------------------------------------------------------------
    Step "Download busybox-w32"
    $busyboxExe = Join-Path $tempDir "busybox.exe"
    Invoke-WebRequest -Uri "https://frippery.org/files/busybox/busybox.exe" `
        -OutFile $busyboxExe
    Check "busybox.exe exists" (Test-Path $busyboxExe)
    $busyboxVersion = (& $busyboxExe 2>&1 | Select-Object -First 1)
    Say $busyboxVersion

    # ---------------------------------------------------------------------------
    Step "Create a mock container filesystem"
    $root = Join-Path $tempDir "root"
    $dirs = @(
        "etc/nginx/conf.d",
        "etc/nginx/templates",
        "etc/nginx/mock-creator",
        "docker-entrypoint.d",
        "proc/net",
        "usr/share/nginx/html"
    )
    foreach ($dir in $dirs) {
        New-Item -ItemType Directory -Path (Join-Path $root $dir) -Force | Out-Null
    }

    # A minimal resolv.conf: one IPv4 and one IPv6 nameserver
    $resolvConfContent = @'
nameserver 127.0.0.11
nameserver fd00::1
'@
    Set-Content -Path (Join-Path $root "etc/resolv.conf") -Value $resolvConfContent

    # /proc/net/if_inet6 exists when the container has IPv6
    New-Item -ItemType File -Path (Join-Path $root "proc/net/if_inet6") -Force | Out-Null

    # A minimal nginx.conf that includes the rendered default.conf
    $nginxConfContent = @'
worker_processes 1;
events { worker_connections 1024; }
http {
    include /etc/nginx/conf.d/*.conf;
}
'@
    Set-Content -Path (Join-Path $root "etc/nginx/nginx.conf") -Value $nginxConfContent

    # The template and entrypoint script from the repository
    Copy-Item "frontend/nginx.conf" (Join-Path $root "etc/nginx/templates/default.conf.template")
    Copy-Item "frontend/docker-entrypoint.d/40-mock-creator.sh" (Join-Path $root "docker-entrypoint.d/40-mock-creator.sh")

    # A stub index.html so nginx does not complain about a missing root
    Set-Content -Path (Join-Path $root "usr/share/nginx/html/index.html") -Value "<html><body>ok</body></html>"

    Check "mock filesystem created" (Test-Path (Join-Path $root "etc/nginx/nginx.conf"))

    # ---------------------------------------------------------------------------
    Step "Render the template with envsubst"
    $template = Get-Content (Join-Path $root "etc/nginx/templates/default.conf.template") -Raw
    $rendered = $template -replace '\$\{PORT\}', '8080' -replace '\$\{API_UPSTREAM\}', 'api.example.com:8080'
    Set-Content -Path (Join-Path $root "etc/nginx/conf.d/default.conf") -Value $rendered -NoNewline
    Check "template rendered" ($rendered -match 'listen 8080;')
    Check "API_UPSTREAM substituted" ($rendered -match 'api\.example\.com')

    # ---------------------------------------------------------------------------
    Step "Run the entrypoint script"
    $env:PORT = "8080"
    $env:API_UPSTREAM = "api.example.com:8080"
    $env:BASIC_AUTH_USER = "admin"
    $env:BASIC_AUTH_PASSWORD = "testpass"
    $env:API_KEY = "test-key-12345"

    # busybox sh runs the script. The command is passed as a single string to sh -c.
    $scriptPath = Join-Path $root "docker-entrypoint.d/40-mock-creator.sh"
    $busyboxCmd = "cd '$root'; sh docker-entrypoint.d/40-mock-creator.sh"
    $scriptOutput = & $busyboxExe sh -c $busyboxCmd 2>&1
    $scriptOutput | ForEach-Object { Say $_ }

    Check "entrypoint script ran" ($LASTEXITCODE -eq 0) "exit code: $LASTEXITCODE"

    # The script should have written these files
    $generatedFiles = @(
        "etc/nginx/mock-creator/listen-ipv6.conf",
        "etc/nginx/mock-creator/resolver.conf",
        "etc/nginx/mock-creator/auth.conf",
        "etc/nginx/mock-creator/htpasswd",
        "etc/nginx/mock-creator/api-key.conf"
    )
    foreach ($file in $generatedFiles) {
        $fullPath = Join-Path $root $file
        Check "$file exists" (Test-Path $fullPath)
    }

    # ---------------------------------------------------------------------------
    Step "Check the generated configuration files"
    $listenIpv6 = Get-Content (Join-Path $root "etc/nginx/mock-creator/listen-ipv6.conf") -Raw
    Check "listen-ipv6.conf has [::]" ($listenIpv6 -match '\[::\]:8080')

    $resolver = Get-Content (Join-Path $root "etc/nginx/mock-creator/resolver.conf") -Raw
    Check "resolver.conf has nameservers" ($resolver -match 'resolver.*127\.0\.0\.11')
    Check "resolver.conf brackets IPv6" ($resolver -match '\[fd00::1\]')

    $auth = Get-Content (Join-Path $root "etc/nginx/mock-creator/auth.conf") -Raw
    Check "auth.conf has auth_basic" ($auth -match 'auth_basic')

    $htpasswd = Get-Content (Join-Path $root "etc/nginx/mock-creator/htpasswd") -Raw
    Check "htpasswd has admin user" ($htpasswd -match '^admin:')
    Check "htpasswd has apr1 hash" ($htpasswd -match '\$apr1\$')

    $apiKey = Get-Content (Join-Path $root "etc/nginx/mock-creator/api-key.conf") -Raw
    Check "api-key.conf has proxy_set_header" ($apiKey -match 'proxy_set_header X-API-Key')
    Check "api-key.conf has the key" ($apiKey -match 'test-key-12345')

    # ---------------------------------------------------------------------------
    Step "Check nginx can parse the configuration"
    # nginx -t checks the config. It needs -p to set the prefix (where it looks
    # for relative paths), and -c for the config file.
    $testOutput = & $nginxExe -p $root -c "etc/nginx/nginx.conf" -t 2>&1
    $testOutput | ForEach-Object { Say $_ }
    Check "nginx -t succeeded" ($LASTEXITCODE -eq 0) "exit code: $LASTEXITCODE"
    Check "nginx reports syntax is ok" ($testOutput -match 'syntax is ok')
    Check "nginx reports test is successful" ($testOutput -match 'test is successful')

    # ---------------------------------------------------------------------------
    Step "Check the JSON schema references are valid"
    $railwayJsonFiles = @(
        "backend/railway.json",
        "services/converter/railway.json",
        "frontend/railway.json"
    )
    foreach ($file in $railwayJsonFiles) {
        $json = Get-Content $file -Raw | ConvertFrom-Json
        Check "$file has schema reference" ($json.'$schema' -eq 'https://railway.com/railway.schema.json')
        Check "$file has builder" ($json.build.builder -ne $null)
        Check "$file has watchPatterns" ($json.build.watchPatterns.Count -ge 1)
        Check "$file has healthcheckPath" ($json.deploy.healthcheckPath -ne $null)
    }

    # ---------------------------------------------------------------------------
    Write-Host ""
    Write-Host "All checks passed." -ForegroundColor Green
}
finally {
    # ---------------------------------------------------------------------------
    Step "Clean up"
    Remove-Item -Recurse -Force $tempDir
    Say "removed $tempDir"
}
