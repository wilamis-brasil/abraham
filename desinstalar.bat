@echo off
setlocal
chcp 65001 >nul

rem desinstalar.bat — tira o Abraham do PATH e apaga o programa e os atalhos.
rem
rem Os DADOS nao sao apagados sem voce mandar: dados\abraham.db contem a
rem sessao do WhatsApp, que vale como senha.

set "DESTINO=%LOCALAPPDATA%\Abraham"

echo.
echo   Removendo o Abraham de %DESTINO%
echo.

powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$d = [Environment]::GetFolderPath('LocalApplicationData') + '\Abraham';" ^
  "$p = [Environment]::GetEnvironmentVariable('Path', 'User');" ^
  "$novo = ($p -split ';' ^| Where-Object { $_ -and $_ -ne $d }) -join ';';" ^
  "[Environment]::SetEnvironmentVariable('Path', $novo, 'User');" ^
  "Remove-Item ([Environment]::GetFolderPath('Desktop') + '\Abraham.lnk') -ErrorAction SilentlyContinue;" ^
  "Remove-Item ([Environment]::GetFolderPath('Programs') + '\Abraham.lnk') -ErrorAction SilentlyContinue;" ^
  "Write-Host '  PATH e atalhos removidos.'"

del /Q "%DESTINO%\abraham.exe" 2>nul
del /Q "%DESTINO%\README.md" 2>nul

echo.
if exist "%DESTINO%\dados" (
  echo   Os dados NAO foram apagados. Eles estao em:
  echo   %DESTINO%\dados
  echo.
  echo   Apague essa pasta a mao se quiser desconectar o WhatsApp de vez.
) else (
  rd /S /Q "%DESTINO%" 2>nul
  echo   Removido por completo.
)
echo.
pause
