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

rem Quem baixa "Code -> Download ZIP" do GitHub pega so o codigo-fonte: nao
rem tem abraham.exe nenhum ali, so o jeito de compilar um. Sem essa checagem
rem o erro que aparece e generico ("Windows nao encontra o arquivo"), que
rem parece o programa quebrado quando na verdade falta a pasta certa.
if not exist "%~dp0abraham.exe" (
  echo.
  echo   Nao encontrei abraham.exe nesta pasta.
  echo.
  echo   Se voce baixou o codigo-fonte do GitHub ^(botao "Code"^), precisa do
  echo   pacote ja compilado: va em Releases, no repositorio, e baixe o
  echo   abraham-windows.zip. Descompacte tudo e rode instalar.bat de la.
  echo.
  echo   Se voce tem o Go instalado e quer compilar voce mesmo, rode
  echo   build.ps1 primeiro e depois use o instalar.bat que fica em dist\.
  echo.
  pause
  exit /b 1
)

set "DESTINO=%LOCALAPPDATA%\Abraham"

if not exist "%DESTINO%" mkdir "%DESTINO%"
copy /Y "%~dp0abraham.exe" "%DESTINO%" >nul
if exist "%~dp0README.md" copy /Y "%~dp0README.md" "%DESTINO%" >nul

rem Arquivo baixado do GitHub (zip) vem marcado pelo Windows como "veio da
rem internet" (Mark-of-the-Web). E esse rotulo, nao o programa em si, que faz
rem o SmartScreen aparecer ao abrir o abraham.exe pela primeira vez. Como a
rem pessoa acabou de escolher instalar isto, tira o rotulo so da copia que
rem vai ficar em uso — nao mexe em antivirus nem desliga protecao nenhuma,
rem so evita o aviso repetido num arquivo que ela mesma baixou e instalou.
powershell -NoProfile -Command "Unblock-File -Path '%DESTINO%\abraham.exe'" >nul 2>&1

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
