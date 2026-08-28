@echo off
setlocal
chcp 65001 >nul

rem instalar.bat — clica, instala, abre. Sem pausa esperando tecla: quem
rem clicou aqui quer ver o Abraham na tela, nao ler um relatorio de instalacao.
rem
rem Cria atalho na Area de Trabalho, atalho pesquisavel no Menu Iniciar, e
rem deixa o comando "abraham" disponivel em qualquer terminal. Nao instala
rem Go, Node, Docker nem nada — so copia o abraham.exe.
rem
rem Os atalhos apontam direto para o abraham.exe, nunca para um .bat: atalho
rem para .bat abre com o icone do cmd.exe, nao o do programa.

set "DESTINO=%LOCALAPPDATA%\Abraham"

if not exist "%DESTINO%" mkdir "%DESTINO%"
copy /Y "%~dp0abraham.exe" "%DESTINO%" >nul
if exist "%~dp0README.md" copy /Y "%~dp0README.md" "%DESTINO%" >nul

rem O PATH do usuario e mexido pelo .NET, e NAO por setx: o setx trunca o
rem PATH em 1024 caracteres e ja destruiu o PATH de muita gente.
powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$d = [Environment]::GetFolderPath('LocalApplicationData') + '\Abraham';" ^
  "$exe = $d + '\abraham.exe';" ^
  "$p = [Environment]::GetEnvironmentVariable('Path', 'User');" ^
  "if ($p -split ';' -notcontains $d) {" ^
  "  [Environment]::SetEnvironmentVariable('Path', ($p.TrimEnd(';') + ';' + $d), 'User');" ^
  "}" ^
  "$shell = New-Object -ComObject WScript.Shell;" ^
  "$area = $shell.CreateShortcut([Environment]::GetFolderPath('Desktop') + '\Abraham.lnk');" ^
  "$area.TargetPath = $exe; $area.WorkingDirectory = $d; $area.IconLocation = $exe + ',0'; $area.Save();" ^
  "$menu = [Environment]::GetFolderPath('Programs');" ^
  "$inicio = $shell.CreateShortcut($menu + '\Abraham.lnk');" ^
  "$inicio.TargetPath = $exe; $inicio.WorkingDirectory = $d; $inicio.IconLocation = $exe + ',0'; $inicio.Save();"

rem Depois de instalado, um atalho na Area de Trabalho e um no Menu Iniciar
rem ja resolvem os proximos cliques. Este aqui abre o Abraham agora, para
rem quem clicou em "instalar" ver o programa na hora, sem precisar caçar nada.
start "" "%DESTINO%\abraham.exe"
