# Установка balanda на Windows:
#
#   irm https://raw.githubusercontent.com/Ar4ikov/balanda/main/install.ps1 | iex
#
# Переменные окружения:
#   BALANDA_VERSION      версия, например v0.1.0; по умолчанию последний релиз
#   BALANDA_INSTALL_DIR  куда положить balanda.exe;
#                        по умолчанию %LOCALAPPDATA%\Programs\balanda
#   BALANDA_LOCAL_DIR    взять архив и checksums.txt из этого каталога, а не
#                        с GitHub (так CI проверяет скрипт на snapshot-сборке)
#
# Скрипт выполняется через iex в текущей сессии, поэтому ошибки
# выбрасываются через throw, а не через exit: exit закрыл бы окно.

& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    $repo = 'Ar4ikov/balanda'

    $cpu = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $cpu = $env:PROCESSOR_ARCHITEW6432 }
    switch ($cpu) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default { throw "balanda: архитектура $cpu не поддерживается, есть сборки для amd64 и arm64" }
    }
    $archive = "balanda_windows_$arch.zip"

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('balanda-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        $zip = Join-Path $tmp $archive
        $sums = Join-Path $tmp 'checksums.txt'
        if ($env:BALANDA_LOCAL_DIR) {
            Write-Host "Беру $archive из $env:BALANDA_LOCAL_DIR"
            Copy-Item (Join-Path $env:BALANDA_LOCAL_DIR $archive) $zip
            Copy-Item (Join-Path $env:BALANDA_LOCAL_DIR 'checksums.txt') $sums
        } else {
            if ($env:BALANDA_VERSION) {
                $version = $env:BALANDA_VERSION
                if (-not $version.StartsWith('v')) { $version = "v$version" }
                $base = "https://github.com/$repo/releases/download/$version"
            } else {
                $base = "https://github.com/$repo/releases/latest/download"
            }
            [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
            Write-Host "Скачиваю $base/$archive"
            Invoke-WebRequest -UseBasicParsing -Uri "$base/$archive" -OutFile $zip
            Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile $sums
        }

        $expected = $null
        foreach ($line in Get-Content $sums) {
            $parts = $line -split '\s+'
            if ($parts.Count -ge 2 -and $parts[1].TrimStart('*') -eq $archive) { $expected = $parts[0].ToLower() }
        }
        if (-not $expected) { throw "balanda: в checksums.txt нет строки для $archive" }
        $actual = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLower()
        if ($expected -ne $actual) { throw "balanda: контрольная сумма $archive не сошлась: ждали $expected, получили $actual" }
        Write-Host 'sha256 сошлась'

        $unpacked = Join-Path $tmp 'x'
        Expand-Archive -Path $zip -DestinationPath $unpacked -Force

        $dir = $env:BALANDA_INSTALL_DIR
        if (-not $dir) { $dir = Join-Path $env:LOCALAPPDATA 'Programs\balanda' }
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        Copy-Item (Join-Path $unpacked 'balanda.exe') (Join-Path $dir 'balanda.exe') -Force

        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        if (-not $userPath) { $userPath = '' }
        $inPath = $userPath.Split(';') | Where-Object { $_.TrimEnd('\') -ieq $dir.TrimEnd('\') }
        if (-not $inPath) {
            $newPath = (($userPath.TrimEnd(';')), $dir | Where-Object { $_ }) -join ';'
            [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
            $env:Path = "$env:Path;$dir"
            Write-Host "Добавил $dir в пользовательский PATH. Новые терминалы увидят его сразу, этот — тоже."
        }

        $ver = & (Join-Path $dir 'balanda.exe') version --short
        Write-Host "Готово: $ver в $dir\balanda.exe"
        Write-Host 'Проверьте: balanda travit'
    } finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }
}
